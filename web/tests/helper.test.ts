// Helper availability and backend routing tests.
//
// Run with `node --test` (see ../../test.sh). Node strips the TypeScript types
// natively, so this needs no test framework, no bundler and no dependencies.
//
// Everything under test takes its fetch and its timers as arguments, so these
// run with no browser, no network, and no Helper installed.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  createApi,
  createHelperStore,
  parseHelperHealth,
  probeHelper,
  readBackendError,
  friendlyMessage,
  friendlyJobError,
  BackendError,
  NOTICE_HELPER_BALKED,
  NOTICE_HELPER_LOST,
  type HelperState
} from '../src/lib/helper.ts';

const LOCAL = 'http://127.0.0.1:47821';
const ONLINE = 'http://127.0.0.1:5001';

const HEALTHY = {
  status: 'ok',
  version: '0.1.0',
  ytdlp_available: true,
  ffmpeg_available: true
};

const LOCAL_METADATA = {
  id: 'jNQXAC9IVRw',
  title: 'Me at the zoo',
  thumbnail: 'https://example.test/thumb.jpg',
  duration: 19,
  uploader: 'jawed',
  formats: [
    { quality: '1080p', height: 1080 },
    { quality: '480p', height: 480 },
    { quality: '144p', height: 144 }
  ]
};

const ONLINE_METADATA = {
  title: 'Me at the zoo',
  thumbnail: 'https://example.test/thumb.jpg',
  duration: 19,
  uploader: 'jawed',
  video_heights: [1080, 480, 144],
  audio_available: true
};

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' }
  });
}

/**
 * A fetch stand-in driven by a router.
 *
 * Returning undefined from the router means nothing is listening there, which
 * is what a refused loopback connection looks like to fetch: a rejection, not a
 * response.
 */
type Router = (path: string, init: RequestInit) => Response | Promise<Response> | undefined;

function stubFetch(router: Router) {
  const calls: string[] = [];
  const fetchImpl = (async (input: any, init: RequestInit = {}) => {
    const url = String(input);
    const path = url.startsWith(LOCAL)
      ? `local:${url.slice(LOCAL.length)}`
      : url.startsWith(ONLINE)
        ? `online:${url.slice(ONLINE.length)}`
        : url;
    calls.push(path);
    const res = await router(path, init);
    if (res === undefined) throw new TypeError(`Failed to fetch ${url}`);
    return res;
  }) as unknown as typeof fetch;
  return { fetchImpl, calls };
}

/** A store with hand-driven timers and clock, so transitions are deterministic. */
function storeWith(fetchImpl: typeof fetch, opts: { timeoutMs?: number; freshMs?: number } = {}) {
  let clock = 1_000;
  let tick: (() => void) | null = null;
  const store = createHelperStore({
    fetchImpl,
    baseUrl: LOCAL,
    timeoutMs: opts.timeoutMs ?? 50,
    freshMs: opts.freshMs ?? 0,
    now: () => clock,
    setTimer: (fn) => {
      tick = fn;
      return 1;
    },
    clearTimer: () => {
      tick = null;
    }
  });
  return {
    store,
    advance: (ms: number) => {
      clock += ms;
    },
    /** Fires the polling callback the way the interval would, and settles it. */
    poll: async () => {
      assert.ok(tick, 'expected the store to have registered a poll timer');
      tick!();
      await store.refresh();
    }
  };
}

const silent = { info: () => {}, warn: () => {}, error: () => {} };

// ---------------------------------------------------------------------------
// HELPER-001 .. HELPER-006, HELPER-012 — health parsing and probing
// ---------------------------------------------------------------------------

test('HELPER-001: a healthy /health response yields state available', async () => {
  const { fetchImpl } = stubFetch((path) => (path === 'local:/health' ? json(HEALTHY) : undefined));
  const result = await probeHelper({ fetchImpl, baseUrl: LOCAL, timeoutMs: 50 });
  assert.equal(result.state, 'available');
  assert.deepEqual(result.health, HEALTHY);
});

test('HELPER-002: a refused connection yields state unavailable', async () => {
  const { fetchImpl } = stubFetch(() => undefined);
  const result = await probeHelper({ fetchImpl, baseUrl: LOCAL, timeoutMs: 50 });
  assert.equal(result.state, 'unavailable');
  assert.match(result.reason, /could not reach/);
});

