# YouPiper Server (Online Downloader)

## Purpose
The online server component for YouPiper. It acts as a server-side video downloader fallback when users do not have the local companion agent installed.

## Technology
- **Language**: Python 3.10+
- **Framework**: Flask
- **Core Engine**: `yt-dlp` CLI + `FFmpeg`

## Setup & Running

### Requirements
- Python 3.10+
- `yt-dlp` installed and in PATH
- `ffmpeg` installed and in PATH

### Running locally
```bash
# Install dependencies
pip install -r requirements.txt

# Run server
python app.py
```

## Status
- **Phase 0.5**: Structure created.
- **Phase 1**: Online downloader implementation will be added here.
