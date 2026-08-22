// Command agent runs the YouPiper Helper: a small background process that
// exposes a loopback-only HTTP API so the website can download directly to this
// computer.
//
// It is normally started by the system at login and never interacted with
// directly. The flags exist for diagnostics and for scripted uninstall.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ytd-local/internal/autostart"
	"ytd-local/internal/binpath"
	"ytd-local/internal/downloader"
	"ytd-local/internal/jobs"
	"ytd-local/internal/server"
)

// maxLogBytes caps the log file. The Helper may run every day for years, and an
// unbounded log would be the only thing about it that grows.
const maxLogBytes = 1 << 20 // 1 MiB

func main() {
	addrFlag := flag.String("addr", server.DefaultAddr, "HTTP server address (must bind to 127.0.0.1)")
	outputFlag := flag.String("output", "", "Download output directory (defaults to ~/Downloads/YTD Local)")
	installFlag := flag.Bool("install-startup", false, "Register the Helper to start at login, then exit")
	uninstallFlag := flag.Bool("uninstall", false, "Remove the login registration, then exit (downloads are never touched)")
	noStartupFlag := flag.Bool("no-startup", false, "Do not register at login on this launch")
	statusFlag := flag.Bool("status", false, "Print startup registration and tool locations, then exit")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("YouPiper Helper %s\n", server.ServerVersion)
		return
	}

	exe := selfPath()

	switch {
	case *uninstallFlag:
		if err := autostart.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "Could not remove the login item: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("YouPiper Helper will no longer start automatically.")
		return

	case *installFlag:
		if err := autostart.Install(exe); err != nil {
			fmt.Fprintf(os.Stderr, "Could not register at login: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("YouPiper Helper will start automatically at login.")
		return

	case *statusFlag:
		printStatus(exe)
		return
	}

	setupLogging()
	log.Printf("Starting YouPiper Helper (v%s)...", server.ServerVersion)

	dl := downloader.NewYTDLPDownloader()
	ytdlp, ffmpeg := dl.CheckDependencies()
	logToolLocations(dl)
	if !ytdlp {
		log.Println("WARNING: yt-dlp binary not found (bundled copy missing and none on PATH). Downloads will fail.")
	}
	if !ffmpeg {
		log.Println("WARNING: ffmpeg binary not found (bundled copy missing and none on PATH). Merging and MP3 output will fail.")
	}

	// A packaged Helper registers itself the first time it runs. That is what
	// makes install-and-forget work without an installer script or a terminal.
	// Development builds skip this so they never leave a login item pointing at
	// a throwaway build directory.
	if autostart.IsPackaged() && !*noStartupFlag {
		if changed, err := autostart.EnsureInstalled(exe); err != nil {
			log.Printf("Could not register at login: %v", err)
		} else if changed {
			log.Printf("Registered to start at login: %s", exe)
		}
	}

	jm := jobs.NewJobManager()
	srv := server.NewServer(*addrFlag, dl, jm, *outputFlag)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Start() }()

	select {
	case <-stop:
		log.Println("Received shutdown signal. Gracefully stopping server...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
		log.Println("YouPiper Helper stopped.")

	case err := <-srvErr:
		// The server stopped on its own, so there is nothing left to serve and
		// staying resident would waste memory for no benefit.
		switch {
		case err == nil || errors.Is(err, http.ErrServerClosed):
			log.Println("YouPiper Helper stopped.")
		case errors.Is(err, syscall.EADDRINUSE):
			// Expected whenever the user opens the app while the login-started
			// copy is already running. Exit successfully so the system does not
			// treat it as a crash and relaunch in a loop.
			log.Printf("YouPiper Helper is already running on %s; this copy will exit.", *addrFlag)
		default:
			log.Printf("Server stopped: %v", err)
			os.Exit(1)
		}
	}
}

// selfPath returns the absolute, symlink-resolved path of this executable. It is
// what gets recorded as the login item, so it has to survive the user moving the
// application.
func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe
}

// setupLogging sends output to a single log file for packaged builds so that a
// login-started run and a double-clicked run leave diagnostics in the same
// place. Development builds keep logging to the terminal.
func setupLogging() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	if !autostart.IsPackaged() {
		return
	}

	path, err := autostart.LogPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogBytes {
		_ = os.Truncate(path, 0)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
}

// logToolLocations records which copy of each tool this process will drive.
// When a packaged build misbehaves, "did it use the bundled one?" is the first
// question worth answering.
func logToolLocations(dl *downloader.YTDLPDownloader) {
	for _, t := range []struct{ tool, path string }{
		{binpath.Ytdlp, dl.YtdlpPath},
		{binpath.Ffmpeg, dl.FfmpegPath},
		{binpath.Ffprobe, dl.FfprobePath},
	} {
		if t.path == "" {
			log.Printf("%s: not found", t.tool)
			continue
		}
		log.Printf("%s: %s (%s)", t.tool, t.path, binpath.Source(t.tool, t.path))
	}
}

func printStatus(exe string) {
	fmt.Printf("YouPiper Helper %s\n", server.ServerVersion)
	fmt.Printf("Executable:  %s\n", exe)
	fmt.Printf("Packaged:    %t\n", autostart.IsPackaged())

	if autostart.Supported() {
		registered, err := autostart.PointsAt(exe)
		switch {
		case err != nil:
			fmt.Printf("Start at login: unknown (%v)\n", err)
		case registered:
			fmt.Println("Start at login: yes")
		default:
			fmt.Println("Start at login: no")
		}
	} else {
		fmt.Println("Start at login: unsupported on this platform")
	}

	if path, err := autostart.LogPath(); err == nil {
		fmt.Printf("Log file:    %s\n", path)
	}
	if dir, err := downloader.GetDefaultDownloadsDir(); err == nil {
		fmt.Printf("Downloads:   %s\n", dir)
	}
	fmt.Printf("API address: %s\n", server.DefaultAddr)

	fmt.Println()
	dl := downloader.NewYTDLPDownloader()
	dl.CheckDependencies()
	writeToolStatus(os.Stdout, dl)
}

func writeToolStatus(w io.Writer, dl *downloader.YTDLPDownloader) {
	for _, t := range []struct{ tool, path string }{
		{binpath.Ytdlp, dl.YtdlpPath},
		{binpath.Ffmpeg, dl.FfmpegPath},
		{binpath.Ffprobe, dl.FfprobePath},
	} {
		if t.path == "" {
			fmt.Fprintf(w, "%-8s NOT FOUND\n", t.tool+":")
			continue
		}
		fmt.Fprintf(w, "%-8s %s (%s)\n", t.tool+":", t.path, binpath.Source(t.tool, t.path))
	}
}
