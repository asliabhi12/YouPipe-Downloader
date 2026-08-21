# YouPiper Server (Online Downloader)

Minimal online downloader that reuses the ReClip downloader approach
(yt-dlp CLI + FFmpeg) behind a small Flask API.

## Requirements

- Python 3.10+
- Flask (`pip install -r requirements.txt`)
- `yt-dlp` on PATH (tested with 2026.07.04)
- `ffmpeg` + `ffprobe` on PATH (tested with FFmpeg 8.1)
- Optional: browser cookies for sites that reject anonymous media requests
  (see "YouTube note" below)

## Install

```bash
cd server
pip install -r requirements.txt
```

## Run

```bash
python app.py            # http://127.0.0.1:5001
```

Environment variables (all optional):

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | 5001 | HTTP port |
| `MAX_CONCURRENT` | 2 | Max simultaneous downloads |
| `CLEANUP_AGE_SECONDS` | 1800 | Remove completed/failed jobs and temp files after this age |
| `MAX_DOWNLOAD_SECONDS` | 1800 | Per-download timeout |
| `YTDLP_PLAYER_CLIENT` | `web_embedded` | Documented YouTube extractor arg — provides a credential-free path (see note). Set to another client to override, or to an empty string to opt out and get stock `yt-dlp` behavior |
| `YTDLP_COOKIES_FILE` | (none) | Netscape cookie file passed to `yt-dlp --cookies` |
| `YTDLP_COOKIES_FROM_BROWSER` | (none) | e.g. `chrome`, passed to `yt-dlp --cookies-from-browser` |

## API

### `POST /api/analyze`

```json
{ "url": "https://www.youtube.com/watch?v=..." }
```

Returns title, uploader, thumbnail, duration, available video heights, audio
availability. Does not download anything.

### `POST /api/download`

```json
{ "url": "...", "quality": "720p", "format": "mp4" }
{ "url": "...", "format": "mp3" }
```

Supported MP4 qualities: `360p`, `480p`, `720p`, `1080p`. Returns `{ "job_id": "..." }`.

Requested quality maps to an exact yt-dlp format at that height. If the height
is not available, the job fails with the list of available heights — it never
silently substitutes a different resolution.

### `GET /api/status/<job_id>`

```json
{ "job_id": "...", "status": "queued|downloading|completed|failed",
  "progress": 0, "filename": "...", "error": null }
```

### `GET /api/file/<job_id>`

Serves the finished file as a browser download and then removes it from disk.
Returns `404` if the job is unknown, `409` if it is not completed yet.

## Example curl

```bash
curl -X POST http://127.0.0.1:5001/api/analyze \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://www.youtube.com/watch?v=gkUjup06XB8"}'

JOB=$(curl -s -X POST http://127.0.0.1:5001/api/download \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://www.youtube.com/watch?v=gkUjup06XB8","quality":"720p","format":"mp4"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["job_id"])')

curl -s http://127.0.0.1:5001/api/status/$JOB   # poll until completed
curl -OJ http://127.0.0.1:5001/api/file/$JOB    # download (file removed after)
```

## Design notes

- Downloader logic (yt-dlp command construction, per-height format map,
  `--merge-output-format mp4`, `-x --audio-format mp3`, output glob, filename
  sanitization) is reused from ReClip (`github.com/averygan/reclip`, `app.py`).
- Every download is verified with `ffprobe` (container, video/audio codecs,
  resolution, duration) before the job is marked `completed`. A nonzero yt-dlp
  exit code alone is not sufficient.
- MP4 output is a real MP4 container (remuxed with FFmpeg, never a renamed
  `.webm`). MP3 output is a real MP3 (transcoded with FFmpeg).
- Filenames follow `[title] [id] [quality].mp4` / `[title] [id] [audio].mp3`.
  Existing files are never overwritten (`... (2)`, `... (3)`).
- Max 2 concurrent downloads via an in-process semaphore; extra jobs queue.
- Temp files live in `server/tmp/`, are removed after browser delivery, and
  abandoned/failed jobs are cleaned by a background thread.
- No database, no Redis/Celery, no auth, no Docker, no frontend integration.

## YouTube note (Phase 2A RESOLVED)

As of August 2026, YouTube returns `HTTP 403 Forbidden` for anonymous media
requests from yt-dlp's default player client (`ANDROID_VR`). The verbose log
shows `PO Token Providers: none`; with that, media delivery is rejected.
Metadata extraction still works; media download fails. This is a reproducible,
server-side YouTube restriction, **not** a bug in this code.

