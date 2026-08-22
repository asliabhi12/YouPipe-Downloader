// Package autostart registers the Helper to start when the user logs in.
//
// The product requirement is "download, install, done" — no terminal, ever. A
// drag-installed .app has no installer script to run, so the Helper registers
// itself on first launch instead. That keeps the whole mechanism inside the
// binary the user already ran and avoids shipping an installer framework.
//
// Both platforms use the standard per-user mechanism and need no administrator
// rights: a LaunchAgent on macOS, the per-user Run key on Windows. Nothing
// system-wide is touched, and no service or root daemon is installed.
package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Label identifies the LaunchAgent on macOS and the Run entry on Windows.
const Label = "com.youpiper.helper"

// runKeyName is the Windows registry value name; it is what the user sees in
// Task Manager's Startup tab, so it reads as a product name.
const runKeyName = "YouPiper Helper"

const runKeyPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

// Packaged is set to "true" via -ldflags by the packaging build only. Dev builds
// leave it false so that `go run ./cmd/agent` never registers a login item
// pointing into a throwaway build directory.
var Packaged = "false"

// IsPackaged reports whether this binary came from a packaging build.
func IsPackaged() bool { return Packaged == "true" }

// Supported reports whether autostart registration is implemented here.
func Supported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

// plistPath is the per-user LaunchAgent location on macOS.
func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

// LogPath is the Helper's canonical log file. On macOS launchd is told to send
// the process's output here; the Helper also writes here directly so that a
// double-clicked launch produces the same diagnostics as a login launch.
func LogPath() (string, error) {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "YouPiper", "helper.log"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "AppData", "Local", "YouPiper", "helper.log"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "YouPiper", "helper.log"), nil
}

// xmlEscape escapes a string for inclusion in plist character data. Application
// paths can legitimately contain & and other markup characters.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// plistContents renders the LaunchAgent.
//
// KeepAlive is deliberately SuccessfulExit=false rather than true: a crash
// should bring the Helper back, but a user who deliberately quits it must stay
// quit until next login. Plain KeepAlive=true would resurrect it immediately and
// make quitting impossible.
//
// ProcessType Background asks the system to treat this as a background task, so
// it is I/O-throttled and deprioritised against whatever the user is doing.
func plistContents(execPath, log string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + xmlEscape(Label) + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(execPath) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>LowPriorityIO</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(log) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(log) + `</string>
</dict>
</plist>
`
}

// Install registers execPath to run at login. Safe to call repeatedly.
func Install(execPath string) error {
	abs, err := filepath.Abs(execPath)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return installDarwin(abs)
	case "windows":
		return installWindows(abs)
	default:
		return fmt.Errorf("autostart not supported on %s", runtime.GOOS)
	}
}

func installDarwin(execPath string) error {
	plist, err := plistPath()
	if err != nil {
		return err
	}
	log, err := LogPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plist, []byte(plistContents(execPath, log)), 0o644); err != nil {
		return err
	}

	// Replace any previously loaded copy so a moved .app is picked up, then
	// load the new one. bootstrap is the modern verb; load -w is the fallback
	// for older systems. Both are best-effort: the plist on disk is what makes
	// the Helper start at next login, so a launchctl hiccup now is not fatal.
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+Label).Run()
	if err := exec.Command("launchctl", "bootstrap", domain, plist).Run(); err != nil {
		_ = exec.Command("launchctl", "load", "-w", plist).Run()
	}
	return nil
}

func installWindows(execPath string) error {
	// reg.exe rather than a registry library: the Helper has no third-party
	// dependencies and this keeps it that way.
	cmd := exec.Command("reg", "add", runKeyPath,
		"/v", runKeyName, "/t", "REG_SZ", "/d", execPath, "/f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reg add failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall removes the login registration. It does not touch the user's
// downloads, and it does not remove the application itself.
func Uninstall() error {
	switch runtime.GOOS {
	case "darwin":
		plist, err := plistPath()
		if err != nil {
			return err
		}
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = exec.Command("launchctl", "bootout", domain+"/"+Label).Run()
		_ = exec.Command("launchctl", "unload", "-w", plist).Run()
		if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case "windows":
		cmd := exec.Command("reg", "delete", runKeyPath, "/v", runKeyName, "/f")
		if out, err := cmd.CombinedOutput(); err != nil {
			// Absent value is success for an uninstall.
			if strings.Contains(strings.ToLower(string(out)), "unable to find") {
				return nil
			}
			return fmt.Errorf("reg delete failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	default:
		return fmt.Errorf("autostart not supported on %s", runtime.GOOS)
	}
}

// PointsAt reports whether autostart is currently registered and targets
// execPath. Used to make first-launch registration idempotent and to repair the
// entry after the user moves the application.
func PointsAt(execPath string) (bool, error) {
	abs, err := filepath.Abs(execPath)
	if err != nil {
		return false, err
	}
	switch runtime.GOOS {
	case "darwin":
		plist, err := plistPath()
		if err != nil {
			return false, err
		}
		data, err := os.ReadFile(plist)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		return strings.Contains(string(data), "<string>"+xmlEscape(abs)+"</string>"), nil
	case "windows":
		out, err := exec.Command("reg", "query", runKeyPath, "/v", runKeyName).CombinedOutput()
		if err != nil {
			return false, nil // not registered
		}
		return strings.Contains(string(out), abs), nil
	default:
		return false, nil
	}
}

// EnsureInstalled registers autostart only when it is missing or stale, so it
// can be called on every launch without doing redundant work.
func EnsureInstalled(execPath string) (changed bool, err error) {
	ok, err := PointsAt(execPath)
	if err != nil {
		return false, err
	}
	if ok {
		return false, nil
	}
	if err := Install(execPath); err != nil {
		return false, err
	}
	return true, nil
}
