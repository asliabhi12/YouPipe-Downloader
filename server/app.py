"""
YouPiper Online Downloader Server (Python / Flask)

Minimal yt-dlp + FFmpeg downloader, reusing ReClip's approach:
  http://github.com/averygan/reclip  (app.py)

Reused ReClip logic:
  - parse_ytdlp_json()      : parse yt-dlp "-j" output (first JSON object per line)
  - best-per-height map     : one best format per resolution (by tbr)
  - download style          : "-f {format_id}+bestaudio/best --merge-output-format mp4"
                             and "-x --audio-format mp3" for audio
  - post-download glob      : find produced file by {job_id}.* , drop leftovers
  - title sanitization      : safe filename from video title

Added for Phase 1 (not in ReClip):
  - /api/analyze (no download during analysis)
  - explicit quality -> exact height format selection (no silent downgrade)
  - ffprobe verification before marking a job completed
  - simple progress parsing
  - max 2 concurrent downloads (in-process semaphore)
  - tmp/ storage with cleanup + file removal after delivery
"""

import glob
import json
import os
import re
import signal
import subprocess
import threading
import time
import uuid

from flask import Flask, after_this_request, jsonify, request, send_file

app = Flask(__name__)

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
TMP_DIR = os.path.join(BASE_DIR, "tmp")
os.makedirs(TMP_DIR, exist_ok=True)

MAX_CONCURRENT = int(os.environ.get("MAX_CONCURRENT", 2))
CLEANUP_AGE_SECONDS = int(os.environ.get("CLEANUP_AGE_SECONDS", 30 * 60))
MAX_DOWNLOAD_SECONDS = int(os.environ.get("MAX_DOWNLOAD_SECONDS", 30 * 60))
ALLOWED_HEIGHTS = (360, 480, 720, 1080)
PROGRESS_RE = re.compile(r"^\[download\]\s+(\d+(?:\.\d+)?)%")

# Optional operator-provided identity for sites that require it (e.g. YouTube
# returns HTTP 403 for unauthenticated media requests). Default: plain default
# Optional operator-provided identity for sites that require it.
COOKIES_FILE = os.environ.get("YTDLP_COOKIES_FILE", "").strip()
COOKIES_FROM_BROWSER = os.environ.get("YTDLP_COOKIES_FROM_BROWSER", "").strip()

# PO Token Provider URL (defaults to local HTTP provider on 4416)
POT_PROVIDER_URL = os.environ.get("POT_PROVIDER_URL", "http://127.0.0.1:4416").strip()

# Client strategies: Primary (mweb,android with PO token) & Fallback (web_embedded)
PLAYER_CLIENT_RAW = (os.environ.get("YTDLP_PLAYER_CLIENT") or "").strip()
if not PLAYER_CLIENT_RAW or PLAYER_CLIENT_RAW.lower() in ("default", "auto", "mweb,android", "ios,mweb,web_safari,android"):
    PRIMARY_PLAYER_CLIENT = "mweb,web_embedded,android"
    FALLBACK_PLAYER_CLIENT = "web_embedded,android"

elif PLAYER_CLIENT_RAW.lower() in ("none", "off", "disabled"):
    PRIMARY_PLAYER_CLIENT = ""
    FALLBACK_PLAYER_CLIENT = ""
else:
    PRIMARY_PLAYER_CLIENT = PLAYER_CLIENT_RAW
    FALLBACK_PLAYER_CLIENT = os.environ.get("YTDLP_FALLBACK_PLAYER_CLIENT", "web_embedded").strip()

PLAYER_CLIENT = PRIMARY_PLAYER_CLIENT


jobs = {}
jobs_lock = threading.Lock()
semaphore = threading.BoundedSemaphore(MAX_CONCURRENT)


# --------------------------------------------------------------------------
# ReClip: parse yt-dlp JSON output (first object per line, multiple objects ok)
# --------------------------------------------------------------------------
def parse_ytdlp_json(stdout):
    for line in stdout.splitlines():
        line = line.strip()
        if line:
            return json.loads(line)
    raise ValueError("yt-dlp returned no data")


# --------------------------------------------------------------------------
# Filename helpers
# --------------------------------------------------------------------------
def sanitize_filename(name):
    name = re.sub(r'[\\/:*?"<>|]', "_", name)
    name = re.sub(r"[\x00-\x1f]", "", name)
    name = re.sub(r"\s+", " ", name).strip(" ._")
    return (name or "video")[:100]


