// Unified API layer for YouPiper web.
//
// The UI thinks in domain terms: analyze(), download(), getStatus(),
// cancelDownload(), getFile(). Requests route to the YouPiper Helper on this
// computer when it is available and to the online downloader otherwise, and
// fall back from the first to the second rather than failing in front of the
// user.
//
// This module is the facade: it reads configuration, owns the one shared Helper
// state store, and exposes the domain functions. The state machine, the health
// rules and the routing live in ./helper, which takes its fetch and timers as
// arguments so all of it can be tested without a browser.

import {
  createApi,
  createHelperStore,
  parseHelperHealth,
  type HelperHealth,
  type HelperState
} from './helper';

const LOCAL_AGENT_URL = 'http://127.0.0.1:47821';
const ONLINE_URL =
  ((import.meta as any).env?.PUBLIC_ONLINE_URL as string | undefined) ??
  'https://youpiper-api.onrender.com';

export {
  HELPER_COPY,
  MESSAGE_HELPER_CONNECTION_LOST,
  QUALITY_LABELS,
  QUALITY_SHORT_LABELS,
  getResolutionText,
  friendlyJobError,
  BackendError,
  type AnalyzeResult,
  type Backend,
  type DownloadJob,
  type HelperHealth,
  type HelperSnapshot,
  type HelperState,
  type VideoFormat,
  type VideoMetadata
} from './helper';

/**
 * The single source of truth for Helper availability.
 *
 * Components subscribe to this; none of them run their own health check. Call
 * `start()` once per page to begin background polling.
 */
export const helperStore = createHelperStore({
  fetchImpl: (...args) => fetch(...args),
  baseUrl: LOCAL_AGENT_URL
});

const api = createApi({
  localBaseUrl: LOCAL_AGENT_URL,
  onlineBaseUrl: ONLINE_URL,
  fetchImpl: (...args) => fetch(...args),
  store: helperStore
});

// --- Domain API -------------------------------------------------------------

export const analyze = api.analyze;
export const download = api.download;
export const getStatus = api.getStatus;
export const cancelDownload = api.cancelDownload;

export const turnOffHelper = (force?: boolean) => helperStore.turnOff(force);
export const turnOnHelper = () => helperStore.turnOn();

// --- Helper state accessors -------------------------------------------------

/** Current Helper state without touching the network. */
export function getHelperState(): HelperState {
  return helperStore.get().state;
}

/** True only when the Helper is present and reports every component working. */
export async function isHelperAvailable(): Promise<boolean> {
  const snapshot = await helperStore.ensureFresh();
  return snapshot.state === 'available';
}

/**
 * The raw health body, for the Helper landing page's diagnostics.
 *
 * Returns null when the Helper is unreachable or reports itself unfit, so a
 * caller cannot mistake a `degraded` Helper for a working one.
 */
export async function checkAgent(): Promise<HelperHealth | null> {
  try {
    const res = await fetch(`${LOCAL_AGENT_URL}/health`, { method: 'GET', cache: 'no-store' });
    if (!res.ok) return null;
    const verdict = parseHelperHealth(await res.json());
    return verdict.ok ? verdict.health : null;
  } catch {
    return null;
  }
}

// --- Browser-only file delivery ---------------------------------------------

/**
 * Pulls a finished online job's file through the browser. Local jobs never come
 * here: the Helper has already written the file to the user's Downloads folder.
 */
export async function getFile(jobId: string): Promise<boolean> {
  const url = `${ONLINE_URL}/api/file/${jobId}`;
  try {
    const res = await fetch(url, { method: 'GET' });
    if (!res.ok) {
      throw new Error(`file request failed with HTTP ${res.status}`);
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
  } catch (err) {
    console.error('[youpiper] file download failed:', err);
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
