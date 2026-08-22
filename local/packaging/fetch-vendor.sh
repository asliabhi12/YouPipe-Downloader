#!/usr/bin/env bash
#
# Download the third-party binaries the packaged Helper bundles, so the end user
# never has to install anything.
#
# Run this once before building a release. It needs network access; the build
# script itself does not. Everything lands in packaging/vendor/<platform>/ and
# every file's SHA-256 is recorded in packaging/vendor/SHA256SUMS.lock, which is
# what later runs verify against.
#
#   ./fetch-vendor.sh                            # every platform
#   ./fetch-vendor.sh macos-arm64                # one platform
#   ./fetch-vendor.sh --only deno macos-arm64    # one tool
#
# --only exists because the yt-dlp and FFmpeg sources are rolling: re-running a
# whole platform to add a single tool re-verifies those against the lock, and an
# upstream that has moved on stops the run before the tool you came for. Adding a
# tool must not require accepting an unrelated build you have not tested.
#
# Sources are all upstream project releases. Override any URL with the matching
# environment variable if you prefer a different builder.
set -euo pipefail

cd "$(dirname "$0")"
VENDOR="$PWD/vendor"
LICENSES="$PWD/vendor/LICENSES"
LOCK="$VENDOR/SHA256SUMS.lock"

# --- Sources -----------------------------------------------------------------
#
# yt-dlp: official project releases. `yt-dlp_macos` is a universal2 standalone
# build (no Python needed on the target machine); `yt-dlp.exe` is the Windows
# standalone. The project publishes SHA2-256SUMS in the same release, so these
# are checked against the publisher's own hashes and not just against our lock.
YTDLP_BASE="${YTDLP_BASE:-https://github.com/yt-dlp/yt-dlp/releases/latest/download}"
YTDLP_MACOS_ASSET="${YTDLP_MACOS_ASSET:-yt-dlp_macos}"
YTDLP_WINDOWS_ASSET="${YTDLP_WINDOWS_ASSET:-yt-dlp.exe}"

# FFmpeg, Windows: BtbN's FFmpeg-Builds, the builder linked from ffmpeg.org's
# download page for Windows. "gpl" in the name means a GPL-licensed build; it is
# redistributable, unlike the "nonfree" variants, which must never be used here.
FFMPEG_WIN_URL="${FFMPEG_WIN_URL:-https://github.com/BtbN/FFmpeg-Builds/releases/latest/download/ffmpeg-master-latest-win64-gpl.zip}"

# Deno: the JavaScript runtime yt-dlp uses to solve YouTube's challenge scripts.
# Without one YouTube offers no playable formats at all, and the PATH launchd
# gives the Helper (/usr/bin:/bin:/usr/sbin:/sbin) contains no runtime on any
# stock macOS install — so this is a hard requirement, not a nicety.
#
# Pinned to an exact tag rather than "latest": a runtime is the component most
# likely to change behaviour under us, and yt-dlp states a minimum version
# (2.3.0), so which one shipped needs to be a recorded fact. Official releases
# from the Deno project, each with the publisher's own .sha256sum beside it.
# Grab "deno", never "denort" — the reduced runtime cannot run the solver.
DENO_VERSION="${DENO_VERSION:-v2.9.5}"
DENO_BASE="${DENO_BASE:-https://github.com/denoland/deno/releases/download}"

# FFmpeg, macOS: static per-architecture builds from ffmpeg.martin-riedl.de.
# This is the build the project's own download tests were validated against.
# See PACKAGING.md for the licensing consequences of shipping a GPL build.
FFMPEG_MAC_BASE="${FFMPEG_MAC_BASE:-https://ffmpeg.martin-riedl.de/redirect/latest/macos}"

ONLY=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--only) ONLY="$2"; shift 2 ;;
	-h|--help) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) break ;;
	esac
done

# want TOOL — is this tool part of the requested run?
want() { [[ -z "$ONLY" || "$ONLY" == "$1" ]]; }

PLATFORMS=("${@:-macos-arm64 macos-amd64 windows-amd64}")
# shellcheck disable=SC2206 # deliberate word splitting of the default list
PLATFORMS=(${PLATFORMS[*]})

need() {
	command -v "$1" >/dev/null 2>&1 || { echo "error: $1 is required" >&2; exit 1; }
}
need curl
need shasum
need unzip

sha256() { shasum -a 256 "$1" | awk '{print $1}'; }

