# Stage 1: Build bgutil PO Token Provider server
FROM node:20-slim AS bgutil-builder
WORKDIR /build
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates
RUN git clone https://github.com/Brainicism/bgutil-ytdlp-pot-provider.git repo \
    && cd repo/server \
    && npm ci \
    && npx tsc

# Stage 2: Production app container
FROM python:3.11-slim

# Install system dependencies required by yt-dlp & bgutil PO-token provider runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    curl \
    ca-certificates \
    nodejs \
    && rm -rf /var/lib/apt-get/lists/*

WORKDIR /app

# Copy built bgutil server from builder stage
COPY --from=bgutil-builder /build/repo/server /app/bgutil-server

# Copy Python requirements and install dependencies
COPY requirements.txt ./requirements.txt
RUN pip install --no-cache-dir -r requirements.txt

# Copy server application code and entrypoint script
COPY . .
RUN chmod +x ./start.sh || true

ENV PORT=10000
ENV PYTHONUNBUFFERED=1
ENV POT_PROVIDER_URL=http://127.0.0.1:4416

# Health check endpoint
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:$PORT/health || exit 1

# Run entrypoint script
CMD ["/app/start.sh"]
