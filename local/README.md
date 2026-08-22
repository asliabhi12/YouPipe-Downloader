# YouPiper Helper (Go Companion)

## Purpose
YouPiper Helper is a lightweight localhost HTTP daemon that runs directly on the user's computer. It processes media downloads locally and saves them directly to the user's Downloads directory (`~/Downloads/YTD Local`).

> **Note:** the output folder is still named `YTD Local` from before the YouPiper rename. Renaming it is deferred until Helper packaging, so existing users' files aren't orphaned. See `GetDefaultDownloadsDir` in `internal/downloader/downloader.go`.

## Technology
- **Language**: Go 1.22+
- **Core Engine**: `yt-dlp` CLI + `FFmpeg`
- **Port**: `127.0.0.1:47821`
- **Third-party Go modules**: none

## Dependencies

Distributed builds bundle `yt-dlp`, `ffmpeg` and `ffprobe` inside the artifact, so an end user installs nothing. For development you need those three on `PATH` instead:

- Go 1.22+
- `yt-dlp`
- `ffmpeg` (and `ffprobe`)

`internal/binpath` resolves each tool in this order — bundled copy beside the executable, then `PATH`, with `YOUPIPER_YTDLP` / `YOUPIPER_FFMPEG` / `YOUPIPER_FFPROBE` overriding both for testing. `-status` prints which source won.

## How to Run

```bash
# Run the companion
go run ./cmd/agent

# Show configuration, resolved tool paths and login-item state
go run ./cmd/agent -status

# Run tests
go test ./...
```

### Flags

| Flag | Effect |
|---|---|
| `-addr` | listen address (default `127.0.0.1:47821`; non-loopback hosts are refused) |
| `-output` | download directory |
| `-status` | print configuration, resolved tool paths and login-item state, then exit |
| `-install-startup` | register to start at login, then exit |
| `-uninstall` | remove the login registration, then exit |
| `-no-startup` | run without registering at login |
| `-version` | print the version, then exit |

Only a build produced by `packaging/build.sh` registers itself at login; `go run` never does. The gate is a build-time `-ldflags` constant, not a path heuristic — see `internal/autostart`.

## Packaging

`packaging/` builds the installable end-user artifacts (`YouPiper Helper.app` + `.dmg`, and `YouPiper-Helper.exe` + `.zip`), bundles the third-party tools, and handles login registration and signing. See [packaging/PACKAGING.md](packaging/PACKAGING.md) for the build steps, the bundled-software inventory and licences, the current signing/notarization status, and the test matrix.

## API Specification

- `GET /health`: Returns agent readiness and dependency availability.
- `POST /metadata`: Accepts `{ "url": "..." }`, returns video metadata and format options.
- `POST /downloads`: Accepts `{ "url": "...", "quality": "..." }`, starts async download job.
- `GET /downloads/{id}`: Returns progress and status of a download job.
- `POST /downloads/{id}/cancel`: Cancels an active download job.
