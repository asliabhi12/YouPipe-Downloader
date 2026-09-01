#!/bin/bash
set -e

# Start the PO Token Provider HTTP server in background if available
if [ -d "/app/bgutil-server" ]; then
    echo "[youpiper] Starting bgutil PO Token Provider HTTP server on port 4416..."
    (cd /app/bgutil-server && node build/main.js > /tmp/pot_provider.log 2>&1) &
    
    # Wait briefly for provider server to respond on /ping
    for i in {1..30}; do
        if curl -s http://127.0.0.1:4416/ping > /dev/null 2>&1; then
            echo "[youpiper] bgutil PO Token Provider is active and ready."
            break
        fi
        sleep 0.2
    done
fi

PORT="${PORT:-10000}"
echo "[youpiper] Starting Gunicorn WSGI server on port $PORT..."
exec gunicorn --bind 0.0.0.0:$PORT --workers 1 --threads 8 --timeout 300 app:app