def unique_path(directory, filename):
    """Never overwrite an existing file: append ' (n)' when needed."""
    path = os.path.join(directory, filename)
    base, ext = os.path.splitext(filename)
    n = 1
    while os.path.exists(path):
        n += 1
        path = os.path.join(directory, f"{base} ({n}){ext}")
    return path


# --------------------------------------------------------------------------
# yt-dlp helpers
# --------------------------------------------------------------------------
def base_ytdlp_args(client=None, use_pot=True):
    args = ["yt-dlp", "--no-playlist", "--no-warnings", "--buffer-size", "16k", "--postprocessor-args", "ffmpeg:-threads 1"]
    if COOKIES_FILE:
        args += ["--cookies", COOKIES_FILE]
    elif COOKIES_FROM_BROWSER:
        args += ["--cookies-from-browser", COOKIES_FROM_BROWSER]
    
    target_client = client if client is not None else PRIMARY_PLAYER_CLIENT
    if target_client:
        args += ["--extractor-args", f"youtube:player_client={target_client}"]
    
    if use_pot and POT_PROVIDER_URL:
        args += ["--extractor-args", f"youtubepot-bgutilhttp:base_url={POT_PROVIDER_URL}"]
        
    return args


def ytdlp_info(url, timeout=90):
    # Primary extraction attempt (with PO-Token Provider)
    cmd = base_ytdlp_args(client=PRIMARY_PLAYER_CLIENT, use_pot=True) + ["-j", url]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    if result.returncode == 0:
        return parse_ytdlp_json(result.stdout), PRIMARY_PLAYER_CLIENT, True
    
    err1 = last_error(result.stderr)
    app.logger.warning("Primary analyze stderr: %s", result.stderr.strip())
    app.logger.warning("Primary yt-dlp analyze failed: %s. Trying fallback client...", err1)
    
    # Fallback extraction attempt (without PO-Token requirement to allow web_embedded/tv clients)
    if FALLBACK_PLAYER_CLIENT and FALLBACK_PLAYER_CLIENT != PRIMARY_PLAYER_CLIENT:
        cmd_fb = base_ytdlp_args(client=FALLBACK_PLAYER_CLIENT, use_pot=False) + ["-j", url]
        result_fb = subprocess.run(cmd_fb, capture_output=True, text=True, timeout=timeout)
        if result_fb.returncode == 0:
            app.logger.info("Fallback yt-dlp analyze succeeded with client: %s", FALLBACK_PLAYER_CLIENT)
            return parse_ytdlp_json(result_fb.stdout), FALLBACK_PLAYER_CLIENT, False
        err1 = last_error(result_fb.stderr)
        app.logger.warning("Fallback analyze stderr: %s", result_fb.stderr.strip())
        app.logger.warning("Fallback yt-dlp analyze failed: %s", err1)
        
    raise DownloaderError(err1)





def last_error(text):
    lines = [l.strip() for l in text.strip().splitlines() if l.strip()]
    return lines[-1] if lines else "yt-dlp failed"



def available_heights(info):
    heights = {
        f.get("height")
        for f in info.get("formats", [])
        if f.get("height") and f.get("vcodec", "none") != "none"
    }
    return sorted(heights)


def audio_available(info):
    return any(f.get("acodec", "none") != "none" for f in info.get("formats", []))


def pick_video_format(info, height):
    """One best format for the requested height (ReClip's per-height map).

    Prefers a video-only DASH format (clean merge with bestaudio).
    Falls back to a combined format (already includes audio).
    Returns None when the height is unavailable.
    """
    best = {}
    for f in info.get("formats", []):
        if f.get("height") != height:
            continue
        if f.get("vcodec", "none") == "none":
            continue
        acodec = f.get("acodec", "none")
        kind = "video" if acodec == "none" else "combined"
        tbr = f.get("tbr") or 0
        if kind not in best or tbr > (best[kind].get("tbr") or 0):
            best[kind] = f
    return best.get("video") or best.get("combined")


# --------------------------------------------------------------------------
# ffprobe verification (required before marking completed)
# --------------------------------------------------------------------------
def ffprobe(path):
    cmd = [
        "ffprobe", "-v", "error", "-print_format", "json",
        "-show_format", "-show_streams", path,
    ]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    if result.returncode != 0:
        raise VerificationError(f"ffprobe failed: {last_error(result.stderr)}")
    return json.loads(result.stdout)