test('HELPER-003: a malformed JSON body yields state error, never available', async () => {
  const { fetchImpl } = stubFetch(
    () => new Response('{"status": "ok"', { status: 200, headers: { 'Content-Type': 'application/json' } })
  );
  const result = await probeHelper({ fetchImpl, baseUrl: LOCAL, timeoutMs: 50 });
  assert.equal(result.state, 'error');
  assert.notEqual(result.state as HelperState, 'available');
});

test('HELPER-004: status other than "ok" yields unavailable', async () => {
  for (const status of ['degraded', 'starting', '']) {
    const { fetchImpl } = stubFetch(() => json({ ...HEALTHY, status }));
    const result = await probeHelper({ fetchImpl, baseUrl: LOCAL, timeoutMs: 50 });
    assert.equal(result.state, 'unavailable', `status "${status}" must not read as ready`);
  }
});

test('HELPER-005: ytdlp_available false yields unavailable', async () => {
  const { fetchImpl } = stubFetch(() => json({ ...HEALTHY, ytdlp_available: false }));
  const result = await probeHelper({ fetchImpl, baseUrl: LOCAL, timeoutMs: 50 });
  assert.equal(result.state, 'unavailable');
});

test('HELPER-006: ffmpeg_available false yields unavailable', async () => {
  const { fetchImpl } = stubFetch(() => json({ ...HEALTHY, ffmpeg_available: false }));
  const result = await probeHelper({ fetchImpl, baseUrl: LOCAL, timeoutMs: 50 });
  assert.equal(result.state, 'unavailable');
});

test('HELPER-012: a health request that never answers times out as unavailable', async () => {
  // No response, ever — the probe must be ended by its own deadline rather than
  // leaving the indicator on "checking" forever.
  const fetchImpl = ((_url: any, init: RequestInit = {}) =>
    new Promise((_resolve, reject) => {
      init.signal?.addEventListener('abort', () =>
        reject(Object.assign(new Error('Aborted'), { name: 'AbortError' }))
      );
    })) as unknown as typeof fetch;

  const started = Date.now();
  const result = await probeHelper({ fetchImpl, baseUrl: LOCAL, timeoutMs: 40 });
  assert.equal(result.state, 'unavailable');
  assert.match(result.reason, /timed out/);
  assert.ok(Date.now() - started < 2000, 'probe must not hang past its timeout');
});

test('an HTTP error status on /health is unavailable, not available', async () => {
  for (const status of [404, 500, 503]) {
    const { fetchImpl } = stubFetch(() => json({ error: 'nope' }, status));
    const result = await probeHelper({ fetchImpl, baseUrl: LOCAL, timeoutMs: 50 });
    assert.equal(result.state, 'unavailable');
  }
});

test('parseHelperHealth separates "not ready" from "not a health report"', () => {
  // Understood, but not fit for work: health present, ok false.
  const degraded = parseHelperHealth({ ...HEALTHY, status: 'degraded' });
  assert.equal(degraded.ok, false);
  assert.ok(degraded.health, 'a parseable body should still be reported');

  // Unintelligible: no health at all, which is what drives the 'error' state.
  for (const body of [null, 'ok', 42, [], {}, { version: '0.1.0' }]) {
    const verdict = parseHelperHealth(body);
    assert.equal(verdict.ok, false);
    assert.equal(verdict.health, null, `${JSON.stringify(body)} is not a health report`);
  }
});

// ---------------------------------------------------------------------------
// HELPER-007, HELPER-008 — state transitions without a page reload
// ---------------------------------------------------------------------------

test('HELPER-007: a Helper that starts after page load transitions unavailable to available', async () => {
  let helperUp = false;
  const { fetchImpl } = stubFetch((path) =>
    path === 'local:/health' && helperUp ? json(HEALTHY) : undefined
  );
  const { store, poll } = storeWith(fetchImpl);

  const seen: HelperState[] = [];
  store.subscribe((snapshot) => seen.push(snapshot.state));

  assert.equal(store.get().state, 'checking', 'starts out as checking');
  store.start();
  await store.refresh();
  assert.equal(store.get().state, 'unavailable');

  helperUp = true;
  await poll();

  assert.equal(store.get().state, 'available');
  assert.deepEqual(seen, ['checking', 'unavailable', 'available']);
  store.stop();
});

