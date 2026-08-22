package autostart

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The write format and the read format must agree, or EnsureInstalled would
// rewrite the LaunchAgent on every single launch and needlessly bounce launchctl.
func TestPointsAtRecognisesOwnPlist(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("LaunchAgent plists are macOS-only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	exe := filepath.Join(home, "Applications", "YouPiper Helper.app", "Contents", "MacOS", "youpiper-helper")
	plist, err := plistPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, []byte(plistContents(exe, filepath.Join(home, "log"))), 0o644); err != nil {
		t.Fatal(err)
	}

	ok, err := PointsAt(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("PointsAt did not recognise a plist written by plistContents")
	}

	// A moved application must read as stale so the entry gets repaired.
	moved := filepath.Join(home, "Desktop", "YouPiper Helper.app", "Contents", "MacOS", "youpiper-helper")
	ok, err = PointsAt(moved)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("PointsAt matched a different executable path")
	}
}

func TestPointsAtNoPlistIsNotAnError(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("LaunchAgent plists are macOS-only")
	}
	t.Setenv("HOME", t.TempDir())

	ok, err := PointsAt("/somewhere/helper")
	if err != nil {
		t.Fatalf("unregistered state must not be an error, got %v", err)
	}
	if ok {
		t.Error("PointsAt reported registered with no plist on disk")
	}
}

func TestPlistIsWellFormedXML(t *testing.T) {
	body := plistContents("/Applications/YouPiper Helper.app/Contents/MacOS/helper", "/tmp/helper.log")
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("generated plist is not well-formed XML: %v", err)
		}
	}
}

// Paths under the user's control can contain markup characters; an unescaped
// ampersand would produce a plist launchd refuses to parse, silently disabling
// autostart.
func TestPlistEscapesPathMetacharacters(t *testing.T) {
	body := plistContents(`/Users/a&b/<app>/helper`, "/tmp/l")
	if strings.Contains(body, "/Users/a&b/") {
		t.Error("ampersand was not escaped")
	}
	if strings.Contains(body, "<app>") {
		t.Error("angle brackets were not escaped")
	}

	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("plist with metacharacters is not well-formed: %v", err)
		}
	}
}

func TestPlistCarriesRequiredKeys(t *testing.T) {
	body := plistContents("/x/helper", "/tmp/l")

	for _, want := range []string{
		"<key>Label</key>",
		"<string>" + Label + "</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<key>ProcessType</key>",
		"<string>Background</string>",
		"<key>StandardOutPath</key>",
		"<key>StandardErrorPath</key>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plist is missing %s", want)
		}
	}
}

// KeepAlive must be conditional. Plain KeepAlive=true would relaunch the Helper
// the instant a user quits it, making it impossible to turn off until reboot.
func TestKeepAliveIsConditionalOnUnsuccessfulExit(t *testing.T) {
	body := plistContents("/x/helper", "/tmp/l")

	if !strings.Contains(body, "<key>SuccessfulExit</key>") {
		t.Fatal("KeepAlive is not conditioned on SuccessfulExit")
	}
	idx := strings.Index(body, "<key>SuccessfulExit</key>")
	rest := body[idx:]
	if !strings.HasPrefix(strings.TrimSpace(rest[len("<key>SuccessfulExit</key>"):]), "<false/>") {
		t.Error("SuccessfulExit must be false so a clean quit stays quit")
	}
}

func TestPlistUsesAbsoluteProgramPath(t *testing.T) {
	// launchd will not resolve a relative path; it has no meaningful working
	// directory at login.
	body := plistContents("/Applications/X.app/Contents/MacOS/helper", "/tmp/l")
	if !strings.Contains(body, "<string>/Applications/X.app/Contents/MacOS/helper</string>") {
		t.Fatal("program path missing from ProgramArguments")
	}
}

func TestAgentPathsLiveUnderUserLibrary(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS paths")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	plist, err := plistPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Library", "LaunchAgents", Label+".plist"); plist != want {
		t.Errorf("plistPath = %q, want %q", plist, want)
	}
	// Nothing may be written outside the user's home: no admin rights, no
	// system-wide daemon.
	if !strings.HasPrefix(plist, home) {
		t.Errorf("plist path %q escapes the user home", plist)
	}

	log, err := LogPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Library", "Logs", "YouPiper", "helper.log"); log != want {
		t.Errorf("logPath = %q, want %q", log, want)
	}
}

func TestWindowsRunKeyIsPerUser(t *testing.T) {
	// HKCU, never HKLM: a machine-wide Run key would require administrator
	// rights and would start the Helper for other accounts.
	if !strings.HasPrefix(runKeyPath, `HKCU\`) {
		t.Fatalf("run key %q is not per-user", runKeyPath)
	}
	if strings.Contains(runKeyPath, "HKLM") {
		t.Fatal("run key must not be machine-wide")
	}
}

// Product name, not a binary name: this string is what the user sees in Task
// Manager's Startup tab.
func TestRunKeyNameIsUserFacing(t *testing.T) {
	if runKeyName != "YouPiper Helper" {
		t.Fatalf("runKeyName = %q", runKeyName)
	}
}

func TestIsPackagedDefaultsFalse(t *testing.T) {
	// Dev builds must never register a login item pointing into a temporary
	// build directory; only the packaging build sets this.
	if IsPackaged() {
		t.Fatal("Packaged must default to false for non-packaging builds")
	}
}

func TestSupportedPlatforms(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if !Supported() {
			t.Fatalf("autostart should be supported on %s", runtime.GOOS)
		}
		return
	}
	if Supported() {
		t.Fatalf("autostart should not claim support on %s", runtime.GOOS)
	}
}

func TestInstallRejectsUnsupportedPlatform(t *testing.T) {
	if Supported() {
		t.Skip("current platform is supported")
	}
	if err := Install("/x/helper"); err == nil {
		t.Fatal("Install should fail on an unsupported platform")
	}
	if err := Uninstall(); err == nil {
		t.Fatal("Uninstall should fail on an unsupported platform")
	}
}

func TestXMLEscape(t *testing.T) {
	got := xmlEscape(`a&b<c>d"e'f`)
	want := `a&amp;b&lt;c&gt;d&quot;e&apos;f`
	if got != want {
		t.Fatalf("xmlEscape = %q, want %q", got, want)
	}
}
