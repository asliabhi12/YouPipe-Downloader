#!/usr/bin/env python3
"""
Live browser verification of the Helper availability indicator and analyze routing.

This is a real-environment check, not part of ./test.sh: it drives Chromium
against a running Astro dev server, starts and stops the *packaged* Helper at
/Applications/YouPiper Helper.app through its LaunchAgent, and talks to YouTube.
None of that can pass in CI, which is why it lives outside the automated suite.

Prerequisites:
    cd web    && npm run dev          # http://localhost:4321
    cd server && python3 app.py       # http://127.0.0.1:5001
    /Applications/YouPiper Helper.app installed and registered at login

Usage:
    python3 web/tests/browser/helper_indicator.py [--headed] [--url YOUTUBE_URL]

The Helper is restored to running before the script exits, whatever happens.
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

from playwright.sync_api import sync_playwright

SITE = "http://localhost:4321/"
HELPER_HEALTH = "http://127.0.0.1:47821/health"
LABEL = "com.youpiper.helper"
PLIST = os.path.expanduser(f"~/Library/LaunchAgents/{LABEL}.plist")
DOMAIN = f"gui/{os.getuid()}"

results: list[tuple[str, bool, str]] = []


def record(name: str, ok: bool, detail: str = "") -> None:
    results.append((name, ok, detail))
    print(f"  {'PASS' if ok else 'FAIL'}  {name}" + (f"  — {detail}" if detail else ""), flush=True)


# --- Helper process control -------------------------------------------------


def helper_up(timeout: float = 1.5) -> bool:
    try:
        with urllib.request.urlopen(HELPER_HEALTH, timeout=timeout) as res:
            return res.status == 200
    except (urllib.error.URLError, OSError, TimeoutError):
        return False


def wait_for_helper(want_up: bool, timeout: float = 20.0) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if helper_up() == want_up:
            return True
        time.sleep(0.5)
    return False


def helper_stop() -> None:
    subprocess.run(["launchctl", "bootout", f"{DOMAIN}/{LABEL}"], capture_output=True)
    wait_for_helper(False)


def helper_start() -> None:
    subprocess.run(["launchctl", "bootstrap", DOMAIN, PLIST], capture_output=True)
    if not wait_for_helper(True):
        # bootstrap is a no-op when the job is already loaded; kickstart forces it.
        subprocess.run(["launchctl", "kickstart", f"{DOMAIN}/{LABEL}"], capture_output=True)
        wait_for_helper(True)


# --- Page helpers -----------------------------------------------------------


def indicator(page):
    el = page.locator("#helper-indicator")
    return el.get_attribute("data-state"), el.locator(".helper-title").inner_text()


def wait_for_state(page, wanted: set[str], timeout_ms: int = 30_000) -> str:
    """Waits for the indicator to settle on one of `wanted` states."""
    expr = "s => wanted.includes(document.getElementById('helper-indicator')?.dataset.state)"
    page.wait_for_function(
        "([wanted]) => wanted.includes(document.getElementById('helper-indicator')?.dataset.state)",
        arg=[sorted(wanted)],
        timeout=timeout_ms,
    )
    return indicator(page)[0]


def new_page(browser):
    """A fresh page that records every backend request it makes."""
    page = browser.new_page()
    calls: list[str] = []
    page.on("request", lambda r: calls.append(f"{r.method} {r.url}"))
    page.on("console", lambda m: None)
    return page, calls


def called(calls: list[str], needle: str) -> bool:
    return any(needle in c for c in calls)


# --- Tests ------------------------------------------------------------------


def test_a_helper_running_before_load(browser):
    helper_start()
    page, _ = new_page(browser)
    page.goto(SITE, wait_until="domcontentloaded")
    state = wait_for_state(page, {"available", "unavailable", "error"})
    _, title = indicator(page)
    record(
        "A. Helper running before page load shows ready",
        state == "available" and "ready" in title.lower(),
        f"state={state!r} title={title!r}",
    )
    page.close()


def test_b_helper_stopped_before_load(browser):
    helper_stop()
    page, _ = new_page(browser)
    page.goto(SITE, wait_until="domcontentloaded")
    state = wait_for_state(page, {"available", "unavailable", "error"})
    _, title = indicator(page)
    cta_visible = page.locator("#helper-indicator-cta").is_visible()
    # Part 6: it must not claim the Helper is not installed.
    wording_ok = "not running" in title.lower() or "not responding" in title.lower()
    record(
        "B. Helper stopped before page load shows unavailable (not 'not installed')",
        state in {"unavailable", "error"} and wording_ok and cta_visible,
        f"state={state!r} title={title!r} cta_visible={cta_visible}",
    )
    page.close()


def test_c_helper_starts_while_open(browser):
    helper_stop()
    page, _ = new_page(browser)
    page.goto(SITE, wait_until="domcontentloaded")
    wait_for_state(page, {"unavailable", "error"})
    navigations = []
    page.on("framenavigated", lambda f: navigations.append(f.url))

    helper_start()
    try:
        state = wait_for_state(page, {"available"}, timeout_ms=40_000)
        ok = state == "available"
    except Exception as err:  # noqa: BLE001 - report, don't abort the run
        ok = False
        state = f"timeout: {type(err).__name__}"
    record(
        "C. Helper started while page open flips to ready without a reload",
        ok and not navigations,
        f"state={state!r} navigations={len(navigations)}",
    )
    page.close()


def test_d_helper_stops_while_open(browser):
    helper_start()
    page, _ = new_page(browser)
    page.goto(SITE, wait_until="domcontentloaded")
    wait_for_state(page, {"available"})
    navigations = []
    page.on("framenavigated", lambda f: navigations.append(f.url))

    helper_stop()
    try:
        state = wait_for_state(page, {"unavailable", "error"}, timeout_ms=40_000)
        ok = state in {"unavailable", "error"}
    except Exception as err:  # noqa: BLE001
        ok = False
        state = f"timeout: {type(err).__name__}"
    record(
        "D. Helper stopped while page open flips to unavailable without a reload",
        ok and not navigations,
        f"state={state!r} navigations={len(navigations)}",
    )
    page.close()


def analyze(page, url: str, timeout_ms: int = 120_000):
    page.fill("#url-input", url)
    page.click("#btn-analyze")
    page.wait_for_function(
        """() => {
            const results = document.getElementById('analysis-results');
            const error = document.getElementById('downloader-error');
            const shownResults = results && !results.classList.contains('hidden');
            const shownError = error && !error.classList.contains('hidden');
            return shownResults || shownError;
        }""",
        timeout=timeout_ms,
    )
    notice = page.locator("#downloader-notice")
    error = page.locator("#downloader-error")
    return {
        "title": page.locator(".video-title").inner_text() if page.locator(".video-title").count() else "",
        "notice": notice.inner_text() if notice.is_visible() else "",
        "error": error.inner_text() if error.is_visible() else "",
        "rows": page.locator(".format-card").count(),
        "hints": page.locator(".backend-hint").all_inner_texts(),
    }


def test_e_analyze_with_helper(browser, url: str):
    helper_start()
    page, calls = new_page(browser)
    page.goto(SITE, wait_until="domcontentloaded")
    wait_for_state(page, {"available"})
    out = analyze(page, url)

    hit_local = called(calls, "127.0.0.1:47821/metadata")
    record(
        "E. Helper running: analyze goes to the Helper's /metadata first",
        hit_local,
        f"local_metadata={hit_local} online_analyze={called(calls, '5001/api/analyze')}",
    )
    record(
        "E2. Helper running: the user still gets formats (fallback covers a failing Helper)",
        out["rows"] > 0 and not out["error"],
        f"rows={out['rows']} title={out['title']!r} notice={out['notice'][:70]!r} error={out['error'][:70]!r}",
    )
    # If it fell back, the notice must explain it and the rows must say Online.
    if called(calls, "5001/api/analyze"):
        record(
            "E3. A fallback is explained in words and the row tags match it",
            bool(out["notice"]) and all("ONLINE" in h.upper() for h in out["hints"]),
            f"notice={out['notice'][:80]!r} hints={out['hints']}",
        )
    page.close()
    return out


def test_f_analyze_without_helper(browser, url: str):
    helper_stop()
    page, calls = new_page(browser)
    page.goto(SITE, wait_until="domcontentloaded")
    wait_for_state(page, {"unavailable", "error"})
    out = analyze(page, url)

    hit_online = called(calls, "5001/api/analyze")
    hit_local = called(calls, "127.0.0.1:47821/metadata")
    record(
        "F. Helper stopped: analyze goes straight to the online /api/analyze",
        hit_online and not hit_local,
        f"online_analyze={hit_online} local_metadata={hit_local}",
    )
    record(
        "F2. Helper stopped: formats render with no error and no fallback notice",
        out["rows"] > 0 and not out["error"] and not out["notice"],
        f"rows={out['rows']} title={out['title']!r} error={out['error'][:70]!r}",
    )
    page.close()
    return out


def test_g_download_online(browser, url: str, quality: str, out_dir: str):
    """Part 15, online path: pick a quality, download, and keep the file."""
    helper_stop()
    page, _ = new_page(browser)
    page.goto(SITE, wait_until="domcontentloaded")
    wait_for_state(page, {"unavailable", "error"})
    analyze(page, url)

    row = page.locator(f'.format-card[data-quality="{quality}"] .btn-download-format')
    if row.count() == 0:
        record(f"G. Online download of {quality}", False, f"no {quality} row was offered")
        page.close()
        return None
    row.first.click()

    page.wait_for_function(
        """() => {
            const banner = document.getElementById('completed-banner');
            const error = document.getElementById('downloader-error');
            return (banner && !banner.classList.contains('hidden'))
                || (error && !error.classList.contains('hidden'));
        }""",
        timeout=300_000,
    )
    err = page.locator("#downloader-error")
    if err.is_visible():
        record(f"G. Online download of {quality}", False, err.inner_text()[:120])
        page.close()
        return None

    with page.expect_download(timeout=300_000) as dl_info:
        page.click("#btn-save-file")
    download = dl_info.value
    dest = os.path.join(out_dir, download.suggested_filename)
    download.save_as(dest)
    size = os.path.getsize(dest)
    record(
        f"G. Online download of {quality} produced a file",
        size > 100_000,
        f"{download.suggested_filename} ({size / 1_048_576:.2f} MiB)",
    )
    page.close()
    return dest


def test_h_download_local(browser, url: str, quality: str):
    """Part 15, local path: the Helper is up, so try to download through it."""
    helper_start()
    page, calls = new_page(browser)
    page.goto(SITE, wait_until="domcontentloaded")
    wait_for_state(page, {"available"})
    analyze(page, url)

    row = page.locator(f'.format-card[data-quality="{quality}"] .btn-download-format')
    if row.count() == 0:
        record(f"H. Local download of {quality}", False, f"no {quality} row was offered")
        page.close()
        return
    row.first.click()

    page.wait_for_function(
        """() => {
            const banner = document.getElementById('completed-banner');
            const error = document.getElementById('downloader-error');
            const metrics = document.getElementById('progress-metrics-text');
            return (banner && !banner.classList.contains('hidden'))
                || (error && !error.classList.contains('hidden'))
                || (metrics && /could not|Connection lost/i.test(metrics.textContent || ''));
        }""",
        timeout=300_000,
    )
    err = page.locator("#downloader-error")
    metrics = page.locator("#progress-metrics-text").inner_text()
    message = err.inner_text() if err.is_visible() else ""
    leaked = any(t in (message + metrics) for t in ("_failed", "exit status", "yt-dlp", "ffmpeg", "127.0.0.1"))
    record(
        f"H. Local download of {quality}: outcome is reported in plain language",
        not leaked,
        f"routed_local={called(calls, '47821/downloads')} metrics={metrics[:60]!r} error={message[:80]!r}",
    )
    page.close()


def test_i_local_path(browser, url: str, quality: str):
    """
    The local path end to end, assuming a Helper that can actually read videos.

    Separate from the tests above because the packaged Helper started by launchd
    cannot currently read YouTube at all — its yt-dlp has no JavaScript runtime
    on launchd's minimal PATH, so every /metadata call exits 1. Run this with the
    same packaged binary started from a shell (which inherits a full PATH) to
    check that the frontend's local route works once the Helper does.
    """
    page, calls = new_page(browser)
    page.goto(SITE, wait_until="domcontentloaded")
    wait_for_state(page, {"available"})
    out = analyze(page, url)

    record(
        "I. Helper that can read: analyze stays local, no fallback",
        called(calls, "47821/metadata")
        and not called(calls, "5001/api/analyze")
        and not out["notice"]
        and all("LOCAL" in h.upper() for h in out["hints"]),
        f"local={called(calls, '47821/metadata')} online={called(calls, '5001/api/analyze')} "
        f"hints={out['hints']} notice={out['notice'][:60]!r}",
    )

    row = page.locator(f'.format-card[data-quality="{quality}"] .btn-download-format')
    if row.count() == 0:
        record(f"I2. Local download of {quality}", False, f"no {quality} row was offered")
        page.close()
        return None
    row.first.click()

    page.wait_for_function(
        """() => {
            const banner = document.getElementById('completed-banner');
            const error = document.getElementById('downloader-error');
            return (banner && !banner.classList.contains('hidden'))
                || (error && !error.classList.contains('hidden'));
        }""",
        timeout=300_000,
    )
    err = page.locator("#downloader-error")
    if err.is_visible():
        record(f"I2. Local download of {quality}", False, err.inner_text()[:140])
        page.close()
        return None

    banner = page.locator("#completed-message-text").inner_text()
    record(
        f"I2. Local download of {quality} routed through the Helper and completed",
        called(calls, "47821/downloads") and "Downloads folder" in banner,
        f"banner={banner!r}",
    )
    # The Helper saves to disk itself, so there is no browser download to catch.
    save_hidden = not page.locator("#btn-save-file").is_visible()
    record("I3. Local mode hides the browser 'Save File' step", save_hidden, "")
    page.close()
    return True


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--headed", action="store_true")
    parser.add_argument("--url", default="https://www.youtube.com/watch?v=XF2WniCfmEE")
    parser.add_argument("--quality", default="480p")
    parser.add_argument("--out-dir", default=os.path.join(os.getcwd(), "test_downloads"))
    parser.add_argument(
        "--local-path-only",
        action="store_true",
        help="Skip Helper start/stop control and only verify the local route "
        "against whatever Helper is already listening.",
    )
    args = parser.parse_args()

    os.makedirs(args.out_dir, exist_ok=True)

    if args.local_path_only:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(headless=not args.headed)
            try:
                print("\n== Local route, against the Helper already running ==", flush=True)
                test_i_local_path(browser, args.url, args.quality)
            finally:
                browser.close()
        failed = [name for name, ok, _ in results if not ok]
        print(f"\n{len(results) - len(failed)}/{len(results)} checks passed", flush=True)
        return 1 if failed else 0

    if not os.path.exists(PLIST):
        print(f"!! no LaunchAgent at {PLIST}; install the packaged Helper first", file=sys.stderr)
        return 2

    saved = None
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(headless=not args.headed)
            try:
                print("\n== Part 14: indicator states ==", flush=True)
                test_a_helper_running_before_load(browser)
                test_b_helper_stopped_before_load(browser)
                test_c_helper_starts_while_open(browser)
                test_d_helper_stops_while_open(browser)

                print("\n== Part 14: analyze routing ==", flush=True)
                test_e_analyze_with_helper(browser, args.url)
                test_f_analyze_without_helper(browser, args.url)

                print("\n== Part 15: real downloads ==", flush=True)
                saved = test_g_download_online(browser, args.url, args.quality, args.out_dir)
                test_h_download_local(browser, args.url, args.quality)
            finally:
                browser.close()
    finally:
        # Whatever happened, leave the user's Helper running.
        helper_start()
        print(f"\nHelper restored: {'running' if helper_up() else 'NOT RUNNING'}", flush=True)

    if saved:
        probe = subprocess.run(
            ["ffprobe", "-v", "error", "-show_entries",
             "stream=codec_name,codec_type,width,height", "-of", "default=nw=1", saved],
            capture_output=True, text=True,
        )
        print(f"\nffprobe {os.path.basename(saved)}:\n{probe.stdout.strip() or probe.stderr.strip()}")

    failed = [name for name, ok, _ in results if not ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} checks passed", flush=True)
    for name in failed:
        print(f"  FAILED: {name}", flush=True)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
