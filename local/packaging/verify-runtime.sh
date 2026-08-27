#!/usr/bin/env bash
#
# Regression checks for the bundled JavaScript runtime (REG-JS-001 … REG-JS-008).
#
# yt-dlp cannot read a YouTube page without a JavaScript runtime, and launchd
# starts the Helper with PATH=/usr/bin:/bin:/usr/sbin:/sbin, where none exists.
# A packaged Helper therefore has to carry its own and find it by path. These
# checks assert that end to end, against the installed application rather than a
# development build.
#
#   ./verify-runtime.sh --offline    packaging and discovery only (no network)
#   ./verify-runtime.sh              everything, including real downloads
#
# Options:
#   --offline          skip anything needing the network or a running Helper
#   --app PATH         application to check (default /Applications/YouPiper Helper.app)
#   --url URL          video to use for the live checks
#   --fresh            move existing downloads aside first (see below)
#   --keep             leave the downloaded files in place
#
# Use --fresh when re-running the download checks. yt-dlp skips a file it has
# already downloaded and exits 0, so the job completes instantly and the check
# would inspect the previous run's file and pass without downloading anything.
#
# Checks that cannot be answered in the current environment report SKIP and say
# what was missing. A SKIP is not a pass.
set -u -o pipefail

cd "$(dirname "$0")"
PKG="$PWD"
LOCK="$PKG/vendor/SHA256SUMS.lock"

APP="/Applications/YouPiper Helper.app"
URL="https://www.youtube.com/watch?v=XF2WniCfmEE"
OFFLINE=0
KEEP=0
FRESH=0
HELPER_ADDR="127.0.0.1:47821"

# The PATH launchd gives a LaunchAgent. Not configurable per user, and holding no
# JavaScript runtime on a stock macOS install.
LAUNCHD_PATH="/usr/bin:/bin:/usr/sbin:/sbin"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--offline) OFFLINE=1; shift ;;
	--app) APP="$2"; shift 2 ;;
	--url) URL="$2"; shift 2 ;;
	--keep) KEEP=1; shift ;;
	--fresh) FRESH=1; shift ;;
	-h|--help) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "error: unknown option '$1'" >&2; exit 1 ;;
	esac
done

bold=$'\033[1m'; red=$'\033[31m'; green=$'\033[32m'; yellow=$'\033[33m'; dim=$'\033[2m'; reset=$'\033[0m'
PASSED=(); FAILED=(); SKIPPED=()

pass() { printf '%s  PASS %s%s %s%s%s\n' "$green" "$1" "$reset" "$dim" "${2:-}" "$reset"; PASSED+=("$1"); }
fail() { printf '%s  FAIL %s%s %s\n' "$red" "$1" "$reset" "${2:-}"; FAILED+=("$1"); }
skip() { printf '%s  SKIP %s%s %s%s%s\n' "$yellow" "$1" "$reset" "$dim" "${2:-}" "$reset"; SKIPPED+=("$1"); }
head2() { printf '\n%s%s%s\n' "$bold" "$1" "$reset"; }

DOWNLOADS="$HOME/Downloads"

if [[ $FRESH -eq 1 && -d "$DOWNLOADS" ]]; then
	archive="$DOWNLOADS/.superseded"
	mkdir -p "$archive"
	moved=0
	while IFS= read -r f; do
		mv "$f" "$archive/" 2>/dev/null && moved=$((moved + 1))
	done < <(find "$DOWNLOADS" -maxdepth 1 -type f)
	printf '%smoved %d existing download(s) to %s%s\n' "$dim" "$moved" "$archive" "$reset"
fi

BIN="$APP/Contents/Resources/bin"
DENO="$BIN/deno"
YTDLP="$BIN/yt-dlp"
HELPER_EXE="$APP/Contents/MacOS/youpiper-helper"

# --- REG-JS-001  the runtime is part of the package -------------------------
head2 "REG-JS-001  packaged JS runtime exists"

