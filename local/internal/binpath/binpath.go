// Package binpath locates the external binaries the Helper drives: yt-dlp,
// ffmpeg and ffprobe.
//
// A packaged Helper ships its own copies so the user never has to install
// anything, which means bundled binaries must win over whatever happens to be
// on PATH. A stale yt-dlp left over from some other install should not silently
// shadow the version we shipped and tested against. PATH is only a fallback, so
// that a development build still works with tools installed by hand.
package binpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Tool names, as they appear on disk (without any platform extension).
const (
	Ytdlp   = "yt-dlp"
	Ffmpeg  = "ffmpeg"
	Ffprobe = "ffprobe"
)

// EnvVar returns the environment variable that force-selects a tool's path,
// bypassing bundle and PATH lookup. Intended for testing and for operators
// who deliberately want to point at their own build.
func EnvVar(tool string) string {
	switch tool {
	case Ytdlp:
		return "YOUPIPER_YTDLP"
	case Ffmpeg:
		return "YOUPIPER_FFMPEG"
	case Ffprobe:
		return "YOUPIPER_FFPROBE"
	}
	return ""
}

// FileName returns the on-disk name of a tool for the current platform.
func FileName(tool string) string {
	if runtime.GOOS == "windows" {
		return tool + ".exe"
	}
	return tool
}

// BundleDirs returns the directories a packaged Helper may keep its bundled
// binaries in, most specific first. Derived from the running executable, so it
// is correct regardless of where the user dragged the app.
func BundleDirs() []string {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	// Follow symlinks: /Applications may hold a link, and we want the real
	// bundle so the relative Resources hop below lands in the right place.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return bundleDirsFor(exe)
}

// bundleDirsFor is the pure part of BundleDirs, split out so the layout rules
// can be tested without a real packaged executable on disk.
func bundleDirsFor(exe string) []string {
	dir := filepath.Dir(exe)
	return dedupe([]string{
		// macOS .app: Contents/MacOS/<exe> -> Contents/Resources/bin
		filepath.Join(dir, "..", "Resources", "bin"),
		// Windows folder layout, and the build's staging layout
		filepath.Join(dir, "bin"),
		// Binaries sitting directly beside the executable
		dir,
	})
}

func dedupe(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		clean := filepath.Clean(p)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out
}

// executable reports whether path is a regular file we are allowed to run.
func executable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		// Windows has no execute bit; presence of the file is the test.
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// Resolve returns an absolute path to the requested tool, or "" when it cannot
// be found anywhere. Order: explicit env override, then bundled directories,
// then PATH.
func Resolve(tool string) string {
	return resolveIn(tool, BundleDirs())
}

func resolveIn(tool string, dirs []string) string {
	if env := EnvVar(tool); env != "" {
		if override := os.Getenv(env); override != "" && executable(override) {
			return override
		}
	}

	name := FileName(tool)
	for _, dir := range dirs {
		candidate := filepath.Join(dir, name)
		if executable(candidate) {
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs
			}
			return candidate
		}
	}

	if path, err := exec.LookPath(tool); err == nil {
		return path
	}
	return ""
}

// Source describes where a resolved tool came from, for startup diagnostics.
// Knowing whether the running Helper used its bundled yt-dlp or one from PATH
// is the first question worth answering when a packaged build misbehaves.
func Source(tool, resolved string) string {
	return sourceIn(tool, resolved, BundleDirs())
}

func sourceIn(tool, resolved string, dirs []string) string {
	if resolved == "" {
		return "not found"
	}
	if env := EnvVar(tool); env != "" && os.Getenv(env) == resolved {
		return "env override (" + env + ")"
	}
	dir := filepath.Dir(resolved)
	for _, bundle := range dirs {
		if dir == bundle {
			return "bundled"
		}
	}
	return "system PATH"
}
