# YTD Local — Minimal Go Companion

A lightweight, headless local companion application for a web-based video downloader.

`YTD Local` acts as an orchestration layer on the user's machine, receiving download requests from a website via a local HTTP API, delegating video extraction to `yt-dlp` and post-processing to `ffmpeg`, and storing the resulting media files directly into the user's `Downloads/YTD Local` folder.

---

## 🏗️ Architecture

```text
Browser
   ↓
Website (Web Interface)
   ↓
localhost HTTP (127.0.0.1:47821)
   ↓
Go Companion (ytd-local)
   ↓
yt-dlp
   ↓
FFmpeg
   ↓
User's Downloads folder (~/Downloads/YTD Local)
```

### Components:
* **HTTP Server (`internal/server`)**: Binds exclusively to `127.0.0.1:47821`. Handles metadata extraction, download initiation, polling progress, and cancellation with CORS support for local web apps.
* **Job Manager (`internal/jobs`)**: In-memory, thread-safe asynchronous job queue for managing download lifecycles and tracking real-time status.
* **Downloader (`internal/downloader`)**: Isolates external subprocess execution (`yt-dlp` and `ffmpeg`), parsing metadata and stdout progress streams.

---

## 🛠️ Requirements

* **Go**: 1.22 or newer
* **yt-dlp**: Required on PATH for extraction & download engine
* **FFmpeg**: Required on PATH for audio/video merging and post-processing

---

## 📥 Installation & Setup

### 1. Install Go
* **macOS (via Homebrew)**:
  ```bash
  brew install go
  ```
* **Windows / Linux**:
  Download installer from [golang.org/dl](https://golang.org/dl/).

### 2. Install yt-dlp
* **macOS (via Homebrew)**:
  ```bash
  brew install yt-dlp
  ```
* **Windows (via winget or pip)**:
  ```powershell
  winget install yt-dlp
  ```
  or
  ```powershell
  python3 -m pip install -U yt-dlp
  ```

### 3. Install FFmpeg
* **macOS (via Homebrew)**:
  ```bash
  brew install ffmpeg
  ```
* **Windows (via winget)**:
  ```powershell
  winget install Gyan.FFmpeg
  ```

---

## 🚀 Running the Application

To run the agent locally:

```bash
go run ./cmd/agent
```

Or build and run the binary:

```bash
go build -o agent ./cmd/agent
./agent
```

Command line flags:
* `-addr`: HTTP bind address (default `"127.0.0.1:47821"`).
* `-output`: Download directory (default `"~/Downloads/YTD Local"`).

---

## 📡 API Reference

All requests must be made to `http://127.0.0.1:47821`.

### 1. `GET /health`
Verifies whether the companion agent and its dependencies are available.

**Response (200 OK):**
```json
{
  "status": "ok",
  "version": "0.1.0",
  "ytdlp_available": true,
  "ffmpeg_available": true
}
```

---

### 2. `POST /metadata`
Retrieves video metadata without starting a download.

**Request:**
```json
{
  "url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
}
```

**Response (200 OK):**
```json
{
  "id": "dQw4w9WgXcQ",
  "title": "Rick Astley - Never Gonna Give You Up",
  "thumbnail": "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg",
  "duration": 213,
  "uploader": "Rick Astley",
  "formats": [
    { "quality": "1080p", "height": 1080 },
    { "quality": "720p", "height": 720 },
    { "quality": "480p", "height": 480 },
    { "quality": "360p", "height": 360 }
  ]
}
```

---

### 3. `POST /downloads`
Enqueues an asynchronous download job.

**Request:**
```json
{
  "url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
  "quality": "1080p"
}
```

Supported qualities: `best`, `1080p`, `720p`, `480p`, `360p`, `audio`.

**Response (200 OK):**
```json
{
  "job_id": "c1f7a1e2-9b34-4b5c-890d-123456789abc"
}
```

---

### 4. `GET /downloads/{id}`
Polls progress and status for a given job.

**Response (200 OK):**
```json
{
  "job_id": "c1f7a1e2-9b34-4b5c-890d-123456789abc",
  "url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
  "quality": "1080p",
  "status": "downloading",
  "progress": 57.4,
  "speed": 8234123,
  "eta": 21,
  "created_at": "2026-08-17T23:30:00Z"
}
```

Possible `status` values: `queued`, `downloading`, `processing`, `completed`, `failed`, `cancelled`.

---

### 5. `POST /downloads/{id}/cancel`
Cancels an ongoing download job.

**Response (200 OK):**
```json
{
  "job_id": "c1f7a1e2-9b34-4b5c-890d-123456789abc",
  "status": "cancelled"
}
```

---

## 🔍 yt-dlp Integration Rationale

We evaluated two third-party wrapper libraries (`github.com/lrstanley/go-ytdlp` and `github.com/wader/goutubedl`) against standard library `os/exec` direct invocation.

### Decision: Direct `os/exec` Invocation (Option C)

**Rationale:**
1. **Zero External Dependencies**: Using Go's standard library `os/exec` avoids third-party API breakage when `yt-dlp` updates its CLI flags or output formats.
2. **Deterministic Progress Parsing**: `yt-dlp` supports custom progress templates (`--progress-template`) which output structured, delimited lines. This provides reliable parsing of speed, ETA, downloaded bytes, and status without relying on wrapper abstractions.
3. **Robust Process Cancellation**: Wrapping `os/exec` with Go's `context.Context` (`exec.CommandContext`) ensures child processes are cleanly terminated when a user cancels a job or the server shuts down.
4. **Simplicity & Reliability**: Directly delegating to the installed `yt-dlp` binary keeps the companion simple, maintainable, and aligned with core design principles.

---

## 🧪 Testing

Run test suite:

```bash
go test ./...
```

Run race detector:

```bash
go test -race ./...
```

Run static analysis:

```bash
go vet ./...
```
