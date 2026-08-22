// Helper availability, and the backend routing that follows from it.
//
// One store owns the answer to "is the YouPiper Helper on this computer usable
// right now?". One router turns that answer into a backend choice. Nothing else
// in the app is allowed to ask the question independently — a second opinion is
// how the UI ends up claiming the Helper is ready while requests go elsewhere.
//
// Both take their fetch and their timers as arguments, so the state machine and
// the routing can be driven from tests with no browser, no network, and no
// Helper installed.

export type HelperState = 'checking' | 'available' | 'unavailable' | 'error';

export type Backend = 'local' | 'online';

export interface HelperHealth {
  status: string;
  version: string;
  ytdlp_available: boolean;
  ffmpeg_available: boolean;
}

/**
 * A settled read of Helper availability.
 *
 * `reason` is a technical sentence for the console. It is deliberately never
 * shown in the UI: the user gets the wording in HELPER_COPY instead.
 */
export interface HelperSnapshot {
  state: HelperState;
  health: HelperHealth | null;
  reason: string;
  checkedAt: number;
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

/** What analyze() resolves to: the metadata, plus which backend produced it. */
export interface AnalyzeResult extends VideoMetadata {
  backend: Backend;
  /** True when the Helper was expected to answer and the online backend covered for it. */
  fellBack: boolean;
  /** User-facing sentence explaining a fallback, or '' when nothing surprising happened. */
  notice: string;
}

export interface DownloadJob {
  job_id: string;
  backend: Backend;
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

// --- Tunables ---------------------------------------------------------------

/**
 * Health checks are a loopback round trip, so they either answer in a few
 * milliseconds or there is nothing listening. A short ceiling is what keeps the
 * indicator from sitting on "Checking" forever when something is bound to the
 * port but not answering.
 */
export const DEFAULT_HEALTH_TIMEOUT_MS = 2000;

/**
 * Background re-check interval. Long enough to be invisible on the Helper's CPU
 * (it is one request every 15s against an idle loopback server), short enough
 * that starting or quitting the Helper is reflected without a reload. Coming
 * back to the tab triggers an immediate refresh, which is what makes the common
 * case — "I just started it, is it there yet?" — feel instant without polling
 * hard for it.
 */
export const DEFAULT_POLL_MS = 15000;

/**
 * How long a probe result is trusted before an action re-checks. Covers the
 * click-to-analyze gap without firing a second health request for a state we
 * measured moments ago.
 */
export const DEFAULT_FRESH_MS = 4000;

// --- User-facing copy -------------------------------------------------------

/**
 * Every string the user can read about the Helper lives here.
 *
 * The rule these follow: no host names, no port numbers, no tool names, no HTTP
 * status codes. "YouPiper Helper" is the only name for it. An unavailable
 * Helper is a normal state for most visitors, not a failure, so it is worded as
 * information rather than an error — and it says "not running" rather than "not
 * installed", because a failed loopback request cannot tell those apart.
 */
export const HELPER_COPY = {
  checking: {
    title: 'Checking for YouPiper Helper…',
    detail: 'One moment.'
  },
  available: {
    title: 'YouPiper Helper ready',
    detail: 'Faster downloads · No server queue'
  },
  unavailable: {
    title: 'YouPiper Helper not running',
    detail: 'Downloads will use the online downloader.'
  },
  error: {
    title: 'YouPiper Helper not responding',
    detail: 'Downloads will use the online downloader.'
  }
} as const satisfies Record<HelperState, { title: string; detail: string }>;

export const NOTICE_HELPER_LOST =
  'YouPiper Helper stopped responding, so we used the online downloader instead.';

export const NOTICE_HELPER_BALKED =
  'YouPiper Helper could not read this video, so we used the online downloader instead.';

export const MESSAGE_HELPER_CONNECTION_LOST =
  'Lost the connection to YouPiper Helper, so this download may not have finished. ' +
  'Make sure YouPiper Helper is running, then try again.';

/**
 * Backend error codes translated for humans.
 *
 * The Go and Python backends both answer failures with a machine code such as
 * `metadata_failed`. Showing that code was the original bug: it tells the user
 * nothing and looks like a crash. The raw code and detail still reach the
 * console for debugging.
 */
const FRIENDLY_BY_CODE: Record<string, string> = {
  invalid_url: 'That does not look like a video link. Please check it and try again.',
  invalid_json: 'Something went wrong sending that request. Please try again.',
  metadata_failed: "We couldn't analyze this video.",
  yt_dlp_missing:
    'YouPiper Helper is missing part of its setup, so it could not read this video.',
  ffmpeg_missing:
    'YouPiper Helper is missing part of its setup, so it could not prepare this file.',
  download_failed: 'The download could not be completed.',
  job_not_found: 'That download is no longer available. Please start it again.',
  file_not_found: 'The finished file could not be found. Please try downloading it again.',
  missing_job_id: 'That download is no longer available. Please start it again.'
};

export const GENERIC_ANALYZE_FAILURE =
  "We couldn't analyze this video. Please check the link and try again.";

export const GENERIC_DOWNLOAD_FAILURE =
  "We couldn't start that download. Please try again.";

/** Translates a backend error code, falling back to a supplied sentence. */
export function friendlyMessage(code: string, fallback: string): string {
  return FRIENDLY_BY_CODE[code] ?? fallback;
}

/**
 * Translates the `error` string on a failed job.
 *
 * A job carries its failure as `"<code>: <technical detail>"` — for example
 * `"download_failed: exit status 1"`. Rendering that verbatim is the same defect
 * as showing `metadata_failed` for an analysis: it names an internal condition
 * and reads like a crash. The code is mapped and the detail dropped.
 */
export function friendlyJobError(raw: string | undefined, backend: Backend): string {
  const fallback =
    backend === 'local'
      ? 'YouPiper Helper could not finish this download. Please try again.'
      : 'This download could not be completed. Please try again.';

  if (!raw) return fallback;
  const code = raw.split(':', 1)[0].trim();
  if (code === 'download_failed' || code === 'metadata_failed') {
    // Both mean the same thing to the user at this point: the file did not
    // arrive. Which stage failed is a console-level detail.
    return fallback;
  }
  return friendlyMessage(code, fallback);
}

/** An error carrying the machine code alongside the sentence shown to the user. */
export class BackendError extends Error {
  code: string;
  detail: string;
  backend: Backend;