**Phase 2A finding — a credential-free path exists.** Using the documented
yt-dlp extractor option `youtube:player_client=web_embedded` downloads full
resolution ranges (144p–2160p) and MP3 without any cookies:

```bash
YTDLP_PLAYER_CLIENT=web_embedded python app.py
```

Validated locally on `gkUjup06XB8` (see "Phase 2A test results"). Also working
credential-free: `android` and `mweb` clients (but limited to ~360p for this
video), and `web_embedded` with yt-dlp's JS-challenge solving. Clients `web`,
`ios`, `web_safari` returned no formats; `tv` required a PO token; the default
and `android_vr` returned 403.

This is a documented configuration option, not a circumvention hack, and it
does not rely on anyone's personal browser credentials.

**Remaining gate (deployment).** These tests ran on a residential IP. YouTube
can behave differently for datacenter/cloud IPs, so before building anything on
it the same test battery must pass on the actual Oracle VM. The hybrid design
already has a fallback (see `README.md` at repo root): if online downloading
fails on the VM, the local companion still serves users.

The server reads these optional env vars: `YTDLP_PLAYER_CLIENT` (defaults to
`web_embedded`, since anonymous YouTube media requests 403 without it and the
Helper sets the same option unconditionally), `YTDLP_COOKIES_FILE`, and
`YTDLP_COOKIES_FROM_BROWSER` (both default to unset — no cookies are ever read
unless an operator explicitly configures them).

## Phase 1 test results

Test video: `https://www.youtube.com/watch?v=gkUjup06XB8` (Android Police,
483s). Server run with `YTDLP_COOKIES_FROM_BROWSER=chrome`.

| Requested | Actual | Container | Video Codec | Audio Codec | Duration | Size | Result |
|---|---|---|---|---|---|---|---|
| 360p | 640x360 | MP4 (h264) | h264 | AAC | 482.77s | 14.3 MB | PASS |
| 480p | 854x480 | MP4 (h264) | h264 | AAC | 482.77s | 17.5 MB | PASS |
| 720p | 1280x720 | MP4 (h264) | h264 | AAC | 482.77s | 22.6 MB | PASS |
| 1080p | 1920x1080 | MP4 (h264) | h264 | AAC | 482.77s | 61.0 MB | PASS |
| mp3 | — | MP3 | — | MP3 | 482.72s | 6.1 MB | PASS |

Requested quality always matched the actual output resolution exactly (no
silent downgrades). All results were verified with `ffprobe`.

### Failure/concurrency checks

| Check | Result |
|---|---|
| Invalid URL (DNS failure) | 400, clear error |
| Non-video URL (`example.com`) | 400, `Unsupported URL` |
| Unavailable quality (1080p on a 240p-max video) | failed, `1080p is not available for this video. Available: 144p, 240p` |
| Failed yt-dlp process (YouTube 403, no cookies) | failed, exact yt-dlp error surfaced |
| Bad `format` / bad `quality` values | 400 |
| File served + removed after delivery | PASS (tmp/ empty after downloads) |
| 3 simultaneous downloads | PASS (2 downloading, 1 queued, then completed) |
| Unique filenames (no overwrite) | PASS (`480p`, `480p (2)`, `480p (3)`) |
| Abandoned completed job cleanup | PASS (job dropped, temp file removed after age) |
| Missing/empty output file, ffprobe failure | handled in code (`VerificationError` → job `failed`) |

## Phase 2A test results (credential-free)

Server run with `YTDLP_PLAYER_CLIENT=web_embedded`, **no cookies**.

| Requested | Actual | Container | Video Codec | Audio Codec | Duration | Size | Result |
|---|---|---|---|---|---|---|---|
| 360p | 640x360 | MP4 | VP9 | Opus | 482.67s | 17.9 MB | PASS |
| 480p | 854x480 | MP4 | VP9 | Opus | 482.67s | 20.9 MB | PASS |
| 720p | 1280x720 | MP4 | VP9 | Opus | 482.67s | 30.0 MB | PASS |
| 1080p | 1920x1080 | MP4 | H.264 | Opus | 482.67s | 60.9 MB | PASS |
| mp3 | — | MP3 | — | MP3 | 482.66s | 6.3 MB | PASS |

Note: `web_embedded` serves VP9/AV1 + Opus (remuxed to real MP4), not H.264/AAC.
This plays in all modern browsers/players; H.264/AAC is the broader-compat
fallback if needed later. Requested quality always matched actual output
resolution exactly, verified by `ffprobe`.