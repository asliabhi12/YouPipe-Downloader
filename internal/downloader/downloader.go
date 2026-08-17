package downloader

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Common errors
var (
	ErrInvalidURL    = errors.New("invalid_url")
	ErrYtdlpMissing  = errors.New("yt_dlp_missing")
	ErrFfmpegMissing = errors.New("ffmpeg_missing")
	ErrMetadataFailed = errors.New("metadata_failed")
	ErrDownloadFailed = errors.New("download_failed")
	ErrCancelled      = errors.New("cancelled")
)

type Format struct {
	Quality string `json:"quality"`
	Height  int    `json:"height"`
}

type Metadata struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Thumbnail string   `json:"thumbnail"`
	Duration  float64  `json:"duration"`
	Uploader  string   `json:"uploader"`
	Formats   []Format `json:"formats"`
}

type Progress struct {
	Status          string  `json:"status"` // queued, downloading, processing, completed, failed, cancelled
	DownloadedBytes int64   `json:"downloaded_bytes"`
	TotalBytes      int64   `json:"total_bytes"`
	Progress        float64 `json:"progress"` // percentage 0-100
	Speed           int64   `json:"speed"`    // bytes per sec
	ETA             int64   `json:"eta"`      // seconds
}

type Downloader interface {
	Metadata(ctx context.Context, rawURL string) (*Metadata, error)
	Download(ctx context.Context, rawURL string, quality string, outputDir string, progressCb func(Progress)) error
	CheckDependencies() (ytdlp bool, ffmpeg bool)
}

type YTDLPDownloader struct {
	YtdlpPath  string
	FfmpegPath string
}

func NewYTDLPDownloader() *YTDLPDownloader {
	yPath, _ := exec.LookPath("yt-dlp")
	fPath, _ := exec.LookPath("ffmpeg")
	return &YTDLPDownloader{
		YtdlpPath:  yPath,
		FfmpegPath: fPath,
	}
}

func ValidateURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return fmt.Errorf("%w: empty URL", ErrInvalidURL)
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: scheme must be http or https", ErrInvalidURL)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: missing host", ErrInvalidURL)
	}
	return nil
}

func GetDefaultDownloadsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	dir := filepath.Join(homeDir, "Downloads", "YTD Local")
	return dir, nil
}

func (d *YTDLPDownloader) CheckDependencies() (bool, bool) {
	ytdlp := d.YtdlpPath != ""
	if !ytdlp {
		if path, err := exec.LookPath("yt-dlp"); err == nil {
			d.YtdlpPath = path
			ytdlp = true
		}
	}
	ffmpeg := d.FfmpegPath != ""
	if !ffmpeg {
		if path, err := exec.LookPath("ffmpeg"); err == nil {
			d.FfmpegPath = path
			ffmpeg = true
		}
	}
	return ytdlp, ffmpeg
}

type ytdlpFormatRaw struct {
	Height int    `json:"height"`
	VCodec string `json:"vcodec"`
	ACodec string `json:"acodec"`
}

type ytdlpJSONRaw struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Thumbnail string           `json:"thumbnail"`
	Duration  float64          `json:"duration"`
	Uploader  string           `json:"uploader"`
	Channel   string           `json:"channel"`
	Formats   []ytdlpFormatRaw `json:"formats"`
}

func (d *YTDLPDownloader) Metadata(ctx context.Context, rawURL string) (*Metadata, error) {
	if err := ValidateURL(rawURL); err != nil {
		return nil, err
	}

	ytdlpAvail, _ := d.CheckDependencies()
	if !ytdlpAvail {
		return nil, ErrYtdlpMissing
	}

	cmd := exec.CommandContext(ctx, d.YtdlpPath,
		"--dump-json",
		"--no-warnings",
		"--no-playlist",
		rawURL,
	)

	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("%w: %v", ErrMetadataFailed, err)
	}

	var raw ytdlpJSONRaw
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("%w: failed to parse metadata JSON: %v", ErrMetadataFailed, err)
	}

	uploader := raw.Uploader
	if uploader == "" {
		uploader = raw.Channel
	}

	// Extract qualities
	heightMap := make(map[int]bool)
	for _, f := range raw.Formats {
		if f.Height > 0 && f.VCodec != "none" {
			heightMap[f.Height] = true
		}
	}

	var heights []int
	for h := range heightMap {
		heights = append(heights, h)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(heights)))

	var formats []Format
	for _, h := range heights {
		formats = append(formats, Format{
			Quality: fmt.Sprintf("%dp", h),
			Height:  h,
		})
	}

	// Fallback standard format list if none extracted
	if len(formats) == 0 {
		formats = []Format{
			{Quality: "1080p", Height: 1080},
			{Quality: "720p", Height: 720},
			{Quality: "480p", Height: 480},
			{Quality: "360p", Height: 360},
		}
	}

	meta := &Metadata{
		ID:        raw.ID,
		Title:     raw.Title,
		Thumbnail: raw.Thumbnail,
		Duration:  raw.Duration,
		Uploader:  uploader,
		Formats:   formats,
	}

	return meta, nil
}

