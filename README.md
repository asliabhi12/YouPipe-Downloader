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
2. **Local Mode**: If YouPiper Helper is running, analysis and downloads are routed directly on-device to save files to `~/Downloads`.
3. **Online Fallback**: If the local helper is unavailable, downloads are routed to the Python Flask backend, which processes the job and serves the completed file via browser download.

---

## Components

### 1. YouPiper Helper (`/local`)
- **Tech Stack**: Go 1.22+, no third-party modules
- **Host**: `127.0.0.1:47821`
- **Engine**: `yt-dlp` CLI + `FFmpeg`, bundled inside distributed builds
- **Output Directory**: `~/Downloads` (standard OS Downloads folder)
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

## Testing

One command verifies everything that does not need a network, a running Helper or a browser:

```bash
./test.sh          # go vet, go test, go test -race, frontend tests, Astro production build
./test.sh go       # Go only
./test.sh web      # frontend tests and build only
```

The frontend suite (`web/tests/`) covers Helper health parsing, the availability state machine and its transitions, analyze/download backend selection, fallback routing, timeout behaviour and error-message translation. It runs on Node's built-in test runner with native TypeScript support, so it needs no test framework and no extra dependencies.

`./test.sh` also runs the offline half of the bundled-runtime regression checks (`REG-JS-001`, `REG-JS-002`): that a JavaScript runtime is pinned for every platform, and that the installed application carries one and finds it without the developer's `PATH`.

The rest of those checks need the network, the installed application and a real video. They drive the launchd-started Helper end to end — metadata, then 480p/720p/1080p/MP3 downloads, each verified with `ffprobe` rather than trusted because the job said "completed":

```bash
local/packaging/verify-runtime.sh              # REG-JS-001 … REG-JS-008
local/packaging/verify-runtime.sh --offline    # only what needs nothing
```

Checks that need the real environment — a packaged Helper being started and stopped, a real video, a real browser — live in `web/tests/browser/helper_indicator.py` and are run by hand:

```bash
cd web && npm run dev          # terminal 1
cd server && python3 app.py    # terminal 2
python3 web/tests/browser/helper_indicator.py   # terminal 3 (restores the Helper on exit)
```

---

## The Helper bundles its own JavaScript runtime

YouTube will not serve a media stream until a JavaScript challenge has been
solved, and `yt-dlp` solves it by running a script in an external JavaScript
runtime that it looks for on `PATH`. launchd starts the Helper with
`PATH=/usr/bin:/bin:/usr/sbin:/sbin`, which holds no runtime on a stock macOS
install — so a Helper relying on `PATH` worked from a developer's shell and
failed for every real user at login, reporting `/health` as `ok` the whole time.

The packaged Helper now ships **Deno** in `Contents/Resources/bin` and names its
absolute path on every `yt-dlp` call, so nothing depends on `PATH`. `/health`
reports `js_runtime_available` and answers `degraded` without it, so "installed"
and "able to download" can be told apart from outside. Details and the
size/sandbox trade-off: [local/README.md](local/README.md) and
[local/packaging/PACKAGING.md](local/packaging/PACKAGING.md).

The end user still installs nothing: no Node, no Deno, no Python, no PATH
changes, no Terminal.

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
| `PORT` | `5001` (Render defaults to `10000`) | HTTP server port |
| `MAX_CONCURRENT` | `2` | Maximum parallel download jobs |
| `ALLOWED_ORIGINS` | `https://youpiper.ytd-web.workers.dev,http://127.0.0.1:4321,http://localhost:4321` | Allowed CORS origin whitelist (comma-separated) |
| `CLEANUP_AGE_SECONDS` | `1800` | Expiry age (seconds) for temporary files |
| `YTDLP_PLAYER_CLIENT` | `web_embedded` | yt-dlp player client. Empty string opts out |
| `YTDLP_COOKIES_FILE` | *(none)* | Path to Netscape format cookie file |

---

### Deployment Setup

#### 1. Backend Deployment (Render)
The backend is packaged for containerized deployment on Render using [server/Dockerfile](server/Dockerfile) or [render.yaml](render.yaml):
* **Docker Context:** `./server`
* **Health Check Path:** `/health`
* **WSGI Server:** Gunicorn (`--workers 2 --threads 4`)

#### 2. Frontend Deployment (Cloudflare Workers)
Deploy the Astro frontend to Cloudflare Workers Static Assets:
```bash
cd web
PUBLIC_ONLINE_URL=https://<your-render-service>.onrender.com npm run deploy
```

#### Environment Variables (Web)
| Variable | Default | Description |
|---|---|---|
| `PUBLIC_ONLINE_URL` | `http://127.0.0.1:5001` | Backend URL for online processing fallback (e.g., Render service URL) |

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