# The lockfile is checked first because it is the only part present in a fresh
# clone: vendor binaries are reproduced from it, never committed.
missing_pins=()
while read -r _ rel; do
	[[ "$rel" == */yt-dlp || "$rel" == */yt-dlp.exe ]] || continue
	platform="${rel%%/*}"
	want="deno"; [[ "$platform" == windows-* ]] && want="deno.exe"
	grep -q "  $platform/$want\$" "$LOCK" || missing_pins+=("$platform/$want")
done <"$LOCK"
if [[ ${#missing_pins[@]} -eq 0 ]]; then
	pass "001a lockfile pins a runtime for every platform" "$(grep -c '/deno' "$LOCK") entries"
else
	fail "001a lockfile pins a runtime for every platform" "not pinned: ${missing_pins[*]}"
fi

# The vendor tree, when it has been fetched, must match those pins.
if [[ -f "$PKG/vendor/macos-arm64/deno" ]]; then
	got="$(shasum -a 256 "$PKG/vendor/macos-arm64/deno" | awk '{print $1}')"
	want="$(awk '$2 == "macos-arm64/deno" {print $1}' "$LOCK")"
	if [[ -n "$want" && "$got" == "$want" ]]; then
		pass "001b vendored runtime matches the lockfile" "${got:0:12}…"
	else
		fail "001b vendored runtime matches the lockfile" "locked=$want got=$got"
	fi
else
	skip "001b vendored runtime matches the lockfile" "vendor tree not fetched"
fi

# And the built application must actually contain it.
if [[ ! -d "$APP" ]]; then
	skip "001c application bundles the runtime" "$APP not installed"
elif [[ -x "$DENO" ]]; then
	pass "001c application bundles the runtime" "$(du -h "$DENO" | awk '{print $1}') at Contents/Resources/bin/deno"
else
	fail "001c application bundles the runtime" "no executable at $DENO"
fi

# --- REG-JS-002  found without the user's PATH ------------------------------
head2 "REG-JS-002  Helper locates the bundled runtime without the user's PATH"

if [[ ! -x "$HELPER_EXE" ]]; then
	skip "002 Helper reports a bundled runtime" "$APP not installed"
else
	# env -i strips the developer's environment entirely, then only launchd's own
	# PATH is put back. Anything found here was found by path, not by lookup.
	status="$(env -i HOME="$HOME" PATH="$LAUNCHD_PATH" "$HELPER_EXE" -status 2>&1)"
	line="$(printf '%s\n' "$status" | grep '^deno:' || true)"
	if [[ "$line" == *"(bundled)"* ]]; then
		pass "002 Helper reports a bundled runtime" "${line}"
	else
		fail "002 Helper reports a bundled runtime" "-status said: ${line:-<no deno line>}"
	fi
fi

# --- REG-JS-003  the runtime works in the launchd environment ---------------
head2 "REG-JS-003  launchd environment can run the bundled runtime"

if [[ ! -x "$YTDLP" || ! -x "$DENO" ]]; then
	missing=(); [[ -x "$YTDLP" ]] || missing+=(yt-dlp); [[ -x "$DENO" ]] || missing+=(deno)
	skip "003 bundled yt-dlp solves the challenge under launchd's PATH" \
		"not in the installed bundle: ${missing[*]}"
elif [[ $OFFLINE -eq 1 ]]; then
	skip "003 bundled yt-dlp solves the challenge under launchd's PATH" "--offline"
else
	# The same starved environment, and the same flag the Helper passes. A run
	# without --js-runtimes is included so a pass here cannot be an accident of
	# the environment having a runtime after all.
	out_with="$(env -i HOME="$HOME" PATH="$LAUNCHD_PATH" "$YTDLP" -v --simulate \
		--no-playlist --js-runtimes "deno:$DENO" \
		--extractor-args "youtube:player_client=web_embedded" "$URL" 2>&1)"
	rc_with=$?
	runtime="$(printf '%s\n' "$out_with" | sed -n 's/.*JS runtimes: \(.*\)/\1/p' | head -1)"
	if [[ $rc_with -eq 0 && -n "$runtime" && "$runtime" != "none" ]]; then
		pass "003a bundled runtime is used under launchd's PATH" "JS runtimes: $runtime"
	else
		fail "003a bundled runtime is used under launchd's PATH" "exit=$rc_with runtimes='${runtime:-unset}'"
	fi

	out_without="$(env -i HOME="$HOME" PATH="$LAUNCHD_PATH" "$YTDLP" -v --simulate \
		--no-playlist --extractor-args "youtube:player_client=web_embedded" "$URL" 2>&1)"
	rc_without=$?
	if [[ $rc_without -ne 0 ]]; then
		pass "003b without the flag the same environment fails" "confirms the flag is what fixes it"
	else
		skip "003b without the flag the same environment fails" \
			"succeeded anyway — a runtime may have reached $LAUNCHD_PATH"
	fi
fi

# --- live checks against the launchd-started Helper -------------------------
health=""
if [[ $OFFLINE -eq 0 ]]; then
	health="$(curl -s --max-time 5 "http://$HELPER_ADDR/health" 2>/dev/null || true)"
fi

jqf() { python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1',''))" 2>/dev/null; }

head2 "REG-JS-004  /health reports runtime availability"
if [[ $OFFLINE -eq 1 ]]; then
	skip "004 /health reports js_runtime_available" "--offline"
elif [[ -z "$health" ]]; then
	fail "004 /health reports js_runtime_available" "no answer from http://$HELPER_ADDR/health"
else
	js="$(printf '%s' "$health" | jqf js_runtime_available)"
	st="$(printf '%s' "$health" | jqf status)"
	if [[ "$js" == "True" && "$st" == "ok" ]]; then
		pass "004 /health reports js_runtime_available" "$health"
	else
		fail "004 /health reports js_runtime_available" "$health"
	fi
fi

# --- REG-JS-005  a missing runtime is not reported as ready -----------------
head2 "REG-JS-005  missing runtime yields a degraded readiness state"

# An environment override cannot express this: a bundled copy beside the
# executable is found whatever the override says, which is the correct
# precedence. So the state is reproduced honestly instead — a bundle assembled
# exactly like the real one except that no runtime was staged into it, which is
# what a build with a broken vendor tree would ship.
#
# Both halves are run against the same scaffold so the difference is the runtime
# and nothing else.
probe_health() {
	local root="$1" port="$2" out=""
	env -i HOME="$HOME" PATH="$LAUNCHD_PATH" \
		"$root/Contents/MacOS/youpiper-helper" -no-startup -addr "127.0.0.1:$port" \
		>/dev/null 2>&1 &
	local pid=$!
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		out="$(curl -s --max-time 2 "http://127.0.0.1:$port/health" 2>/dev/null || true)"
		[[ -n "$out" ]] && break
		sleep 0.3
	done
	kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
	printf '%s' "$out"
}

if [[ ! -x "$HELPER_EXE" ]]; then
	skip "005 Helper with no runtime reports degraded" "$APP not installed"
else
	scaffold="$(mktemp -d)"
	# The executable is a real copy, not a symlink: bundle discovery resolves the
	# executable's own path, so a symlink would lead straight back to the
	# installed bundle and its runtime.
	mkdir -p "$scaffold/Contents/MacOS" "$scaffold/Contents/Resources/bin"
	cp "$HELPER_EXE" "$scaffold/Contents/MacOS/youpiper-helper"
	for tool in yt-dlp ffmpeg ffprobe; do
		[[ -x "$BIN/$tool" ]] && ln -s "$BIN/$tool" "$scaffold/Contents/Resources/bin/$tool"
	done

	without="$(probe_health "$scaffold" 47831)"
	st="$(printf '%s' "$without" | jqf status)"
	js="$(printf '%s' "$without" | jqf js_runtime_available)"
	if [[ "$st" == "degraded" && "$js" == "False" ]]; then
		pass "005a runtime-less bundle reports degraded" "$without"
	else
		fail "005a runtime-less bundle reports degraded" "${without:-no answer}"
	fi

	# Adding the runtime back is the only change; readiness must follow it.
	if [[ -x "$DENO" ]]; then
		ln -s "$DENO" "$scaffold/Contents/Resources/bin/deno"
		with="$(probe_health "$scaffold" 47832)"
		st="$(printf '%s' "$with" | jqf status)"
		js="$(printf '%s' "$with" | jqf js_runtime_available)"
		if [[ "$st" == "ok" && "$js" == "True" ]]; then
			pass "005b the same bundle plus a runtime reports ok" "$with"
		else
			fail "005b the same bundle plus a runtime reports ok" "${with:-no answer}"
		fi
	else
		skip "005b the same bundle plus a runtime reports ok" "no bundled runtime to add"
	fi

	rm -rf "$scaffold"
fi

# --- REG-JS-006  real metadata from the launchd-started Helper --------------
head2 "REG-JS-006  packaged Helper returns metadata"
meta=""
if [[ $OFFLINE -eq 1 ]]; then
	skip "006 POST /metadata returns formats" "--offline"
elif [[ -z "$health" ]]; then
	skip "006 POST /metadata returns formats" "Helper not running"
else
	meta="$(curl -s --max-time 120 -X POST "http://$HELPER_ADDR/metadata" \
		-H 'Content-Type: application/json' -d "{\"url\":\"$URL\"}" 2>/dev/null || true)"
	summary="$(printf '%s' "$meta" | python3 -c "
import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print('UNPARSEABLE'); raise SystemExit
if 'error' in d:
    print('ERROR ' + str(d.get('error')) + ': ' + str(d.get('details','')))
    raise SystemExit
qs = [f['quality'] for f in d.get('formats', [])]
print('OK|%s|%s' % (d.get('title','')[:48], ','.join(qs[:8])))
" 2>/dev/null)"
	if [[ "$summary" == OK\|* ]]; then
		pass "006 POST /metadata returns formats" "${summary#OK|}"
	else
		fail "006 POST /metadata returns formats" "${summary:-no response}"
	fi
fi

# download QUALITY  — start a job on the packaged Helper and wait it out.
# Echoes the job's final status.
download() {
	local quality="$1" resp job status
	resp="$(curl -s --max-time 20 -X POST "http://$HELPER_ADDR/downloads" \
		-H 'Content-Type: application/json' \
		-d "{\"url\":\"$URL\",\"quality\":\"$quality\"}" 2>/dev/null || true)"
	job="$(printf '%s' "$resp" | jqf job_id)"
	if [[ -z "$job" ]]; then
		echo "no-job-id: $resp"
		return 1
	fi
	local waited=0
	while ((waited < 900)); do
		sleep 3; waited=$((waited + 3))
		local body
		body="$(curl -s --max-time 10 "http://$HELPER_ADDR/downloads/$job" 2>/dev/null || true)"
		status="$(printf '%s' "$body" | jqf status)"
		case "$status" in
		completed) echo completed; return 0 ;;
		failed)    echo "failed: $(printf '%s' "$body" | jqf error)"; return 1 ;;
		cancelled) echo cancelled; return 1 ;;
		esac
	done
	echo "timed out after ${waited}s (last status: ${status:-unknown})"
	return 1
}