def verify_video(path):
    if not os.path.exists(path):
        raise VerificationError("output file missing")
    size = os.path.getsize(path)
    if size <= 0:
        raise VerificationError("output file is empty")
    data = ffprobe(path)
    fmt = (data.get("format") or {}).get("format_name", "")
    vstream = next(
        (s for s in data.get("streams", []) if s.get("codec_type") == "video"), None
    )
    astream = next(
        (s for s in data.get("streams", []) if s.get("codec_type") == "audio"), None
    )
    if "mp4" not in fmt and "mov" not in fmt:
        raise VerificationError(f"container is {fmt!r}, not mp4")
    if not vstream:
        raise VerificationError("no video stream found")
    if not astream:
        raise VerificationError("no audio stream found")
    duration = float((data.get("format") or {}).get("duration") or 0)
    if duration <= 0:
        raise VerificationError("invalid duration")
    return {
        "container": fmt,
        "width": vstream.get("width"),
        "height": vstream.get("height"),
        "vcodec": vstream.get("codec_name"),
        "acodec": astream.get("codec_name"),
        "duration": round(duration, 2),
        "size": size,
    }


def verify_audio(path):
    if not os.path.exists(path):
        raise VerificationError("output file missing")
    size = os.path.getsize(path)
    if size <= 0:
        raise VerificationError("output file is empty")
    data = ffprobe(path)
    fmt = (data.get("format") or {}).get("format_name", "")
    astream = next(
        (s for s in data.get("streams", []) if s.get("codec_type") == "audio"), None
    )
    if "mp3" not in fmt:
        raise VerificationError(f"container is {fmt!r}, not mp3")
    if not astream or astream.get("codec_name") != "mp3":
        raise VerificationError("no mp3 audio stream found")
    duration = float((data.get("format") or {}).get("duration") or 0)
    if duration <= 0:
        raise VerificationError("invalid duration")
    return {
        "container": fmt,
        "codec": astream.get("codec_name"),
        "duration": round(duration, 2),
        "size": size,
    }


# --------------------------------------------------------------------------
# Download worker
# --------------------------------------------------------------------------
class DownloaderError(Exception):
    pass


class VerificationError(Exception):
    pass


def _update_progress(job, line):
    m = PROGRESS_RE.match(line.strip())
    if m:
        with jobs_lock:
            job["progress"] = min(99, int(float(m.group(1))))  # 100 set at completion


def _stream_lines(proc, job):
    lines = []
    for raw in iter(proc.stdout.readline, ""):
        line = raw.rstrip("\n")
        lines.append(line)
        _update_progress(job, line)
    return lines


def _run_capture_stream(args, job):
    print(f"[job {job['job_id']}] cmd: {' '.join(args)}", flush=True)
    proc = subprocess.Popen(
        args,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        start_new_session=True,
    )
    start = time.time()
    output = []
    t = threading.Thread(target=lambda: output.append(_stream_lines(proc, job)), daemon=True)
    t.start()
    while proc.poll() is None:
        if time.time() - start > MAX_DOWNLOAD_SECONDS:
            with jobs_lock:
                job["error"] = f"download timed out after {MAX_DOWNLOAD_SECONDS}s"
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
            t.join(timeout=5)
            return 1, ["[youpiper] download timed out"]
        time.sleep(0.2)
    t.join(timeout=5)
    code = proc.returncode or 0
    lines = output[0] if output else []
    return code, lines


def _worker(job):
    with semaphore:
        with jobs_lock:
            job["status"] = "downloading"
        try:
            _perform_download(job)
        except Exception as e:  # noqa: BLE001
            _fail(job, f"{type(e).__name__}: {e}")


def _set_meta(job, info):
    with jobs_lock:
        job["title"] = info.get("title", "")
        job["id"] = info.get("id", "")