# fetch URL DEST — download with a hard failure on any HTTP error, so a 404 from
# a moved asset can never be mistaken for a successful empty download.
fetch() {
	local url="$1" dest="$2"
	echo "  GET $url"
	curl --fail --location --silent --show-error --retry 3 --retry-delay 2 \
		--output "$dest" "$url"
	[[ -s "$dest" ]] || { echo "error: empty download from $url" >&2; exit 1; }
}

# record REL_PATH — append or verify this file's hash in the lockfile.
record() {
	local rel="$1" abs="$VENDOR/$1" got want
	got="$(sha256 "$abs")"
	mkdir -p "$(dirname "$LOCK")"
	touch "$LOCK"
	want="$(awk -v f="$rel" '$2 == f {print $1}' "$LOCK" || true)"
	if [[ -z "$want" ]]; then
		echo "$got  $rel" >>"$LOCK"
		echo "  locked $rel  $got"
	elif [[ "$got" != "$want" ]]; then
		echo "error: $rel hash changed" >&2
		echo "  locked: $want" >&2
		echo "  got:    $got" >&2
		echo "  Upstream published a new build. Review the change, then remove the" >&2
		echo "  line from $LOCK and re-run to accept it." >&2
		exit 1
	else
		echo "  verified $rel"
	fi
}

# verify_against_publisher SUMS_FILE ASSET_NAME LOCAL_FILE
verify_against_publisher() {
	local sums="$1" asset="$2" file="$3" want got
	# Carriage returns are stripped first: a checksum file written on Windows has
	# CRLF endings, and a hash with a trailing \r compares unequal to the identical
	# hash while printing indistinguishably from it in any error message.
	local clean="$sums.clean"
	tr -d '\r' <"$sums" >"$clean"
	want="$(awk -v a="$asset" '$2 == a || $2 == "*"a {print $1}' "$clean" | head -1)"
	if [[ -z "$want" ]]; then
		# Deno's Windows checksum is written by PowerShell's Get-FileHash, which
		# prints "Hash : <UPPERCASE>" on its own line instead of the coreutils
		# "<hash>  <name>" layout. Same guarantee, different shape.
		want="$(awk '/^Hash[[:space:]]*:/ {print tolower($3)}' "$clean" | head -1)"
	fi
	if [[ -z "$want" ]]; then
		echo "error: $asset is not listed in the publisher's checksum file" >&2
		exit 1
	fi
	got="$(sha256 "$file")"
	if [[ "$got" != "$want" ]]; then
		echo "error: $asset failed the publisher's checksum" >&2
		echo "  published: $want" >&2
		echo "  got:       $got" >&2
		exit 1
	fi
	echo "  publisher checksum OK for $asset"
}

fetch_ytdlp() {
	local platform="$1" asset="$2" out="$3"
	local dir="$VENDOR/$platform"
	mkdir -p "$dir"
	local tmp
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' RETURN

	fetch "$YTDLP_BASE/$asset" "$tmp/$asset"
	fetch "$YTDLP_BASE/SHA2-256SUMS" "$tmp/SHA2-256SUMS"
	verify_against_publisher "$tmp/SHA2-256SUMS" "$asset" "$tmp/$asset"

	install -m 0755 "$tmp/$asset" "$dir/$out"
	record "$platform/$out"
}

# fetch_deno PLATFORM ASSET_STEM OUT_NAME — ASSET_STEM is the release asset
# without its .zip suffix, e.g. deno-aarch64-apple-darwin.
fetch_deno() {
	local platform="$1" stem="$2" out="$3"
	local dir="$VENDOR/$platform"
	mkdir -p "$dir"
	local tmp
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' RETURN

	fetch "$DENO_BASE/$DENO_VERSION/$stem.zip" "$tmp/$stem.zip"
	fetch "$DENO_BASE/$DENO_VERSION/$stem.zip.sha256sum" "$tmp/sums"
	verify_against_publisher "$tmp/sums" "$stem.zip" "$tmp/$stem.zip"

	unzip -o -q -j "$tmp/$stem.zip" -d "$tmp/x"
	local found
	found="$(find "$tmp/x" -type f -name "$out" | head -1)"
	[[ -n "$found" ]] || { echo "error: $out not found inside $stem.zip" >&2; exit 1; }
	install -m 0755 "$found" "$dir/$out"
	record "$platform/$out"
}

fetch_ffmpeg_macos() {
	local platform="$1" arch="$2"
	local dir="$VENDOR/$platform"
	mkdir -p "$dir"
	local tmp
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' RETURN

	for tool in ffmpeg ffprobe; do
		fetch "$FFMPEG_MAC_BASE/$arch/release/$tool.zip" "$tmp/$tool.zip"
		unzip -o -q -j "$tmp/$tool.zip" -d "$tmp/$tool.d"
		local found
		found="$(find "$tmp/$tool.d" -type f -name "$tool" | head -1)"
		[[ -n "$found" ]] || { echo "error: $tool not found inside its archive" >&2; exit 1; }
		install -m 0755 "$found" "$dir/$tool"
		record "$platform/$tool"
	done
}

