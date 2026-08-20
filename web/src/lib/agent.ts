export interface AgentHealth {
  status: string;
  version: string;
  ytdlp_available: boolean;
  ffmpeg_available: boolean;
}

export interface VideoFormat {
  quality: string;
  height: number;
}

export interface VideoMetadata {
  id: string;
  title: string;
  thumbnail: string;
  duration: number;
  uploader: string;
  formats: VideoFormat[];
}

export interface DownloadJob {
  job_id: string;
  url: string;
  quality: string;
  status: 'queued' | 'downloading' | 'processing' | 'completed' | 'failed' | 'cancelled';
  progress: number;
  speed: number;
  eta: number;
  error?: string;
  created_at: string;
}

export const AGENT_URL = 'http://127.0.0.1:47821';

export async function checkAgent(): Promise<AgentHealth | null> {
  try {
    const res = await fetch(`${AGENT_URL}/health`, { method: 'GET' });
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

export async function getMetadata(url: string): Promise<VideoMetadata> {
  const res = await fetch(`${AGENT_URL}/metadata`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url })
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'metadata_failed' }));
    throw new Error(err.details || err.error || 'Failed to fetch video metadata');
  }

  return await res.json();
}

export async function createDownload(url: string, quality: string): Promise<{ job_id: string }> {
  const res = await fetch(`${AGENT_URL}/downloads`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url, quality })
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'download_failed' }));
    throw new Error(err.details || err.error || 'Failed to start download job');
  }

  return await res.json();
}

export async function getDownload(jobId: string): Promise<DownloadJob> {
  const res = await fetch(`${AGENT_URL}/downloads/${jobId}`, { method: 'GET' });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'job_not_found' }));
    throw new Error(err.details || err.error || 'Download job not found');
  }

  return await res.json();
}

export async function cancelDownload(jobId: string): Promise<{ job_id: string; status: string }> {
  const res = await fetch(`${AGENT_URL}/downloads/${jobId}/cancel`, { method: 'POST' });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'cancel_failed' }));
    throw new Error(err.details || err.error || 'Failed to cancel download job');
  }

  return await res.json();
}