test('HELPER-008: a Helper that stops after page load transitions available to unavailable', async () => {
  let helperUp = true;
  const { fetchImpl } = stubFetch((path) =>
    path === 'local:/health' && helperUp ? json(HEALTHY) : undefined
  );
  const { store, poll } = storeWith(fetchImpl);
  store.start();
  await store.refresh();
  assert.equal(store.get().state, 'available');

  helperUp = false;
  await poll();
  assert.equal(store.get().state, 'unavailable');
  store.stop();
});

test('subscribers are notified immediately and on every change', async () => {
  const { fetchImpl } = stubFetch(() => json(HEALTHY));
  const { store } = storeWith(fetchImpl);

  const seen: HelperState[] = [];
  const unsubscribe = store.subscribe((s) => seen.push(s.state));
  assert.deepEqual(seen, ['checking'], 'fires once on subscribe');

  await store.refresh();
  assert.deepEqual(seen, ['checking', 'available']);

  unsubscribe();
  await store.refresh();
  assert.deepEqual(seen, ['checking', 'available'], 'stops after unsubscribe');
});

test('concurrent refreshes share one health request', async () => {
  const { fetchImpl, calls } = stubFetch(() => json(HEALTHY));
  const { store } = storeWith(fetchImpl);
  await Promise.all([store.refresh(), store.refresh(), store.refresh()]);
  assert.equal(calls.filter((c) => c === 'local:/health').length, 1);
});

test('ensureFresh reuses a recent result and re-checks a stale one', async () => {
  const { fetchImpl, calls } = stubFetch(() => json(HEALTHY));
  const { store, advance } = storeWith(fetchImpl, { freshMs: 4000 });

  await store.ensureFresh();
  await store.ensureFresh();
  assert.equal(calls.length, 1, 'a result measured moments ago is reused');

  advance(5000);
  await store.ensureFresh();
  assert.equal(calls.length, 2, 'a stale result is re-checked before it is used');
});

// ---------------------------------------------------------------------------
// HELPER-009, HELPER-010, HELPER-011 — analyze routing and fallback
// ---------------------------------------------------------------------------

function apiWith(router: Router, opts: { freshMs?: number } = {}) {
  const { fetchImpl, calls } = stubFetch(router);
  const { store } = storeWith(fetchImpl, { freshMs: opts.freshMs ?? 0 });
  const api = createApi({
    localBaseUrl: LOCAL,
    onlineBaseUrl: ONLINE,
    fetchImpl,
    store,
    logger: silent
  });
  return { api, store, calls };
}

test('HELPER-009: with the Helper available, analyze uses the local /metadata endpoint', async () => {
  const { api, calls } = apiWith((path) => {
    if (path === 'local:/health') return json(HEALTHY);
    if (path === 'local:/metadata') return json(LOCAL_METADATA);
    return undefined;
  });

  const result = await api.analyze('https://www.youtube.com/watch?v=jNQXAC9IVRw');

  assert.equal(result.backend, 'local');
  assert.equal(result.fellBack, false);
  assert.equal(result.notice, '');
  assert.equal(result.title, 'Me at the zoo');
  assert.ok(calls.includes('local:/metadata'));
  assert.ok(!calls.includes('online:/api/analyze'), 'must not also ask the online backend');
  // Unsupported heights are dropped; audio is always offered locally.
  assert.deepEqual(
    result.formats.map((f) => f.quality),
    ['1080p', '480p', 'audio']
  );
});

test('HELPER-010: with no Helper, analyze uses the online /api/analyze endpoint', async () => {
  const { api, calls } = apiWith((path) => {
    if (path === 'online:/api/analyze') return json(ONLINE_METADATA);
    return undefined; // nothing is listening on the Helper port
  });

  const result = await api.analyze('https://www.youtube.com/watch?v=jNQXAC9IVRw');

  assert.equal(result.backend, 'online');
  assert.equal(result.fellBack, false);
  assert.ok(calls.includes('online:/api/analyze'));
  assert.ok(!calls.includes('local:/metadata'), 'must not attempt the local backend');
});

