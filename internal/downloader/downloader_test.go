package downloader

import (
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
		{"1080p", "bestvideo[height<=1080]+bestaudio/best[height<=1080]"},
		{"720p", "bestvideo[height<=720]+bestaudio/best[height<=720]"},
		{"480p", "bestvideo[height<=480]+bestaudio/best[height<=480]"},
		{"360p", "bestvideo[height<=360]+bestaudio/best[height<=360]"},
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
