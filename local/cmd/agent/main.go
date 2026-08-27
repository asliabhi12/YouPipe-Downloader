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
	"runtime"
	"strings"
	"syscall"
	"time"

	"ytd-local/internal/autostart"
	"ytd-local/internal/binpath"
	"ytd-local/internal/downloader"
	"ytd-local/internal/jobs"
	"ytd-local/internal/server"
	"ytd-local/internal/tray"
)

// maxLogBytes caps the log file. The Helper may run every day for years, and an
// unbounded log would be the only thing about it that grows.
const maxLogBytes = 1 << 20 // 1 MiB

func main() {
	runtime.LockOSThread()
	addrFlag := flag.String("addr", server.DefaultAddr, "HTTP server address (must bind to 127.0.0.1)")
	outputFlag := flag.String("output", "", "Download output directory (defaults to ~/Downloads)")
	installFlag := flag.Bool("install-startup", false, "Register the Helper to start at login, then exit")
	uninstallFlag := flag.Bool("uninstall", false, "Remove the login registration, then exit (downloads are never touched)")
	onFlag := flag.Bool("on", false, "Enable the Helper and register to start at login")
	offFlag := flag.Bool("off", false, "Disable the Helper, remove login registration, and exit")
	noStartupFlag := flag.Bool("no-startup", false, "Do not register at login on this launch")
	statusFlag := flag.Bool("status", false, "Print startup registration and tool locations, then exit")
	versionFlag := flag.Bool("version", false, "Print version and exit")

	var cleanArgs []string
	for _, arg := range os.Args {
		if !strings.HasPrefix(arg, "-psn") {
			cleanArgs = append(cleanArgs, arg)
		}
	}
	os.Args = cleanArgs

	flag.Parse()

	if *versionFlag {
		fmt.Printf("YouPiper Helper %s\n", server.ServerVersion)
		return
	}

	exe := selfPath()

	switch {
	case *uninstallFlag || *offFlag:
		if err := autostart.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "Could not remove the login item: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("YouPiper Helper will no longer start automatically.")
		return

	case *installFlag || *onFlag:
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
	deps := dl.CheckDependencies()
	logToolLocations(dl)
	if !deps.Ytdlp {
		log.Println("WARNING: yt-dlp binary not found (bundled copy missing and none on PATH). Downloads will fail.")
	}
	if !deps.Ffmpeg {
		log.Println("WARNING: ffmpeg binary not found (bundled copy missing and none on PATH). Merging and MP3 output will fail.")
	}
	if !deps.JSRuntime {
		log.Println("WARNING: no JavaScript runtime found (bundled deno missing and none on PATH). YouTube will report no")
		log.Println("         available formats, so every download will fail. /health reports this as degraded.")
	}

	// A packaged Helper registers itself the first time it runs. That is what
	// makes install-and-forget work without an installer script or a terminal.
	// Development builds skip this so they never leave a login item pointing at
	// a throwaway build directory.
	if autostart.IsPackaged() && !*noStartupFlag {
		if stable, why := autostart.StableLocation(exe); !stable {
			log.Printf("Not registering to start at login because %s.", why)
			log.Printf("Copy YouPiper Helper to your Applications folder and open it from there.")
		} else if changed, err := autostart.EnsureInstalled(exe); err != nil {
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

	if runtime.GOOS == "darwin" && (autostart.IsPackaged() || os.Getenv("FORCE_TRAY") == "1") {
		tray.Init(
			"YouPiper",
			server.ServerVersion,
			func() {
				log.Println("Menu action: Turn Off Helper")
				if err := autostart.Uninstall(); err != nil {
					log.Printf("Warning: failed to uninstall autostart: %v", err)
				}
				tray.SetStatus(false)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(ctx)
			},
			func() {
				log.Println("Menu action: Turn On Helper")
				if err := autostart.Install(exe); err != nil {
					log.Printf("Warning: failed to install autostart: %v", err)
				}
				tray.SetStatus(true)
			},
			func() {
				log.Println("Menu action: Open YouPiper")
				tray.OpenDefaultBrowser("https://youpiper.com")
			},
			func() {
				log.Println("Menu action: Quit")
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = srv.Shutdown(ctx)
				os.Exit(0)
			},
		)
		tray.SetStatus(true)

		go func() { srvErr <- srv.Start() }()

		go func() {
			select {
			case <-stop:
				log.Println("Received shutdown signal. Gracefully stopping server...")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(ctx)
				tray.Stop()
			case err := <-srvErr:
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Printf("Server stopped: %v", err)
				}
				tray.Stop()
			}
		}()

		tray.Run()
		return
	}

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
			//
			// Whichever copy binds first keeps serving, so after replacing the
			// app the old process is still the live one until it stops. Say so:
			// otherwise an upgrade looks like it did nothing.
			log.Printf("Another YouPiper Helper is already using %s, so this copy will exit.", *addrFlag)
			log.Printf("The copy that is already running stays in charge until it is quit or you log in again.")
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
		{binpath.Deno, dl.DenoPath},
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
			// Distinguish "nothing is registered" from "a different copy is
			// registered". The second means the copy that starts at login is not
			// this one, which is the more confusing state to be in and the one
			// worth naming.
			other, _ := autostart.RegisteredPath()
			if other != "" {
				fmt.Println("Start at login: no — another copy is registered:")
				fmt.Printf("                %s\n", other)
			} else {
				fmt.Println("Start at login: no")
			}
		}
		if stable, why := autostart.StableLocation(exe); !stable {
			fmt.Printf("                cannot register here: %s\n", why)
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
		{binpath.Deno, dl.DenoPath},
	} {
		if t.path == "" {
			fmt.Fprintf(w, "%-8s NOT FOUND\n", t.tool+":")
			continue
		}
		fmt.Fprintf(w, "%-8s %s (%s)\n", t.tool+":", t.path, binpath.Source(t.tool, t.path))
	}
}