fetch_ffmpeg_windows() {
	local platform="$1"
	local dir="$VENDOR/$platform"
	mkdir -p "$dir"
	local tmp
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' RETURN

	fetch "$FFMPEG_WIN_URL" "$tmp/ffmpeg.zip"
	unzip -o -q "$tmp/ffmpeg.zip" -d "$tmp/x"
	for tool in ffmpeg ffprobe; do
		local found
		found="$(find "$tmp/x" -type f -name "$tool.exe" | head -1)"
		[[ -n "$found" ]] || { echo "error: $tool.exe not found inside the archive" >&2; exit 1; }
		install -m 0755 "$found" "$dir/$tool.exe"
		record "$platform/$tool.exe"
	done
	# The build's own licence text travels with the binaries it applies to.
	local lic
	lic="$(find "$tmp/x" -type d -name LICENSE -o -type f -name 'LICENSE*' | head -1)"
	if [[ -n "$lic" ]]; then
		mkdir -p "$LICENSES/ffmpeg-windows"
		cp -R "$lic" "$LICENSES/ffmpeg-windows/" 2>/dev/null || true
	fi
}

fetch_licenses() {
	mkdir -p "$LICENSES"
	echo "Fetching licence texts"
	# Unlicense covers yt-dlp itself; GPL-2.0 is the licence of the FFmpeg builds
	# bundled here (they are configured --enable-gpl); Deno is MIT.
	fetch "https://raw.githubusercontent.com/yt-dlp/yt-dlp/master/LICENSE" \
		"$LICENSES/yt-dlp-LICENSE.txt"
	fetch "https://www.gnu.org/licenses/old-licenses/gpl-2.0.txt" \
		"$LICENSES/GPL-2.0.txt"
	# Taken from the tag that was fetched, not from main, so the text matches the
	# binary that ships.
	fetch "https://raw.githubusercontent.com/denoland/deno/$DENO_VERSION/LICENSE.md" \
		"$LICENSES/deno-LICENSE.txt"
}

record_versions() {
	local out="$VENDOR/VERSIONS.txt"
	: >"$out"
	{
		echo "Recorded by fetch-vendor.sh"
		echo
		echo "[pinned versions]"
		printf '  %-14s %s\n' deno "$DENO_VERSION"
		echo "  yt-dlp, ffmpeg, ffprobe: latest upstream release at fetch time; the"
		echo "  hashes below are what SHA256SUMS.lock holds them to."
		echo
		for d in "$VENDOR"/*/; do
			local p
			p="$(basename "$d")"
			[[ "$p" == "LICENSES" ]] && continue
			echo "[$p]"
			for f in "$d"*; do
				[[ -f "$f" ]] || continue
				printf '  %-14s %s\n' "$(basename "$f")" "$(sha256 "$f")"
			done
			echo
		done
	} >>"$out"
	echo "Wrote $out"
}

for platform in "${PLATFORMS[@]}"; do
	echo "==> $platform"
	case "$platform" in
	macos-arm64)
		if want yt-dlp; then fetch_ytdlp "$platform" "$YTDLP_MACOS_ASSET" yt-dlp; fi
		if want ffmpeg; then fetch_ffmpeg_macos "$platform" arm64; fi
		if want deno; then fetch_deno "$platform" deno-aarch64-apple-darwin deno; fi
		;;
	macos-amd64)
		if want yt-dlp; then fetch_ytdlp "$platform" "$YTDLP_MACOS_ASSET" yt-dlp; fi
		if want ffmpeg; then fetch_ffmpeg_macos "$platform" amd64; fi
		if want deno; then fetch_deno "$platform" deno-x86_64-apple-darwin deno; fi
		;;
	windows-amd64)
		if want yt-dlp; then fetch_ytdlp "$platform" "$YTDLP_WINDOWS_ASSET" yt-dlp.exe; fi
		if want ffmpeg; then fetch_ffmpeg_windows "$platform"; fi
		if want deno; then fetch_deno "$platform" deno-x86_64-pc-windows-msvc deno.exe; fi
		;;
	*)
		echo "error: unknown platform '$platform'" >&2
		echo "  known: macos-arm64 macos-amd64 windows-amd64" >&2
		exit 1
		;;
	esac
done

fetch_licenses
record_versions

echo
echo "Vendor tree ready. Next: ./build.sh"
