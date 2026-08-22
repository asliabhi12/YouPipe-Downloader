package binpath

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeTool creates a runnable stub named after tool inside dir.
func writeTool(t *testing.T, dir, tool string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName(tool))
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBundleDirsForAppBundleLayout(t *testing.T) {
	dirs := bundleDirsFor("/Applications/YouPiper Helper.app/Contents/MacOS/youpiper-helper")

	want := []string{
		"/Applications/YouPiper Helper.app/Contents/Resources/bin",
		"/Applications/YouPiper Helper.app/Contents/MacOS/bin",
		"/Applications/YouPiper Helper.app/Contents/MacOS",
	}
	if len(dirs) != len(want) {
		t.Fatalf("got %d dirs %v, want %d", len(dirs), dirs, len(want))
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Errorf("dir[%d] = %q, want %q", i, dirs[i], want[i])
		}
	}
}

func TestBundleDirsResourcesTakesPriority(t *testing.T) {
	// The Resources/bin hop must be searched before the executable's own
	// directory, otherwise a stray binary beside the launcher would win.
	dirs := bundleDirsFor("/opt/app/Contents/MacOS/helper")
	if filepath.Base(filepath.Dir(dirs[0])) != "Resources" {
		t.Fatalf("first search dir %q is not under Resources", dirs[0])
	}
}

func TestBundleDirsDedupes(t *testing.T) {
	dirs := bundleDirsFor("/x/helper")
	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d] {
			t.Fatalf("duplicate search dir %q in %v", d, dirs)
		}
		seen[d] = true
	}
}

func TestFileNameMatchesPlatform(t *testing.T) {
	got := FileName(Ytdlp)
	if runtime.GOOS == "windows" {
		if got != "yt-dlp.exe" {
			t.Fatalf("FileName = %q, want yt-dlp.exe", got)
		}
		return
	}
	if got != "yt-dlp" {
		t.Fatalf("FileName = %q, want yt-dlp", got)
	}
}

func TestResolvePrefersBundleOverPath(t *testing.T) {
	bundle := t.TempDir()
	pathDir := t.TempDir()
	bundled := writeTool(t, bundle, Ytdlp)
	writeTool(t, pathDir, Ytdlp)

	t.Setenv("PATH", pathDir)
	t.Setenv(EnvVar(Ytdlp), "")

	if got := resolveIn(Ytdlp, []string{bundle}); got != bundled {
		t.Fatalf("resolveIn = %q, want bundled %q", got, bundled)
	}
}

func TestResolveFallsBackToPath(t *testing.T) {
	pathDir := t.TempDir()
	onPath := writeTool(t, pathDir, Ffmpeg)

	t.Setenv("PATH", pathDir)
	t.Setenv(EnvVar(Ffmpeg), "")

	// An empty bundle directory must not stop the PATH fallback; that fallback
	// is what keeps development builds working.
	got := resolveIn(Ffmpeg, []string{t.TempDir()})
	if got != onPath {
		t.Fatalf("resolveIn = %q, want %q from PATH", got, onPath)
	}
}

func TestResolveEnvOverrideWinsOverBundle(t *testing.T) {
	bundle := t.TempDir()
	writeTool(t, bundle, Ffprobe)
	override := writeTool(t, t.TempDir(), Ffprobe)

	t.Setenv("PATH", "")
	t.Setenv(EnvVar(Ffprobe), override)

	if got := resolveIn(Ffprobe, []string{bundle}); got != override {
		t.Fatalf("resolveIn = %q, want override %q", got, override)
	}
}

func TestResolveIgnoresBrokenEnvOverride(t *testing.T) {
	bundle := t.TempDir()
	bundled := writeTool(t, bundle, Ytdlp)

	t.Setenv("PATH", "")
	t.Setenv(EnvVar(Ytdlp), filepath.Join(t.TempDir(), "does-not-exist"))

	// A stale override must degrade to the bundled copy rather than leaving the
	// Helper with no downloader at all.
	if got := resolveIn(Ytdlp, []string{bundle}); got != bundled {
		t.Fatalf("resolveIn = %q, want bundled %q", got, bundled)
	}
}

func TestResolveReturnsEmptyWhenMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv(EnvVar(Ytdlp), "")

	if got := resolveIn(Ytdlp, []string{t.TempDir()}); got != "" {
		t.Fatalf("resolveIn = %q, want empty", got)
	}
}

func TestExecutableRejectsDirsAndNonExecutables(t *testing.T) {
	dir := t.TempDir()
	if executable(dir) {
		t.Error("a directory must not be reported as executable")
	}
	if executable(filepath.Join(dir, "absent")) {
		t.Error("a missing file must not be reported as executable")
	}

	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Windows has no execute bit, so mode-based rejection only applies elsewhere.
	if runtime.GOOS != "windows" && executable(plain) {
		t.Error("a non-executable file must not be reported as executable")
	}
}

func TestSourceReporting(t *testing.T) {
	bundle := t.TempDir()
	bundled := filepath.Join(bundle, FileName(Ytdlp))

	t.Setenv(EnvVar(Ytdlp), "")
	if got := sourceIn(Ytdlp, bundled, []string{bundle}); got != "bundled" {
		t.Errorf("bundled binary reported as %q", got)
	}
	if got := sourceIn(Ytdlp, "/usr/local/bin/yt-dlp", []string{bundle}); got != "system PATH" {
		t.Errorf("PATH binary reported as %q", got)
	}
	if got := sourceIn(Ytdlp, "", []string{bundle}); got != "not found" {
		t.Errorf("missing binary reported as %q", got)
	}

	t.Setenv(EnvVar(Ytdlp), "/custom/yt-dlp")
	if got := sourceIn(Ytdlp, "/custom/yt-dlp", []string{bundle}); got != "env override (YOUPIPER_YTDLP)" {
		t.Errorf("override reported as %q", got)
	}
}

func TestEnvVarKnownToolsOnly(t *testing.T) {
	for _, tool := range []string{Ytdlp, Ffmpeg, Ffprobe} {
		if EnvVar(tool) == "" {
			t.Errorf("no env var defined for %q", tool)
		}
	}
	if EnvVar("curl") != "" {
		t.Error("unknown tools must not get an env override")
	}
}

// The Helper resolves three tools and all three must be findable in a bundle;
// a packaged build that only ships yt-dlp cannot merge or transcode.
func TestAllRequiredToolsResolveFromBundle(t *testing.T) {
	bundle := t.TempDir()
	for _, tool := range []string{Ytdlp, Ffmpeg, Ffprobe} {
		writeTool(t, bundle, tool)
	}
	t.Setenv("PATH", "")
	for _, tool := range []string{Ytdlp, Ffmpeg, Ffprobe} {
		t.Setenv(EnvVar(tool), "")
		if got := resolveIn(tool, []string{bundle}); got == "" {
			t.Errorf("%s not resolved from bundle", tool)
		}
	}
}
