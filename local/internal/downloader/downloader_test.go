package downloader

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", false},
		{"http://youtu.be/dQw4w9WgXcQ", false},
		{"https://vimeo.com/123456", false},
		{"ftp://example.com/video.mp4", true},
		{"invalid-url", true},
		{"", true},
		{"https://", true},
	}

	for _, tt := range tests {
		err := ValidateURL(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateURL(%q) err = %v, wantErr = %v", tt.url, err, tt.wantErr)
		}
	}
}

func TestMapQualityToFormatFlag(t *testing.T) {
	tests := []struct {
		quality string
		want    string
	}{
		{"best", "bestvideo+bestaudio/best"},
		{"1080p", "bestvideo[height<=1080]+bestaudio/best[height<=1080]/best"},
		{"720p", "bestvideo[height<=720]+bestaudio/best[height<=720]/best"},
		{"480p", "bestvideo[height<=480]+bestaudio/best[height<=480]/best"},
		{"360p", "bestvideo[height<=360]+bestaudio/best[height<=360]/best"},
		{"audio", "bestaudio/best"},
		{"unknown", "bestvideo+bestaudio/best"},
	}

	for _, tt := range tests {
		got := mapQualityToFormatFlag(tt.quality)
		if got != tt.want {
			t.Errorf("mapQualityToFormatFlag(%q) = %q, want %q", tt.quality, got, tt.want)
		}
	}
}

func TestGetDefaultDownloadsDir(t *testing.T) {
	dir, err := GetDefaultDownloadsDir()
	if err != nil {
		t.Fatalf("GetDefaultDownloadsDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, "Downloads/YTD Local") && !strings.HasSuffix(dir, "Downloads\\YTD Local") {
		t.Errorf("Unexpected default downloads directory: %s", dir)
	}
}

// flagValue returns the argument following flag, or "" if the flag is absent.
func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// A packaged Helper ships its own FFmpeg. If the location is not handed to
// yt-dlp, yt-dlp searches PATH instead and merging fails on a machine where the
// user has installed nothing — which is every machine we target.
func TestBuildDownloadArgsPassesBundledFfmpegLocation(t *testing.T) {
	ffmpeg := filepath.Join("/Apps", "YouPiper Helper.app", "Contents", "Resources", "bin", "ffmpeg")
	args := buildDownloadArgs("https://youtu.be/x", "720p", "/out", ffmpeg)

	got := flagValue(args, "--ffmpeg-location")
	if want := filepath.Dir(ffmpeg); got != want {
		t.Fatalf("--ffmpeg-location = %q, want %q", got, want)
	}
	// The directory, not the binary: yt-dlp needs to find ffprobe beside it.
	if got == ffmpeg {
		t.Error("--ffmpeg-location points at the binary; ffprobe would not be found")
	}
}

func TestBuildDownloadArgsOmitsFfmpegLocationWhenUnknown(t *testing.T) {
	args := buildDownloadArgs("https://youtu.be/x", "720p", "/out", "")
	if hasFlag(args, "--ffmpeg-location") {
		t.Fatal("--ffmpeg-location must be omitted when no FFmpeg path is known")
	}
}

func TestBuildDownloadArgsAlwaysSetsExtractorClient(t *testing.T) {
	for _, q := range []string{"audio", "360p", "480p", "720p", "1080p", "best"} {
		args := buildDownloadArgs("https://youtu.be/x", q, "/out", "/bin/ffmpeg")
		if got := flagValue(args, "--extractor-args"); got != "youtube:player_client=web_embedded" {
			t.Errorf("quality %q: --extractor-args = %q", q, got)
		}
	}
}

// Output contract: video is a real MP4 container, audio is a real MP3 —
// never a renamed source file.
func TestBuildDownloadArgsContainerContract(t *testing.T) {
	video := buildDownloadArgs("https://youtu.be/x", "1080p", "/out", "/bin/ffmpeg")
	if got := flagValue(video, "--merge-output-format"); got != "mp4" {
		t.Errorf("video --merge-output-format = %q, want mp4", got)
	}
	if hasFlag(video, "-x") {
		t.Error("video download must not request audio extraction")
	}

	audio := buildDownloadArgs("https://youtu.be/x", "audio", "/out", "/bin/ffmpeg")
	if !hasFlag(audio, "-x") {
		t.Error("audio download must request extraction")
	}
	if got := flagValue(audio, "--audio-format"); got != "mp3" {
		t.Errorf("audio --audio-format = %q, want mp3", got)
	}
	if hasFlag(audio, "--merge-output-format") {
		t.Error("audio download must not set a video merge container")
	}
}

func TestBuildDownloadArgsURLIsLast(t *testing.T) {
	const url = "https://youtu.be/x"
	args := buildDownloadArgs(url, "720p", "/out", "/bin/ffmpeg")
	if args[len(args)-1] != url {
		t.Fatalf("last arg = %q, want the URL; a flag after it would be parsed as another input", args[len(args)-1])
	}
}

func TestFfmpegLocationEmptyForEmptyPath(t *testing.T) {
	if got := ffmpegLocation(""); got != "" {
		t.Fatalf("ffmpegLocation(\"\") = %q, want empty", got)
	}
}