# probe_file TAG EXPECT_KIND — find the newest output carrying [TAG] and report
# what ffprobe actually says is inside it, not merely that a file appeared.
probe_file() {
	local tag="$1" kind="$2" dir="$DOWNLOADS" f
	f="$(find "$dir" -type f -iname "*[[]$tag[]]*" ! -name '*.part' ! -name '*.ytdl' \
		-print0 2>/dev/null | xargs -0 ls -t 2>/dev/null | head -1)"
	if [[ -z "$f" ]]; then
		echo "no file tagged [$tag] in $dir"
		return 1
	fi
	local probe
	probe="$(ffprobe -v error -show_entries \
		"stream=codec_type,codec_name,width,height:format=format_name,duration,size" \
		-of json "$f" 2>/dev/null)"
	printf '%s' "$probe" | FILE="$f" KIND="$kind" python3 -c "
import json, os, sys
f = os.environ['FILE']; kind = os.environ['KIND']
try:
    d = json.load(sys.stdin)
except Exception:
    print('ffprobe returned nothing for ' + os.path.basename(f)); raise SystemExit(1)
fmt = d.get('format', {})
streams = d.get('streams', [])
v = next((s for s in streams if s.get('codec_type') == 'video'), None)
a = next((s for s in streams if s.get('codec_type') == 'audio'), None)
size = int(fmt.get('size', 0)); dur = float(fmt.get('duration', 0) or 0)
container = fmt.get('format_name', '')
parts = [os.path.basename(f), 'container=' + container,
         'size=%.1f MiB' % (size / 1048576.0), 'duration=%.1fs' % dur]
problems = []
if kind == 'audio':
    if v: problems.append('unexpected video stream ' + str(v.get('codec_name')))
    if not a: problems.append('no audio stream')
    else: parts.append('audio=' + str(a.get('codec_name')))
    if 'mp3' not in container: problems.append('container is not mp3: ' + container)
else:
    if not v: problems.append('no video stream')
    else:
        parts.append('video=%s %sx%s' % (v.get('codec_name'), v.get('width'), v.get('height')))
        if int(v.get('height') or 0) != int(kind): problems.append('height %s, expected %s' % (v.get('height'), kind))
    if not a: problems.append('no audio stream')
    else: parts.append('audio=' + str(a.get('codec_name')))
    if 'mp4' not in container: problems.append('container is not mp4: ' + container)
if dur < 1: problems.append('duration is %.2fs' % dur)
if size < 100000: problems.append('file is only %d bytes' % size)
print('  '.join(parts))
if problems:
    print('PROBLEMS: ' + '; '.join(problems)); raise SystemExit(1)
"
}

