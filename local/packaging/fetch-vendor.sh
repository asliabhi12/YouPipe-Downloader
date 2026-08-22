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
#   ./fetch-vendor.sh              # every platform
#   ./fetch-vendor.sh macos-arm64  # one platform
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

# FFmpeg, macOS: static per-architecture builds from ffmpeg.martin-riedl.de.
# This is the build the project's own download tests were validated against.
# See PACKAGING.md for the licensing consequences of shipping a GPL build.
FFMPEG_MAC_BASE="${FFMPEG_MAC_BASE:-https://ffmpeg.martin-riedl.de/redirect/latest/macos}"

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
	want="$(awk -v a="$asset" '$2 == a || $2 == "*"a {print $1}' "$sums" | head -1)"
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
	# bundled here (they are configured --enable-gpl).
	fetch "https://raw.githubusercontent.com/yt-dlp/yt-dlp/master/LICENSE" \
		"$LICENSES/yt-dlp-LICENSE.txt"
	fetch "https://www.gnu.org/licenses/old-licenses/gpl-2.0.txt" \
		"$LICENSES/GPL-2.0.txt"
}

record_versions() {
	local out="$VENDOR/VERSIONS.txt"
	: >"$out"
	{
		echo "Recorded by fetch-vendor.sh"
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
		fetch_ytdlp "$platform" "$YTDLP_MACOS_ASSET" yt-dlp
		fetch_ffmpeg_macos "$platform" arm64
		;;
	macos-amd64)
		fetch_ytdlp "$platform" "$YTDLP_MACOS_ASSET" yt-dlp
		fetch_ffmpeg_macos "$platform" amd64
		;;
	windows-amd64)
		fetch_ytdlp "$platform" "$YTDLP_WINDOWS_ASSET" yt-dlp.exe
		fetch_ffmpeg_windows "$platform"
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
