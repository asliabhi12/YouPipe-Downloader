"""
YouPiper Online Downloader Server (Python / Flask)

This server will host the server-side video downloader based on ReClip (yt-dlp + FFmpeg).
Implementation will be completed in Phase 1.
"""

from flask import Flask, jsonify

app = Flask(__name__)

@app.route("/health", methods=["GET"])
def health():
    return jsonify({
        "status": "ok",
        "service": "youpiper-server",
        "message": "YouPiper Online Server placeholder. Implementation starts in Phase 1."
    })

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=True)