  constructor(message: string, code: string, detail: string, backend: Backend) {
    super(message);
    this.name = 'BackendError';
    this.code = code;
    this.detail = detail;
    this.backend = backend;
  }
}

function errText(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * Turns a non-2xx backend response into a BackendError.
 *
 * Both backends answer with `{error, details}`. The code goes on the error
 * object for logging and for deciding what to do next; the message is the
 * sentence a person reads.
 */
export async function readBackendError(
  res: Response,
  backend: Backend,
  fallback: string
): Promise<BackendError> {
  let code = '';
  let detail = '';
  try {
    const body = await res.json();
    if (body && typeof body === 'object') {
      const b = body as Record<string, unknown>;
      code = typeof b.error === 'string' ? b.error : '';
      detail = typeof b.details === 'string' ? b.details : '';
    }
  } catch {
    // A body that is not JSON tells us nothing beyond the status code.
  }
  const message = code ? friendlyMessage(code, fallback) : fallback;
  return new BackendError(message, code, detail || `HTTP ${res.status}`, backend);
}

// --- Health parsing ---------------------------------------------------------

export interface HealthVerdict {
  ok: boolean;
  reason: string;
  /**
   * The parsed body, or null when the response could not be understood as a
   * health report at all. Null is what separates "the Helper says it is not
   * ready" from "something answered but it was not the Helper".
   */
  health: HelperHealth | null;
}

/**
 * Decides whether a /health body means the Helper can actually do work.
 *
 * Ready requires all three: status "ok", the downloader component present, and
 * the media converter present. `degraded` is deliberately not accepted — the
 * Helper reports it precisely when one of those components is missing, and
 * calling that "ready" is the false-positive this indicator exists to avoid.
 */
export function parseHelperHealth(raw: unknown): HealthVerdict {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) {
    return { ok: false, reason: 'health response was not a JSON object', health: null };
  }

  const r = raw as Record<string, unknown>;
  if (typeof r.status !== 'string') {
    return { ok: false, reason: 'health response has no status field', health: null };
  }

  const health: HelperHealth = {
    status: r.status,
    version: typeof r.version === 'string' ? r.version : '',
    ytdlp_available: r.ytdlp_available === true,
    ffmpeg_available: r.ffmpeg_available === true
  };

  if (health.status !== 'ok') {
    return { ok: false, reason: `health status is "${health.status}"`, health };
  }
  if (!health.ytdlp_available) {
    return { ok: false, reason: 'helper reports its downloader component is unavailable', health };
  }
  if (!health.ffmpeg_available) {
    return { ok: false, reason: 'helper reports its media converter is unavailable', health };
  }
  return { ok: true, reason: '', health };
}

export interface ProbeOptions {
  fetchImpl: typeof fetch;
  baseUrl: string;
  timeoutMs?: number;
}

export interface ProbeResult {
  state: Exclude<HelperState, 'checking'>;
  health: HelperHealth | null;
  reason: string;
}

/**
 * Performs one health check and classifies the outcome.
 *
 * A refused connection, an HTTP error and a timeout all mean the same thing to
 * a user — there is no Helper to talk to — so they collapse to `unavailable`.
 * `error` is reserved for the case where something did answer but the answer
 * was not a health report, which is worth wording differently because the port
 * is occupied by something unexpected.
 */
export async function probeHelper(opts: ProbeOptions): Promise<ProbeResult> {
  const timeoutMs = opts.timeoutMs ?? DEFAULT_HEALTH_TIMEOUT_MS;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  try {
    const res = await opts.fetchImpl(`${opts.baseUrl}/health`, {
      method: 'GET',
      signal: controller.signal,
      // Without this a browser can keep answering 200 from cache after the
      // Helper has quit, which would strand the indicator on "ready".
      cache: 'no-store'
    });

    if (!res.ok) {
      return {
        state: 'unavailable',
        health: null,
        reason: `health check returned HTTP ${res.status}`
      };
    }

    let body: unknown;
    try {
      body = await res.json();
    } catch (err) {
      return {
        state: 'error',
        health: null,
        reason: `health response was not valid JSON: ${errText(err)}`
      };
    }

    const verdict = parseHelperHealth(body);
    if (verdict.ok) {
      return { state: 'available', health: verdict.health, reason: '' };
    }
    return {
      state: verdict.health ? 'unavailable' : 'error',
      health: verdict.health,
      reason: verdict.reason
    };
  } catch (err) {
    if (controller.signal.aborted) {
      return {
        state: 'unavailable',
        health: null,
        reason: `health check timed out after ${timeoutMs}ms`
      };
    }
    return {
      state: 'unavailable',
      health: null,
      reason: `health check could not reach the helper: ${errText(err)}`
    };
  } finally {
    clearTimeout(timer);
  }
}

// --- The store --------------------------------------------------------------

export type HelperListener = (snapshot: HelperSnapshot) => void;

export interface HelperStore {
  /** The current snapshot. Never async, never stale-by-surprise: check `checkedAt`. */
  get(): HelperSnapshot;
  /** Subscribes and fires immediately with the current snapshot. Returns an unsubscribe. */
  subscribe(listener: HelperListener): () => void;
  /** Forces a health check. Concurrent callers share one in-flight request. */
  refresh(): Promise<HelperSnapshot>;
  /** Re-checks only when the snapshot is older than `freshMs`, or still 'checking'. */
  ensureFresh(): Promise<HelperSnapshot>;
  /** Begins background polling. Idempotent. */
  start(): void;
  /** Stops background polling. Idempotent. */
  stop(): void;
}

export interface HelperStoreOptions {
  fetchImpl: typeof fetch;
  baseUrl: string;
  timeoutMs?: number;
  pollMs?: number;
  freshMs?: number;
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (handle: unknown) => void;
}

export function createHelperStore(opts: HelperStoreOptions): HelperStore {
  const timeoutMs = opts.timeoutMs ?? DEFAULT_HEALTH_TIMEOUT_MS;
  const pollMs = opts.pollMs ?? DEFAULT_POLL_MS;
  const freshMs = opts.freshMs ?? DEFAULT_FRESH_MS;
  const now = opts.now ?? (() => Date.now());
  const setTimer =
    opts.setTimer ?? ((fn: () => void, ms: number) => setInterval(fn, ms) as unknown);
  const clearTimer =
    opts.clearTimer ?? ((h: unknown) => clearInterval(h as ReturnType<typeof setInterval>));

  let snapshot: HelperSnapshot = {
    state: 'checking',
    health: null,
    reason: '',
    checkedAt: 0
  };

  const listeners = new Set<HelperListener>();
  let inflight: Promise<HelperSnapshot> | null = null;
  let pollHandle: unknown = null;

  function emit() {
    // Snapshot the listener set: a listener that unsubscribes during the loop
    // must not disturb the ones after it.
    for (const listener of [...listeners]) {
      try {
        listener(snapshot);
      } catch (err) {
        console.error('[youpiper] helper state listener failed:', err);
      }
    }
  }

  function set(next: ProbeResult) {
    const changed = next.state !== snapshot.state;
    snapshot = {
      state: next.state,
      health: next.health,
      reason: next.reason,
      checkedAt: now()
    };
    if (changed) {
      // The state itself is the interesting log line; the reason explains it.
      console.info(
        `[youpiper] helper state: ${snapshot.state}${snapshot.reason ? ` (${snapshot.reason})` : ''}`
      );
    }
    emit();
  }

  function refresh(): Promise<HelperSnapshot> {
    if (inflight) return inflight;
    inflight = probeHelper({ fetchImpl: opts.fetchImpl, baseUrl: opts.baseUrl, timeoutMs })
      .then((result) => {
        set(result);
        return snapshot;
      })
      .finally(() => {
        inflight = null;
      });
    return inflight;
  }

  function ensureFresh(): Promise<HelperSnapshot> {
    if (snapshot.state !== 'checking' && now() - snapshot.checkedAt < freshMs) {
      return Promise.resolve(snapshot);
    }
    return refresh();
  }

  return {
    get: () => snapshot,

    subscribe(listener) {
      listeners.add(listener);
      listener(snapshot);
      return () => listeners.delete(listener);
    },

    refresh,
    ensureFresh,

    start() {
      void refresh();
      if (pollHandle === null) {
        pollHandle = setTimer(() => void refresh(), pollMs);
      }
    },

    stop() {
      if (pollHandle !== null) {
        clearTimer(pollHandle);
        pollHandle = null;
      }
    }
  };
}

// --- Format helpers ---------------------------------------------------------

export const SUPPORTED_VIDEO_HEIGHTS = [360, 480, 720, 1080];

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

/** Normalises the Helper's /metadata body into the shape the UI renders. */
export function normalizeLocalMetadata(data: any): VideoMetadata {
  const rawFormats: Array<{ quality: string; height: number }> = Array.isArray(data?.formats)
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
    id: data?.id || '',
    title: data?.title || '',
    thumbnail: data?.thumbnail || '',
    duration: data?.duration || 0,
    uploader: data?.uploader || '',
    formats
  };
}

/** Normalises the online backend's /api/analyze body into the same shape. */
export function normalizeOnlineMetadata(data: any): VideoMetadata {
  const heights: number[] = Array.isArray(data?.video_heights) ? data.video_heights : [];
  const formats: VideoFormat[] = heights
    .filter((h) => SUPPORTED_VIDEO_HEIGHTS.includes(h))
    .sort((a, b) => b - a)
    .map((h) => ({ quality: `${h}p`, label: QUALITY_LABELS[`${h}p`], height: h }));

  if (data?.audio_available) {
    formats.push({ quality: 'audio', label: QUALITY_LABELS.audio });
  }

  return {
    id: '',
    title: data?.title || '',
    thumbnail: data?.thumbnail || '',
    duration: data?.duration || 0,
    uploader: data?.uploader || '',
    formats
  };
}

// --- The routed API ---------------------------------------------------------

export interface ApiOptions {
  localBaseUrl: string;
  onlineBaseUrl: string;
  fetchImpl: typeof fetch;
  store: HelperStore;
  logger?: Pick<Console, 'info' | 'warn' | 'error'>;
}

export interface RoutedApi {
  analyze(url: string): Promise<AnalyzeResult>;
  download(url: string, quality: string): Promise<{ job_id: string; backend: Backend }>;
  getStatus(jobId: string, backend: Backend): Promise<DownloadJob>;
  cancelDownload(jobId: string, backend: Backend): Promise<{ job_id: string; status: string }>;
}

const JSON_HEADERS = { 'Content-Type': 'application/json' };

export function createApi(opts: ApiOptions): RoutedApi {
  const log = opts.logger ?? console;

  /**
   * The last URL the Helper was asked about and could not read.
   *
   * A Helper that just failed to analyze a video will almost certainly fail to
   * download the same one, and the format rows are already labelled for the
   * backend that answered. Remembering the one URL keeps the label and the
   * routing telling the same story, instead of promising "Online" and then
   * quietly retrying the Helper. Cleared as soon as the Helper manages that URL.
   */
  let helperBalkedOnUrl = '';

  async function analyzeLocal(url: string): Promise<VideoMetadata> {
    const res = await opts.fetchImpl(`${opts.localBaseUrl}/metadata`, {
      method: 'POST',
      headers: JSON_HEADERS,
      body: JSON.stringify({ url })
    });
    if (!res.ok) throw await readBackendError(res, 'local', GENERIC_ANALYZE_FAILURE);
    return normalizeLocalMetadata(await res.json());
  }

  async function analyzeOnline(url: string): Promise<VideoMetadata> {
    const res = await opts.fetchImpl(`${opts.onlineBaseUrl}/api/analyze`, {
      method: 'POST',
      headers: JSON_HEADERS,
      body: JSON.stringify({ url })
    });
    if (!res.ok) throw await readBackendError(res, 'online', GENERIC_ANALYZE_FAILURE);
    return normalizeOnlineMetadata(await res.json());
  }

  return {
    /**
     * Reads a video's formats from whichever backend can.
     *
     * The Helper is preferred when it is available, but a Helper that answers
     * /health can still fail to read a particular video, and it can quit
     * between the check and the request. Either way the user wanted formats,
     * not a diagnosis, so the online backend covers for it and the result says
     * so via `notice`. What we do not do is re-check health before every poll,
     * or route on a state measured minutes ago.
     */
    async analyze(url: string): Promise<AnalyzeResult> {
      const snapshot = await opts.store.ensureFresh();

      if (snapshot.state === 'available') {
        try {
          const meta = await analyzeLocal(url);
          if (helperBalkedOnUrl === url) helperBalkedOnUrl = '';
          return { ...meta, backend: 'local', fellBack: false, notice: '' };
        } catch (localErr) {
          const code = localErr instanceof BackendError ? localErr.code : '';
          const detail = localErr instanceof BackendError ? localErr.detail : errText(localErr);
          log.warn(
            `[youpiper] helper analyze failed (${code || 'no code'}): ${detail} — falling back to the online downloader`
          );
          helperBalkedOnUrl = url;

          // Distinguish "the Helper vanished" from "the Helper is up but could
          // not read this video". Both fall back; they read differently to the
          // user, and claiming the Helper is gone when it is running would be a
          // lie the indicator immediately contradicts.
          const after = await opts.store.refresh();
          const notice = after.state === 'available' ? NOTICE_HELPER_BALKED : NOTICE_HELPER_LOST;

          try {
            const meta = await analyzeOnline(url);
            return { ...meta, backend: 'online', fellBack: true, notice };
          } catch (onlineErr) {
            log.error('[youpiper] online analyze also failed:', onlineErr);
            // Report the local failure: it is the backend the user was on, and
            // its code is the more specific of the two.
            throw localErr instanceof BackendError
              ? localErr
              : new BackendError(GENERIC_ANALYZE_FAILURE, code, detail, 'local');
          }
        }
      }

      const meta = await analyzeOnline(url);
      return { ...meta, backend: 'online', fellBack: false, notice: '' };
    },

    /**
     * Starts a download job on whichever backend can take it.
     *
     * Falling back here is safe because a failed start means nothing has been
     * downloaded yet. Once a local job is running, a lost Helper is reported
     * rather than papered over — see the polling in Downloader.astro.
     */
    async download(url: string, quality: string) {
      const snapshot = await opts.store.ensureFresh();

      if (snapshot.state === 'available' && helperBalkedOnUrl !== url) {
        try {
          const res = await opts.fetchImpl(`${opts.localBaseUrl}/downloads`, {
            method: 'POST',
            headers: JSON_HEADERS,
            body: JSON.stringify({ url, quality })
          });
          if (!res.ok) throw await readBackendError(res, 'local', GENERIC_DOWNLOAD_FAILURE);
          const data = await res.json();
          return { job_id: data.job_id as string, backend: 'local' as const };
        } catch (localErr) {
          log.warn(
            '[youpiper] helper download start failed, falling back to the online downloader:',
            localErr
          );
          void opts.store.refresh();
        }
      }

      const body =
        quality === 'audio' ? { url, format: 'mp3' } : { url, quality, format: 'mp4' };
      const res = await opts.fetchImpl(`${opts.onlineBaseUrl}/api/download`, {
        method: 'POST',
        headers: JSON_HEADERS,
        body: JSON.stringify(body)
      });
      if (!res.ok) throw await readBackendError(res, 'online', GENERIC_DOWNLOAD_FAILURE);
      const data = await res.json();
      return { job_id: data.job_id as string, backend: 'online' as const };
    },

    /**
     * Polls one job. Deliberately does no health check: this runs several times
     * a second, and the job already knows which backend owns it.
     */
    async getStatus(jobId: string, backend: Backend): Promise<DownloadJob> {
      if (backend === 'local') {
        const res = await opts.fetchImpl(`${opts.localBaseUrl}/downloads/${jobId}`, {
          method: 'GET'
        });
        if (!res.ok) throw await readBackendError(res, 'local', GENERIC_DOWNLOAD_FAILURE);
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

      const res = await opts.fetchImpl(`${opts.onlineBaseUrl}/api/status/${jobId}`, {
        method: 'GET'
      });
      if (!res.ok) throw await readBackendError(res, 'online', GENERIC_DOWNLOAD_FAILURE);
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
    },

    async cancelDownload(jobId: string, backend: Backend) {
      if (backend === 'local') {
        const res = await opts.fetchImpl(`${opts.localBaseUrl}/downloads/${jobId}/cancel`, {
          method: 'POST'
        });
        if (!res.ok) throw await readBackendError(res, 'local', GENERIC_DOWNLOAD_FAILURE);
        return await res.json();
      }
      return { job_id: jobId, status: 'cancelled' };
    }
  };
}