test('HELPER-011: a healthy Helper that fails to analyze falls back to the online downloader', async () => {
  // This is the reported bug: /health says ok, /metadata answers 500
  // metadata_failed. The user still gets their formats.
  const { api, calls } = apiWith((path) => {
    if (path === 'local:/health') return json(HEALTHY);
    if (path === 'local:/metadata') {
      return json({ error: 'metadata_failed', details: 'metadata_failed: exit status 1' }, 500);
    }
    if (path === 'online:/api/analyze') return json(ONLINE_METADATA);
    return undefined;
  });

  const result = await api.analyze('https://www.youtube.com/watch?v=jNQXAC9IVRw');

  assert.equal(result.backend, 'online');
  assert.equal(result.fellBack, true);
  assert.equal(result.title, 'Me at the zoo');
  // The Helper is still up, so the notice must not claim it disappeared.
  assert.equal(result.notice, NOTICE_HELPER_BALKED);
  assert.ok(calls.includes('local:/metadata'));
  assert.ok(calls.includes('online:/api/analyze'));
});

test('a Helper that quits between the health check and the request reads as lost', async () => {
  let helperUp = true;
  const { api } = apiWith((path) => {
    if (path === 'local:/health') return helperUp ? json(HEALTHY) : undefined;
    if (path === 'local:/metadata') {
      helperUp = false; // it goes away mid-request
      return undefined;
    }
    if (path === 'online:/api/analyze') return json(ONLINE_METADATA);
    return undefined;
  });

  const result = await api.analyze('https://www.youtube.com/watch?v=jNQXAC9IVRw');
  assert.equal(result.backend, 'online');
  assert.equal(result.fellBack, true);
  assert.equal(result.notice, NOTICE_HELPER_LOST);
});

test('when both backends fail, the error is a sentence and never a raw error code', async () => {
  const { api } = apiWith((path) => {
    if (path === 'local:/health') return json(HEALTHY);
    if (path === 'local:/metadata') {
      return json({ error: 'metadata_failed', details: 'metadata_failed: exit status 1' }, 500);
    }
    if (path === 'online:/api/analyze') return json({ error: 'analyze_failed' }, 500);
    return undefined;
  });

  await assert.rejects(
    () => api.analyze('https://www.youtube.com/watch?v=jNQXAC9IVRw'),
    (err: unknown) => {
      assert.ok(err instanceof BackendError);
      assert.equal(err.message, "We couldn't analyze this video.");
      assert.doesNotMatch(err.message, /metadata_failed|exit status|HTTP \d+/);
      // The machine detail survives for the console, just not for the user.
      assert.equal(err.code, 'metadata_failed');
      assert.match(err.detail, /exit status 1/);
      return true;
    }
  );
});

test('a stale available state cannot route a request to a Helper that is gone', async () => {
  let helperUp = true;
  const { api, store } = apiWith(
    (path) => {
      if (path === 'local:/health') return helperUp ? json(HEALTHY) : undefined;
      if (path === 'local:/metadata') return json(LOCAL_METADATA);
      if (path === 'online:/api/analyze') return json(ONLINE_METADATA);
      return undefined;
    },
    { freshMs: 0 }
  );

  await store.refresh();
  assert.equal(store.get().state, 'available');

  helperUp = false;
  const result = await api.analyze('https://www.youtube.com/watch?v=jNQXAC9IVRw');
  assert.equal(result.backend, 'online', 'routing re-checked instead of trusting stale state');
  assert.equal(store.get().state, 'unavailable');
});

// ---------------------------------------------------------------------------
// Backend selection for downloads and polling
// ---------------------------------------------------------------------------

test('download starts locally when the Helper is available', async () => {
  const { api, calls } = apiWith((path) => {
    if (path === 'local:/health') return json(HEALTHY);
    if (path === 'local:/downloads') return json({ job_id: 'job-local-1' });
    return undefined;
  });

  const job = await api.download('https://www.youtube.com/watch?v=jNQXAC9IVRw', '480p');
  assert.deepEqual(job, { job_id: 'job-local-1', backend: 'local' });
  assert.ok(!calls.includes('online:/api/download'));
});

test('download starts online when the Helper is unavailable', async () => {
  const { api, calls } = apiWith((path) => {
    if (path === 'online:/api/download') return json({ job_id: 'job-online-1' });
    return undefined;
  });

  const job = await api.download('https://www.youtube.com/watch?v=jNQXAC9IVRw', '480p');
  assert.deepEqual(job, { job_id: 'job-online-1', backend: 'online' });
  assert.ok(!calls.includes('local:/downloads'));
});

