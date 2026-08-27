package downloader

import (
	"context"
	"errors"
	"fmt"
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
	args := buildDownloadArgs("https://youtu.be/x", "720p", "/out", ffmpeg, "/bin/deno")

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
	args := buildDownloadArgs("https://youtu.be/x", "720p", "/out", "", "/bin/deno")
	if hasFlag(args, "--ffmpeg-location") {
		t.Fatal("--ffmpeg-location must be omitted when no FFmpeg path is known")
	}
}

func TestBuildDownloadArgsAlwaysSetsExtractorClient(t *testing.T) {
	for _, q := range []string{"audio", "360p", "480p", "720p", "1080p", "best"} {
		args := buildDownloadArgs("https://youtu.be/x", q, "/out", "/bin/ffmpeg", "/bin/deno")
		if got := flagValue(args, "--extractor-args"); got != "youtube:player_client=web_embedded" {
			t.Errorf("quality %q: --extractor-args = %q", q, got)
		}
	}
}

// Output contract: video is a real MP4 container, audio is a real MP3 —
// never a renamed source file.
func TestBuildDownloadArgsContainerContract(t *testing.T) {
	video := buildDownloadArgs("https://youtu.be/x", "1080p", "/out", "/bin/ffmpeg", "/bin/deno")
	if got := flagValue(video, "--merge-output-format"); got != "mp4" {
		t.Errorf("video --merge-output-format = %q, want mp4", got)
	}
	if hasFlag(video, "-x") {
		t.Error("video download must not request audio extraction")
	}

	audio := buildDownloadArgs("https://youtu.be/x", "audio", "/out", "/bin/ffmpeg", "/bin/deno")
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
	args := buildDownloadArgs(url, "720p", "/out", "/bin/ffmpeg", "/bin/deno")
	if args[len(args)-1] != url {
		t.Fatalf("last arg = %q, want the URL; a flag after it would be parsed as another input", args[len(args)-1])
	}
}

func TestFfmpegLocationEmptyForEmptyPath(t *testing.T) {
	if got := ffmpegLocation(""); got != "" {
		t.Fatalf("ffmpegLocation(\"\") = %q, want empty", got)
	}
}

// --- JavaScript runtime -----------------------------------------------------
//
// yt-dlp needs a JavaScript runtime to solve YouTube's challenge scripts. It
// looks for one on PATH by default, and launchd gives the Helper a PATH with no
// runtime on it, so the location has to be stated explicitly on every call.

func TestJSRuntimeArgsNamesTheRuntimeAndPath(t *testing.T) {
	args := jsRuntimeArgs("/opt/YouPiper Helper.app/Contents/Resources/bin/deno")
	if len(args) != 2 || args[0] != "--js-runtimes" {
		t.Fatalf("jsRuntimeArgs = %q, want --js-runtimes and one value", args)
	}
	// runtime:path is yt-dlp's syntax. Passing the bare path would be read as a
	// runtime name and silently ignored.
	if want := "deno:/opt/YouPiper Helper.app/Contents/Resources/bin/deno"; args[1] != want {
		t.Fatalf("jsRuntimeArgs value = %q, want %q", args[1], want)
	}
}

func TestJSRuntimeArgsEmptyWhenNoRuntime(t *testing.T) {
	// An empty location must mean "say nothing", not "--js-runtimes deno:".
	if got := jsRuntimeArgs(""); len(got) != 0 {
		t.Fatalf("jsRuntimeArgs(\"\") = %q, want nothing", got)
	}
}

// Every download path needs the runtime, audio included: the challenge has to be
// solved before any format is offered, whatever is done with it afterwards.
func TestBuildDownloadArgsPassesBundledJSRuntime(t *testing.T) {
	const deno = "/apps/Helper.app/Contents/Resources/bin/deno"
	for _, q := range []string{"audio", "360p", "480p", "720p", "1080p", "best"} {
		args := buildDownloadArgs("https://youtu.be/x", q, "/out", "/bin/ffmpeg", deno)
		if got := flagValue(args, "--js-runtimes"); got != "deno:"+deno {
			t.Errorf("quality %q: --js-runtimes = %q, want %q", q, got, "deno:"+deno)
		}
	}
}

func TestBuildDownloadArgsOmitsJSRuntimeWhenUnknown(t *testing.T) {
	args := buildDownloadArgs("https://youtu.be/x", "720p", "/out", "/bin/ffmpeg", "")
	if hasFlag(args, "--js-runtimes") {
		t.Fatal("--js-runtimes must be omitted when no runtime was found, so yt-dlp's own lookup still applies")
	}
}