def _perform_download(job):
    info, client_used, pot_used = ytdlp_info(job["url"])
    _set_meta(job, info)
    job["client_used"] = client_used
    job["pot_used"] = pot_used

    if job["format"] == "mp3":
        _download_audio(job, info)
        return

    fmt = pick_video_format(info, job["height"])
    if fmt is None:
        avail = ", ".join(f"{h}p" for h in available_heights(info)) or "none"
        raise DownloaderError(
            f"{job['quality']} is not available for this video. Available: {avail}"
        )

    out_template = os.path.join(TMP_DIR, f"{job['job_id']}.%(ext)s")
    cmd = base_ytdlp_args(client=job["client_used"], use_pot=job["pot_used"]) + ["--newline", "-o", out_template]
    if fmt.get("acodec", "none") == "none":
        cmd += ["-f", f'{fmt["format_id"]}+bestaudio/best']
    else:
        cmd += ["-f", fmt["format_id"]]
    cmd += ["--merge-output-format", "mp4"]
    cmd.append(job["url"])

    code, lines = _run_capture_stream(cmd, job)
    if code != 0:
        error_line = next((l for l in reversed(lines) if l.strip()), "yt-dlp failed")
        raise DownloaderError(f"yt-dlp exited {code}: {error_line}")

    produced = sorted(glob.glob(os.path.join(TMP_DIR, f"{job['job_id']}.*")))
    mp4s = [p for p in produced if p.lower().endswith(".mp4")]
    chosen = mp4s[0] if mp4s else (produced[0] if produced else None)
    if chosen is None:
        raise DownloaderError("download finished but no output file was produced")
    for other in produced:
        if other != chosen:
            _safe_remove(other)

    if not chosen.lower().endswith(".mp4"):
        raise DownloaderError(
            f"output is {os.path.splitext(chosen)[1]}, not an mp4 container"
        )

    verify = verify_video(chosen)
    _finalize(job, chosen, verify, quality_label=job["quality"])


def _download_audio(job, info):
    out_template = os.path.join(TMP_DIR, f"{job['job_id']}.%(ext)s")
    cmd = base_ytdlp_args(client=job.get("client_used"), use_pot=job.get("pot_used", True)) + [
        "--newline", "-x", "--audio-format", "mp3",
        "-o", out_template, job["url"],
    ]
    code, lines = _run_capture_stream(cmd, job)
    if code != 0:
        error_line = next((l for l in reversed(lines) if l.strip()), "yt-dlp failed")
        raise DownloaderError(f"yt-dlp exited {code}: {error_line}")

    produced = sorted(glob.glob(os.path.join(TMP_DIR, f"{job['job_id']}.*")))
    mp3s = [p for p in produced if p.lower().endswith(".mp3")]
    chosen = mp3s[0] if mp3s else (produced[0] if produced else None)
    if chosen is None:
        raise DownloaderError("download finished but no output file was produced")
    for other in produced:
        if other != chosen:
            _safe_remove(other)

    if not chosen.lower().endswith(".mp3"):
        raise DownloaderError(
            f"output is {os.path.splitext(chosen)[1]}, not mp3"
        )

    verify = verify_audio(chosen)
    _finalize(job, chosen, verify, quality_label="audio")



def _finalize(job, src_path, verify, quality_label):
    title = sanitize_filename(str(job.get("title") or "video"))
    clean_id = re.sub(r"[^A-Za-z0-9_-]", "", str(job.get("id") or "")) or "video"
    ext = os.path.splitext(src_path)[1]
    final_name = f"{title} {clean_id} {quality_label}{ext}"
    final_path = unique_path(TMP_DIR, final_name)
    os.replace(src_path, final_path)
    with jobs_lock:
        job["file"] = final_path
        job["filename"] = os.path.basename(final_path)
        job["verify"] = verify
        job["progress"] = 100
        job["status"] = "completed"
        job["error"] = None


def _fail(job, message):
    with jobs_lock:
        job["status"] = "failed"
        job["error"] = _sanitize_error(message)
        job["progress"] = 0
        for f in glob.glob(os.path.join(TMP_DIR, f"{job['job_id']}.*")):
            _safe_remove(f)


def _safe_remove(path):
    try:
        if os.path.isfile(path):
            os.remove(path)
    except OSError:
        pass