func mapQualityToFormatFlag(quality string) string {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "best":
		return "bestvideo+bestaudio/best"
	case "1080p":
		return "bestvideo[height<=1080]+bestaudio/best[height<=1080]"
	case "720p":
		return "bestvideo[height<=720]+bestaudio/best[height<=720]"
	case "480p":
		return "bestvideo[height<=480]+bestaudio/best[height<=480]"
	case "360p":
		return "bestvideo[height<=360]+bestaudio/best[height<=360]"
	case "audio":
		return "bestaudio/best"
	default:
		if strings.HasSuffix(quality, "p") {
			numStr := strings.TrimSuffix(quality, "p")
			if h, err := strconv.Atoi(numStr); err == nil && h > 0 {
				return fmt.Sprintf("bestvideo[height<=%d]+bestaudio/best[height<=%d]", h, h)
			}
		}
		return "bestvideo+bestaudio/best"
	}
}

func (d *YTDLPDownloader) Download(
	ctx context.Context,
	rawURL string,
	quality string,
	outputDir string,
	progressCb func(Progress),
) error {
	if err := ValidateURL(rawURL); err != nil {
		return err
	}

	ytdlpAvail, ffmpegAvail := d.CheckDependencies()
	if !ytdlpAvail {
		return ErrYtdlpMissing
	}
	if !ffmpegAvail {
		return ErrFfmpegMissing
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	formatArg := mapQualityToFormatFlag(quality)
	outputTemplate := filepath.Join(outputDir, "%(title)s [%(id)s].%(ext)s")

	cmd := exec.CommandContext(ctx, d.YtdlpPath,
		"--newline",
		"--no-playlist",
		"--progress-template", "%(progress.status)s|%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress.total_bytes_estimate)s|%(progress.speed)s|%(progress.eta)s",
		"-f", formatArg,
		"-o", outputTemplate,
		rawURL,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}

	// Read output concurrently
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			// keep stderr draining
		}
	}()

	scanner := bufio.NewScanner(stdout)
	lastProgress := Progress{Status: "downloading"}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) == 6 {
			statusStr := parts[0]
			downloaded, _ := strconv.ParseInt(parts[1], 10, 64)
			total, _ := strconv.ParseInt(parts[2], 10, 64)
			totalEst, _ := strconv.ParseInt(parts[3], 10, 64)
			speed, _ := strconv.ParseInt(parts[4], 10, 64)
			eta, _ := strconv.ParseInt(parts[5], 10, 64)

			if total <= 0 {
				total = totalEst
			}

			var pct float64
			if total > 0 {
				pct = (float64(downloaded) / float64(total)) * 100.0
				if pct > 100.0 {
					pct = 100.0
				}
			}

			status := "downloading"
			if statusStr == "finished" {
				status = "processing"
			}

			lastProgress = Progress{
				Status:          status,
				DownloadedBytes: downloaded,
				TotalBytes:      total,
				Progress:        pct,
				Speed:           speed,
				ETA:             eta,
			}
			if progressCb != nil {
				progressCb(lastProgress)
			}
		} else if strings.Contains(line, "[Merger]") || strings.Contains(line, "[ExtractAudio]") || strings.Contains(line, "[ffmpeg]") || strings.Contains(line, "[Postprocess]") {
			lastProgress.Status = "processing"
			if progressCb != nil {
				progressCb(lastProgress)
			}
		}
	}

	err = cmd.Wait()
	if ctx.Err() != nil {
		return ErrCancelled
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}

	lastProgress.Status = "completed"
	lastProgress.Progress = 100.0
	lastProgress.ETA = 0
	lastProgress.Speed = 0
	if progressCb != nil {
		progressCb(lastProgress)
	}

	return nil
}