# run_download_check ID QUALITY TAG KIND LABEL
run_download_check() {
	local id="$1" quality="$2" tag="$3" kind="$4" label="$5"
	if [[ $OFFLINE -eq 1 ]]; then
		skip "$id $label" "--offline"; return
	fi
	if [[ -z "$health" ]]; then
		skip "$id $label" "Helper not running"; return
	fi
	local outcome
	outcome="$(download "$quality")"
	if [[ "$outcome" != completed ]]; then
		fail "$id $label" "job $outcome"; return
	fi
	local verdict rc
	verdict="$(probe_file "$tag" "$kind")"; rc=$?
	if [[ $rc -eq 0 ]]; then
		pass "$id $label" "$verdict"
	else
		fail "$id $label" "$verdict"
	fi
}

head2 "REG-JS-007  packaged Helper downloads video"
run_download_check "007a" 480p 480p 480 "480p downloads and is really 480p MP4"
run_download_check "007b" 720p 720p 720 "720p downloads and is really 720p MP4"
run_download_check "007c" 1080p 1080p 1080 "1080p downloads and is really 1080p MP4"

head2 "REG-JS-008  packaged Helper downloads audio"
run_download_check "008" audio audio audio "MP3 downloads and is really an MP3"

# --- REG-JS-009  ON/OFF control -------------------------------------------
head2 "REG-JS-009  Helper ON/OFF control"
if [[ ! -x "$HELPER_EXE" ]]; then
	skip "009 Helper ON/OFF control flags" "$APP not installed"