test('a local download that cannot start falls back to online', async () => {
  const { api } = apiWith((path) => {
    if (path === 'local:/health') return json(HEALTHY);
    if (path === 'local:/downloads') return json({ error: 'download_failed' }, 500);
    if (path === 'online:/api/download') return json({ job_id: 'job-online-2' });
    return undefined;
  });

  const job = await api.download('https://www.youtube.com/watch?v=jNQXAC9IVRw', 'audio');
  assert.equal(job.backend, 'online');
});

test('after analyze falls back, the download of that video goes online too', async () => {
  // The rows are labelled "Online" once analyze falls back. Retrying the Helper
  // for the same video would contradict that label and fail the same way.
  const url = 'https://www.youtube.com/watch?v=jNQXAC9IVRw';
  let localMetadataWorks = false;
  const { api, calls } = apiWith((path) => {
    if (path === 'local:/health') return json(HEALTHY);
    if (path === 'local:/metadata') {
      return localMetadataWorks ? json(LOCAL_METADATA) : json({ error: 'metadata_failed' }, 500);
    }
    if (path === 'local:/downloads') return json({ job_id: 'job-local-should-not-happen' });
    if (path === 'online:/api/analyze') return json(ONLINE_METADATA);
    if (path === 'online:/api/download') return json({ job_id: 'job-online-3' });
    return undefined;
  });

  const analyzed = await api.analyze(url);
  assert.equal(analyzed.backend, 'online');

  const job = await api.download(url, '480p');
  assert.equal(job.backend, 'online');
  assert.ok(!calls.includes('local:/downloads'), 'must not retry the Helper for this video');

  // A different video is still offered to the Helper: the memo is per URL.
  const other = await api.download('https://www.youtube.com/watch?v=XF2WniCfmEE', '480p');
  assert.equal(other.backend, 'local');

  // And once the Helper can read that video again, it is trusted with it again.
  localMetadataWorks = true;
  const reanalyzed = await api.analyze(url);
  assert.equal(reanalyzed.backend, 'local');
  const retried = await api.download(url, '480p');
  assert.equal(retried.backend, 'local');
});

test('polling a job performs no health request', async () => {
  const { api, calls } = apiWith((path) => {
    if (path === 'local:/health') return json(HEALTHY);
    if (path === 'local:/downloads/job-1') {
      return json({ job_id: 'job-1', status: 'downloading', progress: 42, speed: 1024, eta: 8 });
    }
    return undefined;
  });

  for (let i = 0; i < 5; i++) await api.getStatus('job-1', 'local');

  assert.equal(
    calls.filter((c) => c === 'local:/health').length,
    0,
    'progress polling must not re-probe health on every tick'
  );
  assert.equal(calls.filter((c) => c === 'local:/downloads/job-1').length, 5);
});

test('a failing poll rejects so the caller can report it, rather than reporting success', async () => {
  const { api } = apiWith((path) => (path === 'local:/health' ? json(HEALTHY) : undefined));
  await assert.rejects(() => api.getStatus('job-1', 'local'));
});

// ---------------------------------------------------------------------------
// Error message translation
// ---------------------------------------------------------------------------

test('backend error codes are translated for people', async () => {
  const cases: Array<[string, string]> = [
    ['metadata_failed', "We couldn't analyze this video."],
    ['invalid_url', 'That does not look like a video link. Please check it and try again.'],
    ['job_not_found', 'That download is no longer available. Please start it again.']
  ];
  for (const [code, expected] of cases) {
    const err = await readBackendError(json({ error: code }, 500), 'local', 'fallback sentence');
    assert.equal(err.message, expected);
    assert.equal(err.code, code);
  }
});

test('an unknown error code falls back to a readable sentence', async () => {
  const err = await readBackendError(
    json({ error: 'something_new_and_unmapped' }, 500),
    'local',
    'We could not do that. Please try again.'
  );
  assert.equal(err.message, 'We could not do that. Please try again.');
  assert.equal(err.code, 'something_new_and_unmapped');
  assert.equal(friendlyMessage('something_new_and_unmapped', 'x'), 'x');
});

test('a non-JSON error body still yields a readable sentence', async () => {
  const err = await readBackendError(
    new Response('<html>502 Bad Gateway</html>', { status: 502 }),
    'online',
    'The online downloader is not responding. Please try again shortly.'
  );
  assert.equal(err.message, 'The online downloader is not responding. Please try again shortly.');
  assert.equal(err.detail, 'HTTP 502');
});