def _sanitize_error(msg):
    """Sanitize internal paths, bot challenge text, commands, and stack traces from API responses."""
    if not msg:
        return "An error occurred during media processing"
    s = str(msg)
    if "Sign in to confirm" in s or "cookies" in s or "bot" in s.lower():
        return "YouTube is temporarily limiting access to this video. Please try another video or try again later."
    # Remove internal filesystem paths
    s = re.sub(r"/(?:[a-zA-Z0-9_.-]+/)+[a-zA-Z0-9_.-]+", "[file]", s)
    if "yt-dlp exited" in s or "ERROR: [youtube]" in s:
        parts = s.split(":", 1)
        if len(parts) > 1:
            clean_part = parts[1].strip()
            if "Sign in to confirm" in clean_part or "bot" in clean_part.lower():
                return "YouTube is temporarily limiting access to this video. Please try another video or try again later."
            s = f"Extraction error: {clean_part}"
        else:
            s = "Extraction failed"
    return s


# --------------------------------------------------------------------------
# Cleanup thread & signal handlers
# --------------------------------------------------------------------------
def _cleanup_loop():
    while True:
        time.sleep(60)
        now = time.time()
        with jobs_lock:
            stale = [
                jid for jid, j in jobs.items()
                if now - j.get("created", now) > CLEANUP_AGE_SECONDS
                and j["status"] in ("completed", "failed")
            ]
            for jid in stale:
                if jobs[jid].get("file"):
                    _safe_remove(jobs[jid]["file"])
                del jobs[jid]
        for f in glob.glob(os.path.join(TMP_DIR, "*")):
            if now - os.path.getmtime(f) > CLEANUP_AGE_SECONDS:
                _safe_remove(f)


threading.Thread(target=_cleanup_loop, daemon=True).start()


def _handle_signal(sig, frame):
    for f in glob.glob(os.path.join(TMP_DIR, "*")):
        _safe_remove(f)
    os._exit(0)


try:
    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)
except (ValueError, AttributeError):
    pass


# --------------------------------------------------------------------------
# CORS & API
# --------------------------------------------------------------------------
ALLOWED_ORIGINS_RAW = os.environ.get(
    "ALLOWED_ORIGINS",
    "https://youpiper.ytd-web.workers.dev,http://127.0.0.1:4321,http://localhost:4321,http://127.0.0.1:5001"
)
ALLOWED_ORIGINS = [o.strip() for o in ALLOWED_ORIGINS_RAW.split(",") if o.strip()]


