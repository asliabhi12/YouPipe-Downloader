# YouPiper Helper (Go Companion)

## Purpose
YouPiper Helper is a lightweight localhost HTTP daemon that runs directly on the user's computer. It processes media downloads locally and saves them directly to the user's Downloads directory (`~/Downloads/YTD Local`).

> **Note:** the output folder is still named `YTD Local` from before the YouPiper rename. Renaming it is deferred until Helper packaging, so existing users' files aren't orphaned. See `GetDefaultDownloadsDir` in `internal/downloader/downloader.go`.

## Technology
- **Language**: Go 1.22+
- **Core Engine**: `yt-dlp` CLI + `FFmpeg`
- **Port**: `127.0.0.1:47821`

## Dependencies
- Go 1.22+
- `yt-dlp` (must be installed and in PATH)
- `ffmpeg` (must be installed and in PATH)

## How to Run

```bash
# Run agent
go run ./cmd/agent

# Run tests
go test ./...
```

## API Specification

- `GET /health`: Returns agent readiness and dependency availability.
- `POST /metadata`: Accepts `{ "url": "..." }`, returns video metadata and format options.
- `POST /downloads`: Accepts `{ "url": "...", "quality": "..." }`, starts async download job.
- `GET /downloads/{id}`: Returns progress and status of a download job.
- `POST /downloads/{id}/cancel`: Cancels an active download job.