// starveRuntimeLookup removes every way of finding a runtime, reproducing a
// packaged Helper that shipped without one.
func starveRuntimeLookup(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("YOUPIPER_DENO", "")
}

// REG-JS-005: a missing runtime has to be visible as a missing dependency, not
// discovered later as an unexplained yt-dlp exit status.
func TestCheckDependenciesReportsMissingJSRuntime(t *testing.T) {
	starveRuntimeLookup(t)
	d := &YTDLPDownloader{YtdlpPath: "/bin/sh", FfmpegPath: "/bin/sh", FfprobePath: "/bin/sh"}

	deps := d.CheckDependencies()
	if deps.JSRuntime {
		t.Fatalf("JSRuntime reported available with nothing to find (DenoPath=%q)", d.DenoPath)
	}
	if !deps.Ytdlp || !deps.Ffmpeg {
		t.Fatal("yt-dlp and ffmpeg must still be reported present")
	}
	if deps.Ready() {
		t.Fatal("Ready() must be false without a JavaScript runtime: no download can complete")
	}
}

func TestCheckDependenciesReadyWithAllTools(t *testing.T) {
	d := &YTDLPDownloader{
		YtdlpPath:   "/bin/sh",
		FfmpegPath:  "/bin/sh",
		FfprobePath: "/bin/sh",
		DenoPath:    "/bin/sh",
	}
	if !d.CheckDependencies().Ready() {
		t.Fatal("Ready() must be true when every tool is present")
	}
}

// Fail with the real reason instead of running yt-dlp and reporting whatever
// exit status came back. Without a runtime YouTube offers no formats at all, so
// there is nothing to attempt.
func TestMetadataFailsFastWithoutJSRuntime(t *testing.T) {
	starveRuntimeLookup(t)
	d := &YTDLPDownloader{YtdlpPath: "/bin/sh", FfmpegPath: "/bin/sh", FfprobePath: "/bin/sh"}

	_, err := d.Metadata(context.Background(), "https://www.youtube.com/watch?v=x")
	if !errors.Is(err, ErrJSRuntimeMissing) {
		t.Fatalf("Metadata error = %v, want ErrJSRuntimeMissing", err)
	}
}

func TestDownloadFailsFastWithoutJSRuntime(t *testing.T) {
	starveRuntimeLookup(t)
	d := &YTDLPDownloader{YtdlpPath: "/bin/sh", FfmpegPath: "/bin/sh", FfprobePath: "/bin/sh"}

	err := d.Download(context.Background(), "https://www.youtube.com/watch?v=x", "480p", t.TempDir(), nil)
	if !errors.Is(err, ErrJSRuntimeMissing) {
		t.Fatalf("Download error = %v, want ErrJSRuntimeMissing", err)
	}
}

// The runtime flag must not disturb the flags the download contract already
// depends on.
func TestJSRuntimeFlagDoesNotDisplaceURLOrContainer(t *testing.T) {
	const url = "https://youtu.be/x"
	args := buildDownloadArgs(url, "720p", "/out", "/bin/ffmpeg", "/bin/deno")
	if args[len(args)-1] != url {
		t.Fatalf("last arg = %q, want the URL", args[len(args)-1])
	}
	if got := flagValue(args, "--merge-output-format"); got != "mp4" {
		t.Fatalf("--merge-output-format = %q, want mp4", got)
	}
	if got := flagValue(args, "--ffmpeg-location"); got != "/bin" {
		t.Fatalf("--ffmpeg-location = %q, want /bin", got)
	}
}

func TestMetadataErrorWrappingSentinelDetection(t *testing.T) {
	// Verify that when errors are wrapped with ErrMetadataFailed,
	// errors.Is successfully detects both ErrMetadataFailed and the underlying sentinel.
	sentinel := errors.New("underlying_sentinel_error")
	wrapped := fmt.Errorf("%w: %w", ErrMetadataFailed, sentinel)

	if !errors.Is(wrapped, ErrMetadataFailed) {
		t.Errorf("errors.Is(wrapped, ErrMetadataFailed) = false, want true")
	}
	if !errors.Is(wrapped, sentinel) {
		t.Errorf("errors.Is(wrapped, sentinel) = false, want true")
	}

	wrappedJS := fmt.Errorf("%w: %w", ErrMetadataFailed, ErrJSRuntimeMissing)
	if !errors.Is(wrappedJS, ErrMetadataFailed) {
		t.Errorf("errors.Is(wrappedJS, ErrMetadataFailed) = false, want true")
	}
	if !errors.Is(wrappedJS, ErrJSRuntimeMissing) {
		t.Errorf("errors.Is(wrappedJS, ErrJSRuntimeMissing) = false, want true")
	}
}