@app.after_request
def _cors(resp):
    origin = request.headers.get("Origin")
    if origin:
        if "*" in ALLOWED_ORIGINS:
            resp.headers["Access-Control-Allow-Origin"] = "*"
        elif origin in ALLOWED_ORIGINS:
            resp.headers["Access-Control-Allow-Origin"] = origin
            resp.headers["Vary"] = "Origin"
    elif "*" in ALLOWED_ORIGINS:
        resp.headers["Access-Control-Allow-Origin"] = "*"

    resp.headers.setdefault("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
    resp.headers.setdefault("Access-Control-Allow-Headers", "Content-Type")
    resp.headers.setdefault("Access-Control-Expose-Headers", "Content-Disposition")
    return resp


@app.route("/api/<path:_path>", methods=["OPTIONS"])
@app.route("/health", methods=["OPTIONS"])
def _cors_preflight(_path=None):
    return ("", 204)


@app.errorhandler(400)
def _handle_bad_request(e):
    desc = getattr(e, "description", "Bad request")
    return jsonify({"error": _sanitize_error(desc)}), 400


@app.errorhandler(404)
def _handle_not_found(e):
    return jsonify({"error": "Resource not found"}), 404


@app.errorhandler(405)
def _handle_method_not_allowed(e):
    return jsonify({"error": "Method not allowed"}), 405


@app.errorhandler(500)
@app.errorhandler(Exception)
def _handle_internal_error(e):
    app.logger.error("Internal error: %s", e, exc_info=True)
    return jsonify({"error": "An internal server error occurred"}), 500


@app.post("/api/analyze")
def analyze():
    data = request.get_json(silent=True) or {}
    url = (data.get("url") or "").strip()
    if not url:
        return jsonify({"error": "No URL provided"}), 400
    try:
        info, client_used, pot_used = ytdlp_info(url)
    except DownloaderError as e:

        return jsonify({"error": _sanitize_error(str(e))}), 400
    except Exception as e:
        app.logger.error("Analyze error: %s", e, exc_info=True)
        return jsonify({"error": "Unable to analyze video link"}), 500
    return jsonify({
        "title": info.get("title", ""),
        "uploader": info.get("uploader", ""),
        "thumbnail": info.get("thumbnail", ""),
        "duration": info.get("duration"),
        "video_heights": available_heights(info),
        "audio_available": audio_available(info),
    })


@app.post("/api/download")
def start_download():
    data = request.get_json(silent=True) or {}
    url = (data.get("url") or "").strip()
    if not url:
        return jsonify({"error": "No URL provided"}), 400

    kind = (data.get("format") or "").strip().lower()
    if kind not in ("mp4", "mp3"):
        return jsonify({"error": "format must be 'mp4' or 'mp3'"}), 400

    if kind == "mp4":
        quality = (data.get("quality") or "").strip().lower()
        m = re.fullmatch(r"(\d+)p", quality)
        if not m or int(m.group(1)) not in ALLOWED_HEIGHTS:
            return jsonify({
                "error": "quality must be one of 360p, 480p, 720p, 1080p for mp4"
            }), 400
        height = int(m.group(1))
    else:
        quality = "mp3"
        height = None

    job = {
        "job_id": uuid.uuid4().hex[:10],
        "status": "queued",
        "progress": 0,
        "filename": None,
        "file": None,
        "error": None,
        "url": url,
        "format": kind,
        "quality": quality,
        "height": height,
        "title": None,
        "id": None,
        "created": time.time(),
    }
    with jobs_lock:
        jobs[job["job_id"]] = job

    threading.Thread(target=_worker, args=(job,), daemon=True).start()
    return jsonify({"job_id": job["job_id"]}), 202


@app.get("/api/status/<job_id>")
def status(job_id):
    with jobs_lock:
        job = jobs.get(job_id)
    if not job:
        return jsonify({"error": "job not found"}), 404
    return jsonify({
        "job_id": job["job_id"],
        "status": job["status"],
        "progress": job["progress"],
        "filename": job["filename"],
        "error": job["error"],
    })


@app.get("/api/file/<job_id>")
def get_file(job_id):
    with jobs_lock:
        job = jobs.get(job_id)
    if not job:
        return jsonify({"error": "job not found"}), 404
    if job["status"] != "completed" or not job.get("file"):
        return jsonify({
            "error": f"file not ready (status: {job['status']})"
        }), 409
    path = job["file"]

    @after_this_request
    def _remove(resp):
        _safe_remove(path)
        with jobs_lock:
            job["file"] = None
        return resp

    return send_file(path, as_attachment=True, download_name=job["filename"])


@app.post("/api/debug_extract")
def debug_extract():
    data = request.get_json(silent=True) or {}
    url = (data.get("url") or "").strip()
    client = (data.get("client") or PRIMARY_PLAYER_CLIENT).strip()
    use_pot = data.get("use_pot", True)
    
    cmd = ["yt-dlp", "-v", "--no-playlist", "--no-warnings", "--buffer-size", "16k"]
    if client:
        cmd += ["--extractor-args", f"youtube:player_client={client}"]
    if use_pot and POT_PROVIDER_URL:
        cmd += ["--extractor-args", f"youtubepot-bgutilhttp:base_url={POT_PROVIDER_URL}"]
    cmd += ["-j", url]
    
    res = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    return jsonify({
        "cmd": " ".join(cmd),
        "returncode": res.returncode,
        "stdout_len": len(res.stdout),
        "stderr": res.stderr.splitlines()[-20:] if res.stderr else [],
    })


@app.get("/health")
def health():

    pot_status = "unavailable"
    pot_log = None
    try:
        import urllib.request
        with urllib.request.urlopen("http://127.0.0.1:4416/ping", timeout=2) as req:
            if req.getcode() == 200:
                pot_status = "ok"
    except Exception as e:
        pot_status = f"error: {e}"
        if os.path.exists("/tmp/pot_provider.log"):
            try:
                with open("/tmp/pot_provider.log", "r") as f:
                    pot_log = f.read().splitlines()[-5:]
            except Exception:
                pass

    res = {
        "status": "ok",
        "service": "youpiper-server",
        "pot_provider": pot_status,
        "primary_client": PRIMARY_PLAYER_CLIENT,
        "fallback_client": FALLBACK_PLAYER_CLIENT,
        "max_concurrent": MAX_CONCURRENT,
    }
    if pot_log:
        res["pot_log"] = pot_log
    return jsonify(res)



if __name__ == "__main__":
    port = int(os.environ.get("PORT", 5001))
    app.run(host="0.0.0.0", port=port, debug=False)