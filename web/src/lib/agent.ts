// Unified API layer for YouPiper web.
//
// The UI thinks in domain terms: analyze(), download(), getStatus(), cancelDownload(), getFile().
// Automatically routes to the local Go companion if available, otherwise falls back
// to the online Python downloader backend.

export type HelperState = 'HELPER_UNKNOWN' | 'HELPER_AVAILABLE' | 'HELPER_UNAVAILABLE';

export interface AgentHealth {
  status: string;
  version: string;
  ytdlp_available: boolean;
  ffmpeg_available: boolean;
}

export interface VideoFormat {
  quality: string;
  label?: string;
  height?: number;
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
  backend: 'local' | 'online';
  url: string;
  quality: string;
  status: 'queued' | 'downloading' | 'processing' | 'completed' | 'failed' | 'cancelled';
  progress: number;
  speed?: number;
  eta?: number;
  filename?: string;
  error?: string;
  created_at: string;
}

const LOCAL_AGENT_URL = 'http://127.0.0.1:47821';
const ONLINE_URL =
  (import.meta.env.PUBLIC_ONLINE_URL as string | undefined) ?? 'http://127.0.0.1:5001';

const SUPPORTED_VIDEO_HEIGHTS = [360, 480, 720, 1080];
export const QUALITY_LABELS: Record<string, string> = {
  '1080p': '1080p Full HD',
  '720p': '720p HD',
  '480p': '480p SD',
  '360p': '360p Low',
  audio: 'Audio Only (MP3)'
};

export const QUALITY_SHORT_LABELS: Record<string, string> = {
  '1080p': 'Full HD',
  '720p': 'HD',
  '480p': 'SD',
  '360p': 'Low',
  audio: 'Audio only'
};

export function getResolutionText(height?: number, quality?: string): string {
  if (quality === 'audio') return '';
  if (height && height > 0) {
    const widthMap: Record<number, number> = {
      2160: 3840,
      1440: 2560,
      1080: 1920,
      720: 1280,
      480: 854,
      360: 640,
      240: 426,
      144: 256
    };
    const width = widthMap[height] || Math.round((height * 16) / 9);
    return `${width} × ${height}`;
  }
  if (quality === '1080p') return '1920 × 1080';
  if (quality === '720p') return '1280 × 720';
  if (quality === '480p') return '854 × 480';
  if (quality === '360p') return '640 × 360';
  return '';
}

async function readError(res: Response): Promise<string> {
  try {
    const data = await res.json();
    return data.error || data.details || `Request failed (${res.status})`;
  } catch {
    return `Request failed (${res.status})`;
  }
}

// --- Helper state probe ----------------------------------------------------
export async function checkHelperState(): Promise<HelperState> {
  try {
    const res = await fetch(`${LOCAL_AGENT_URL}/health`, { method: 'GET' });
    if (!res.ok) return 'HELPER_UNAVAILABLE';
    const data = await res.json();
    if (data && (data.status === 'ok' || data.status === 'degraded')) {
      return 'HELPER_AVAILABLE';
    }
    return 'HELPER_UNAVAILABLE';
  } catch {
    return 'HELPER_UNAVAILABLE';
  }
}

export async function isHelperAvailable(): Promise<boolean> {
  const state = await checkHelperState();
  return state === 'HELPER_AVAILABLE';
}

export async function checkAgent(): Promise<AgentHealth | null> {
  try {
    const res = await fetch(`${LOCAL_AGENT_URL}/health`, { method: 'GET' });
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

// --- Unified domain API methods ---------------------------------------------
export async function analyze(url: string): Promise<VideoMetadata> {
  const state = await checkHelperState();
  if (state === 'HELPER_AVAILABLE') {
    return analyzeLocal(url);
  }
  return analyzeOnline(url);
}

async function analyzeLocal(url: string): Promise<VideoMetadata> {
  const res = await fetch(`${LOCAL_AGENT_URL}/metadata`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url })
  });

  if (!res.ok) {
    throw new Error(await readError(res));
  }

  const data = await res.json();
  const rawFormats: Array<{ quality: string; height: number }> = Array.isArray(data.formats)
    ? data.formats
    : [];

  const formats: VideoFormat[] = rawFormats
    .filter((f) => SUPPORTED_VIDEO_HEIGHTS.includes(f.height))
    .sort((a, b) => b.height - a.height)
    .map((f) => ({
      quality: f.quality,
      label: QUALITY_LABELS[f.quality] || f.quality,
      height: f.height
    }));

  formats.push({ quality: 'audio', label: QUALITY_LABELS.audio });

  return {
    id: data.id || '',
    title: data.title || '',
    thumbnail: data.thumbnail || '',
    duration: data.duration || 0,
    uploader: data.uploader || '',
    formats
  };
}

