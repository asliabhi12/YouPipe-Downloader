# YouPiper

YouPiper is a high-performance media downloader platform featuring a hybrid architecture. It seamlessly supports both **Local** (on-device Go companion agent for direct-to-disk downloads without server limits) and **Online** (server-side Python Flask processing) download workflows with an Astro 5 frontend.

---

## Architecture Overview

```
youpiper/
├── local/      # YouPiper Helper (Go) — Runs on localhost:47821
├── server/     # Online Downloader Backend (Python/Flask) — Server-side downloads
├── web/        # Frontend Website (Astro 5) — UI with automatic agent detection
└── README.md   # Project Overview & Quick Start
```

### How It Works
1. **Helper Detection**: The web client probes the local helper daemon (`http://127.0.0.1:47821/health`).
2. **Local Mode**: If YouPiper Helper is running, analysis and downloads are routed directly on-device to save files to `~/Downloads/YTD Local`.
3. **Online Fallback**: If the local helper is unavailable, downloads are routed to the Python Flask backend, which processes the job and serves the completed file via browser download.

---

## Components

### 1. YouPiper Helper (`/local`)
- **Tech Stack**: Go 1.22+, no third-party modules
- **Host**: `127.0.0.1:47821`
- **Engine**: `yt-dlp` CLI + `FFmpeg`, bundled inside distributed builds
- **Output Directory**: `~/Downloads/YTD Local` — still carries the pre-rename name; see `local/README.md`
- **Purpose**: Direct-to-disk local downloading without server bandwidth limits or queue delays.
- **Distribution**: `local/packaging/` produces an installable `.app`/`.dmg` and `.exe`/`.zip` that start at login and need nothing else installed — see [local/packaging/PACKAGING.md](local/packaging/PACKAGING.md).

### 2. Online Server Backend (`/server`)
- **Tech Stack**: Python 3.10+ / Flask
- **Host**: `127.0.0.1:5001` (default)
- **Engine**: `yt-dlp` CLI + `FFmpeg`
- **Purpose**: Server-side download processing, job status tracking, and file delivery for users without the local helper.

### 3. Web Frontend (`/web`)
- **Tech Stack**: Astro 5, TypeScript, Custom CSS
- **Host**: `127.0.0.1:4321`
- **Purpose**: Modern user interface, video URL analysis, format selection (1080p, 720p, 480p, 360p, MP3), live progress tracking, and companion helper download page.

---

## Quick Start Guide

### Prerequisites
- **Go** 1.22+ (for local agent)
- **Python** 3.10+ (for online server)
- **Node.js** 18+ & **npm** (for web frontend)
- **yt-dlp** and **FFmpeg** installed and accessible on system `PATH`

These are development prerequisites. An end user installs none of them: the packaged Helper bundles `yt-dlp` and `FFmpeg`, and the online fallback needs nothing on the client.

---

### Running YouPiper Helper (Go)

```bash
cd local
go run ./cmd/agent
```

To see resolved tool paths and configuration:
```bash
cd local
go run ./cmd/agent -status
```

To build the installable end-user artifacts:
```bash
cd local/packaging
./fetch-vendor.sh && ./build.sh --windows
```

To run test suite:
```bash
cd local
go test ./...
```

---

### Running the Online Server (Python)

```bash
cd server
pip install -r requirements.txt
python app.py
```

#### Environment Variables (Server)
| Variable | Default | Description |
|---|---|---|
| `PORT` | `5001` | HTTP server port |
| `MAX_CONCURRENT` | `2` | Maximum parallel download jobs |
| `CLEANUP_AGE_SECONDS` | `1800` | Expiry age (seconds) for temporary files |
| `YTDLP_PLAYER_CLIENT` | `web_embedded` | yt-dlp player client. Empty string opts out |
| `YTDLP_COOKIES_FILE` | *(none)* | Path to Netscape format cookie file |

---

### Running the Web Frontend (Astro)

```bash
cd web
npm install
npm run dev
```

#### Production Build
```bash
cd web
npm run build
```

#### Environment Variables (Web)
| Variable | Default | Description |
|---|---|---|
| `PUBLIC_ONLINE_URL` | `http://127.0.0.1:5001` | Backend URL for online processing fallback |

---

## API Summary

### Local Helper API (`127.0.0.1:47821`)
- `GET /health` — Health check & dependency verification (`yt-dlp`, `ffmpeg`).
- `POST /metadata` — Extract video info and available quality formats.
- `POST /downloads` — Initiate asynchronous local download job.
- `GET /downloads/{id}` — Poll job status, progress percentage, and speed.
- `POST /downloads/{id}/cancel` — Cancel active download job.

### Online Server API (`127.0.0.1:5001`)
- `POST /api/analyze` — Extract metadata and supported quality height options.
- `POST /api/download` — Queue server-side download (MP4 / MP3).
- `GET /api/status/{job_id}` — Query job progress and status.
- `GET /api/file/{job_id}` — Download completed media file.