test('a failed job error string is never shown to the user verbatim', () => {
  // The job carries "<code>: <detail>"; the detail is console material.
  for (const raw of [
    'download_failed: exit status 1',
    'metadata_failed: exit status 1',
    'download_failed',
    undefined,
    ''
  ]) {
    const message = friendlyJobError(raw, 'local');
    assert.doesNotMatch(message, /_failed|exit status|HTTP \d+/, `leaked internals for "${raw}"`);
    assert.match(message, /YouPiper Helper/);
  }

  assert.doesNotMatch(friendlyJobError('download_failed: boom', 'online'), /YouPiper Helper/);

  // A code with a mapping still uses it.
  assert.equal(
    friendlyJobError('invalid_url: missing host', 'local'),
    'That does not look like a video link. Please check it and try again.'
  );
});

// ---------------------------------------------------------------------------
// Helper ON/OFF Control Tests
// ---------------------------------------------------------------------------

test('HELPER-STATUS-001: Website correctly detects running Helper', async () => {
  const { store } = storeWith(stubFetch((path) => (path === 'local:/health' ? json(HEALTHY) : undefined)).fetchImpl);
  const snap = await store.refresh();
  assert.equal(snap.state, 'available');
});

test('HELPER-STATUS-002: Website correctly handles Helper being OFF', async () => {
  const { store } = storeWith(stubFetch(() => undefined).fetchImpl);
  const snap = await store.refresh();
  assert.equal(snap.state, 'unavailable');
});

test('HELPER-OFF-001 & HELPER-OFF-002: Helper can be disabled and stops when disabled', async () => {
  let isRunning = true;
  const { store } = storeWith(
    stubFetch((path, init) => {
      if (path === 'local:/health') return isRunning ? json(HEALTHY) : undefined;
      if (path.startsWith('local:/off') && init.method === 'POST') {
        isRunning = false;
        return json({ status: 'off', message: 'YouPiper Helper turned off' });
      }
      return undefined;
    }).fetchImpl
  );

  const initSnap = await store.refresh();
  assert.equal(initSnap.state, 'available');

  await store.turnOff();
  assert.equal(store.get().state, 'unavailable');

  const afterSnap = await store.refresh();
  assert.equal(afterSnap.state, 'unavailable');
});

test('HELPER-OFF-001 conflict: Helper turnOff fails when active download exists unless forced', async () => {
  const { store } = storeWith(
    stubFetch((path, init) => {
      if (path.startsWith('local:/off') && init.method === 'POST') {
        if (!path.includes('force=true')) {
          return json({ error: 'active_job', details: 'An active download is in progress' }, 409);
        }
        return json({ status: 'off' });
      }
      return undefined;
    }).fetchImpl
  );

  await assert.rejects(
    () => store.turnOff(false),
    (err: any) => err instanceof BackendError && err.code === 'active_job'
  );

  const res = await store.turnOff(true);
  assert.equal(res, true);
  assert.equal(store.get().state, 'unavailable');
});

test('HELPER-FALLBACK-001: Online download still works when Helper is OFF', async () => {
  const { api } = apiWith((path) => {
    if (path === 'local:/health') return undefined; // OFF
    if (path === 'online:/api/analyze') return json(ONLINE_METADATA);
    if (path === 'online:/api/download') return json({ job_id: 'online-job-1' });
    return undefined;
  });

  const meta = await api.analyze('https://www.youtube.com/watch?v=jNQXAC9IVRw');
  assert.equal(meta.backend, 'online');
  const job = await api.download('https://www.youtube.com/watch?v=jNQXAC9IVRw', '1080p');
  assert.equal(job.backend, 'online');
});

test('HELPER-DOWNLOAD-001: Local download still works when Helper is ON', async () => {
  const { api } = apiWith((path) => {
    if (path === 'local:/health') return json(HEALTHY);
    if (path === 'local:/metadata') return json(LOCAL_METADATA);
    if (path === 'local:/downloads') return json({ job_id: 'local-job-1' });
    return undefined;
  });

  const meta = await api.analyze('https://www.youtube.com/watch?v=jNQXAC9IVRw');
  assert.equal(meta.backend, 'local');
  const job = await api.download('https://www.youtube.com/watch?v=jNQXAC9IVRw', '1080p');
  assert.equal(job.backend, 'local');
});