async function analyzeOnline(url: string): Promise<VideoMetadata> {
  const res = await fetch(`${ONLINE_URL}/api/analyze`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url })
  });

  if (!res.ok) {
    throw new Error(await readError(res));
  }

  const data = await res.json();
  const heights: number[] = Array.isArray(data.video_heights) ? data.video_heights : [];
  const formats: VideoFormat[] = heights
    .filter((h) => SUPPORTED_VIDEO_HEIGHTS.includes(h))
    .sort((a, b) => b - a)
    .map((h) => ({ quality: `${h}p`, label: QUALITY_LABELS[`${h}p`], height: h }));

  if (data.audio_available) {
    formats.push({ quality: 'audio', label: QUALITY_LABELS.audio });
  }

  return {
    id: '',
    title: data.title || '',
    thumbnail: data.thumbnail || '',
    duration: data.duration || 0,
    uploader: data.uploader || '',
    formats
  };
}

export async function download(
  url: string,
  quality: string
): Promise<{ job_id: string; backend: 'local' | 'online' }> {
  const state = await checkHelperState();
  if (state === 'HELPER_AVAILABLE') {
    const res = await fetch(`${LOCAL_AGENT_URL}/downloads`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url, quality })
    });

    if (!res.ok) {
      throw new Error(await readError(res));
    }

    const data = await res.json();
    return { job_id: data.job_id, backend: 'local' };
  }

  const body =
    quality === 'audio' ? { url, format: 'mp3' } : { url, quality, format: 'mp4' };

  const res = await fetch(`${ONLINE_URL}/api/download`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });

  if (!res.ok) {
    throw new Error(await readError(res));
  }

  const data = await res.json();
  return { job_id: data.job_id, backend: 'online' };
}

export async function getStatus(
  jobId: string,
  backend: 'local' | 'online'
): Promise<DownloadJob> {
  if (backend === 'local') {
    const res = await fetch(`${LOCAL_AGENT_URL}/downloads/${jobId}`, { method: 'GET' });
    if (!res.ok) {
      throw new Error(await readError(res));
    }
    const data = await res.json();
    return {
      job_id: data.job_id || jobId,
      backend: 'local',
      url: data.url || '',
      quality: data.quality || '',
      status: data.status,
      progress: data.progress || 0,
      speed: data.speed,
      eta: data.eta,
      error: data.error,
      created_at: data.created_at || ''
    };
  }

  const res = await fetch(`${ONLINE_URL}/api/status/${jobId}`, { method: 'GET' });
  if (!res.ok) {
    throw new Error(await readError(res));
  }
  const data = await res.json();
  return {
    job_id: data.job_id || jobId,
    backend: 'online',
    url: '',
    quality: data.quality || '',
    status: data.status,
    progress: data.progress || 0,
    filename: data.filename,
    error: data.error,
    created_at: ''
  };
}

export async function cancelDownload(
  jobId: string,
  backend: 'local' | 'online'
): Promise<{ job_id: string; status: string }> {
  if (backend === 'local') {
    const res = await fetch(`${LOCAL_AGENT_URL}/downloads/${jobId}/cancel`, {
      method: 'POST'
    });
    if (!res.ok) {
      throw new Error(await readError(res));
    }
    return await res.json();
  }
  return { job_id: jobId, status: 'cancelled' };
}

export async function getFile(jobId: string): Promise<boolean> {
  const url = `${ONLINE_URL}/api/file/${jobId}`;
  try {
    const res = await fetch(url, { method: 'GET' });
    if (!res.ok) {
      throw new Error(await readError(res));
    }
    const blob = await res.blob();
    const objUrl = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = objUrl;
    a.download =
      filenameFromContentDisposition(res.headers.get('Content-Disposition')) ||
      `download-${jobId}`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(objUrl);
    return true;
  } catch (err: any) {
    console.error('File download failed:', err);
    return false;
  }
}

function filenameFromContentDisposition(header: string | null): string {
  if (!header) return '';
  const star = /filename\*=UTF-8''([^;]+)/i.exec(header);
  if (star) return decodeURIComponent(star[1]);
  const plain = /filename="?([^";]+)"?/i.exec(header);
  return plain ? plain[1] : '';
}