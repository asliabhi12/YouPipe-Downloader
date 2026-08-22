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

	"ytd-local/internal/binpath"
)

// Common errors
var (
	ErrInvalidURL     = errors.New("invalid_url")
	ErrYtdlpMissing   = errors.New("yt_dlp_missing")
	ErrFfmpegMissing  = errors.New("ffmpeg_missing")
	ErrMetadataFailed = errors.New("metadata_failed")
	ErrDownloadFailed = errors.New("download_failed")
	ErrCancelled      = errors.New("cancelled")
	ErrFileNotFound   = errors.New("file_not_found")
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
	YtdlpPath   string
	FfmpegPath  string
	FfprobePath string
}

func NewYTDLPDownloader() *YTDLPDownloader {
	return &YTDLPDownloader{
		YtdlpPath:   binpath.Resolve(binpath.Ytdlp),
		FfmpegPath:  binpath.Resolve(binpath.Ffmpeg),
		FfprobePath: binpath.Resolve(binpath.Ffprobe),
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
		return fmt.Errorf("%w: scheme must be http or pages must be http/https", ErrInvalidURL)
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
	if d.YtdlpPath == "" {
		d.YtdlpPath = binpath.Resolve(binpath.Ytdlp)
	}
	if d.FfmpegPath == "" {
		d.FfmpegPath = binpath.Resolve(binpath.Ffmpeg)
	}
	if d.FfprobePath == "" {
		d.FfprobePath = binpath.Resolve(binpath.Ffprobe)
	}
	return d.YtdlpPath != "", d.FfmpegPath != ""
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
		"--extractor-args", "youtube:player_client=web_embedded",
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
	q := strings.ToLower(strings.TrimSpace(quality))
	switch q {
	case "best":
		return "bestvideo+bestaudio/best"
	case "1080p":
		return "bestvideo[height<=1080]+bestaudio/best[height<=1080]/best"
	case "720p":
		return "bestvideo[height<=720]+bestaudio/best[height<=720]/best"
	case "480p":
		return "bestvideo[height<=480]+bestaudio/best[height<=480]/best"
	case "360p":
		return "bestvideo[height<=360]+bestaudio/best[height<=360]/best"
	case "audio":
		return "bestaudio/best"
	default:
		if strings.HasSuffix(q, "p") {
			numStr := strings.TrimSuffix(q, "p")
			if h, err := strconv.Atoi(numStr); err == nil && h > 0 {
				return fmt.Sprintf("bestvideo[height<=%d]+bestaudio/best[height<=%d]/best", h, h)
			}
		}
		return "bestvideo+bestaudio/best"
	}
}

func verifyOutputFileExists(outputDir string, qualityTag string) error {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("failed to read output directory: %w", err)
	}

	tagLower := strings.ToLower(qualityTag)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())

		// Exclude temporary download files
		if strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".ytdl") {
			continue
		}

		if strings.Contains(name, fmt.Sprintf("[%s]", tagLower)) {
			info, err := entry.Info()
			if err == nil && info.Size() > 0 {
				return nil
			}
		}
	}

	return fmt.Errorf("%w: no completed file with quality [%s] found in %s", ErrFileNotFound, qualityTag, outputDir)
}

// ffmpegLocation returns the value for yt-dlp's --ffmpeg-location, or "" when
// there is nothing to point at.
//
// The directory is passed rather than the ffmpeg binary itself: yt-dlp looks for
// ffprobe alongside it, and probing is what tells it whether a merge is needed.
// Passing just the ffmpeg path leaves ffprobe to be found on PATH, which on a
// clean machine does not exist.
func ffmpegLocation(ffmpegPath string) string {
	if ffmpegPath == "" {
		return ""
	}
	return filepath.Dir(ffmpegPath)
}

// buildDownloadArgs assembles the full yt-dlp argument list.
//
// Split out from Download so the flags that decide correctness — the extractor
// client, the MP4/MP3 container contract, and the bundled-FFmpeg location — can
// be asserted in tests without running a download.
func buildDownloadArgs(rawURL, qClean, outputDir, ffmpegPath string) []string {
	args := []string{
		"--newline",
		"--no-playlist",
		"--extractor-args", "youtube:player_client=web_embedded",
		"--progress-template", "%(progress.status)s|%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress.total_bytes_estimate)s|%(progress.speed)s|%(progress.eta)s",
	}

	// Without this a packaged Helper would ignore its own bundled FFmpeg and
	// search PATH instead, so merging and MP3 extraction would fail on a machine
	// where the user has installed nothing.
	if loc := ffmpegLocation(ffmpegPath); loc != "" {
		args = append(args, "--ffmpeg-location", loc)
	}

	// Distinct output template per quality to avoid filename collisions
	outputTemplate := filepath.Join(outputDir, fmt.Sprintf("%%(title)s [%%(id)s] [%s].%%(ext)s", qClean))

	if strings.EqualFold(qClean, "audio") {
		// Audio extraction to MP3 via FFmpeg
		args = append(args,
			"-x",
			"--audio-format", "mp3",
			"--audio-quality", "0",
			"-f", "bestaudio/best",
			"-o", outputTemplate,
			rawURL,
		)
	} else {
		args = append(args,
			"--merge-output-format", "mp4",
			"-f", mapQualityToFormatFlag(qClean),
			"-o", outputTemplate,
			rawURL,
		)
	}

	return args
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

	qClean := strings.TrimSpace(quality)
	if qClean == "" {
		qClean = "best"
	}

	args := buildDownloadArgs(rawURL, qClean, outputDir, d.FfmpegPath)

	cmd := exec.CommandContext(ctx, d.YtdlpPath, args...)

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

	// Read stderr concurrently
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

	// Verify that expected output file actually exists on disk!
	if err := verifyOutputFileExists(outputDir, qClean); err != nil {
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
