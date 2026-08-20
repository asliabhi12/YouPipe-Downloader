# YouPiper

YouPiper is a video downloader platform with a hybrid architecture supporting both **Local** (on-device companion agent) and **Online** (server-side processing) download workflows.

---

## Project Structure

```
youpiper/
│
├── local/      # Local Companion Agent (Go)
├── server/     # Online Downloader Backend (Python)
├── web/        # Frontend Website (Astro)
└── README.md   # Project Overview & Guide
```

---

## Components

### 1. LOCAL
- **Technology**: Go
- **Environment**: User's local machine (`127.0.0.1:47821`)
- **Engine**: `yt-dlp` + `FFmpeg`
- **Purpose**: Direct-to-disk video/audio downloading without server bandwidth limits.

### 2. SERVER
- **Technology**: Python (Flask)
- **Environment**: Online Server (e.g., Oracle Cloud VM)
- **Engine**: `yt-dlp` + `FFmpeg`
- **Purpose**: Server-side download processing for users without the local agent installed.

### 3. WEB
- **Technology**: Astro 5
- **Environment**: Public Website / Web Browser
- **Purpose**: User interface, SEO pages, URL analysis, local agent detection, and download controls.

---

## Development Guide

### Running Local Companion Agent (Go)
```bash
cd local
go run ./cmd/agent
```

### Running Frontend Website (Astro)
```bash
cd web
npm install
npm run dev
```

### Running Online Server (Python)
```bash
cd server
# Implementation starts in Phase 1
python app.py
```