else
	status_on="$("$HELPER_EXE" -install-startup 2>&1 || true)"
	if [[ "$status_on" == *"automatically"* ]]; then
		pass "009a enable helper startup" "$status_on"
	else
		fail "009a enable helper startup" "$status_on"
	fi

	status_off="$("$HELPER_EXE" -uninstall 2>&1 || true)"
	if [[ "$status_off" == *"no longer start"* ]]; then
		pass "009b disable helper startup" "$status_off"
	else
		fail "009b disable helper startup" "$status_off"
	fi
fi

if [[ $KEEP -eq 0 && $OFFLINE -eq 0 ]]; then
	printf '\n%s(downloaded test files left in ~/Downloads)%s\n' "$dim" "$reset"
fi

# --- summary ---------------------------------------------------------------
printf '\n%s──────── summary ────────%s\n' "$bold" "$reset"
printf '  %d passed, %d failed, %d skipped\n' "${#PASSED[@]}" "${#FAILED[@]}" "${#SKIPPED[@]}"
for n in "${SKIPPED[@]+"${SKIPPED[@]}"}"; do printf '%s  skipped: %s%s\n' "$yellow" "$n" "$reset"; done
for n in "${FAILED[@]+"${FAILED[@]}"}"; do printf '%s  failed:  %s%s\n' "$red" "$n" "$reset"; done
((${#FAILED[@]} == 0)) || exit 1
