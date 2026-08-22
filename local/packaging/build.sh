#!/usr/bin/env bash
#
# Build the distributable YouPiper Helper artifacts.
#
#   ./build.sh                       # host architecture, release layout
#   ./build.sh --arch arm64,amd64    # both Mac architectures
#   ./build.sh --windows             # Windows artifact as well
#   ./build.sh --dev                 # allow an incomplete vendor tree
#   ./build.sh --no-dmg              # skip the disk image (hdiutil unavailable)
#
# Release builds require packaging/vendor/ to be populated by fetch-vendor.sh and
# refuse to produce an artifact with a missing tool. --dev relaxes that so the
# packaging itself can be exercised without network access; the artifacts it
# produces are renamed to make their incompleteness impossible to miss.
set -euo pipefail

cd "$(dirname "$0")"
PKG="$PWD"
ROOT="$(cd .. && pwd)"          # the Go module (local/)
VENDOR="$PKG/vendor"
DIST="$PKG/dist"

APP_NAME="YouPiper Helper"
EXE_NAME="youpiper-helper"
BUNDLE_ID="com.youpiper.helper"
VERSION="$(awk -F'"' '/ServerVersion =/{print $2}' "$ROOT/internal/server/server.go")"
[[ -n "$VERSION" ]] || { echo "error: could not read version from internal/server/server.go" >&2; exit 1; }
COPYRIGHT="YouPiper. Bundles yt-dlp (Unlicense) and FFmpeg (GPL-2.0-or-later); see Licenses."

# Packaged builds register themselves at login; development builds must not.
LDFLAGS="-s -w -X ytd-local/internal/autostart.Packaged=true"

HOST_ARCH="$(uname -m)"
[[ "$HOST_ARCH" == "x86_64" ]] && HOST_ARCH="amd64"
case "$(uname -s)" in
Darwin) HOST_PLATFORM="macos-$HOST_ARCH" ;;
*)      HOST_PLATFORM="$(uname -s | tr '[:upper:]' '[:lower:]')-$HOST_ARCH" ;;
esac

ARCHES="$HOST_ARCH"
DO_WINDOWS=0
DEV=0
ALLOW_NO_DMG=0

while [[ $# -gt 0 ]]; do
	case "$1" in
	--arch) ARCHES="${2//,/ }"; shift 2 ;;
	--windows) DO_WINDOWS=1; shift ;;
	--dev) DEV=1; shift ;;
	--no-dmg) ALLOW_NO_DMG=1; shift ;;
	-h|--help) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "error: unknown option '$1'" >&2; exit 1 ;;
	esac
done

MISSING=()
NO_DMG=()

# stage_tool SRC DEST_DIR NAME PLATFORM — copy a bundled binary, or record it as
# missing.
#
# A release build stops at the end if anything is missing: shipping a Helper
# without its own yt-dlp or FFmpeg would silently fall back to whatever the user
# happens to have installed, which is exactly what bundling is meant to avoid.
stage_tool() {
	local src="$1" dir="$2" name="$3" platform="$4"
	if [[ -f "$src" ]]; then
		install -m 0755 "$src" "$dir/$name"
		return 0
	fi
	MISSING+=("$platform/$name (expected at $src)")
	if [[ $DEV -eq 1 ]]; then
		# Fall back to a copy on PATH so the layout, sizes and idle behaviour of
		# the packaging can still be measured. Only ever for the host platform:
		# copying this Mac's ffmpeg in as ffmpeg.exe would produce an artifact
		# whose size and contents say nothing true about the Windows build.
		if [[ "$platform" != "$HOST_PLATFORM" ]]; then
			echo "  dev: $name omitted (no host copy usable for $platform)"
			return 0
		fi
		local found
		found="$(command -v "${name%.exe}" || true)"
		if [[ -n "$found" ]] && head -c2 "$found" | grep -qv '#!'; then
			install -m 0755 "$found" "$dir/$name"
			echo "  dev: staged $name from $found"
			return 0
		fi
		echo "  dev: $name unavailable, omitted"
	fi
	return 0
}

# Licence texts a release must carry. BUNDLED-SOFTWARE.txt names both by
# filename, and the bundled FFmpeg builds are GPL, so an artifact without them
# is both a broken cross-reference and a licence violation.
REQUIRED_LICENSES=(yt-dlp-LICENSE.txt GPL-2.0.txt)

stage_licenses() {
	local dest="$1"
	mkdir -p "$dest"
	if [[ -d "$VENDOR/LICENSES" ]]; then
		cp -R "$VENDOR/LICENSES/." "$dest/"
	fi
	cp "$PKG/BUNDLED-SOFTWARE.txt" "$dest/BUNDLED-SOFTWARE.txt"
}

build_macos() {
	local arch="$1"
	local platform="macos-$arch"
	local work="$DIST/work-$platform"
	local suffix=""
	[[ $DEV -eq 1 ]] && suffix="-dev-incomplete"
	# The suffix goes on the bundle itself, not just on the copies in dist/. A
	# disk image containing a plain "YouPiper Helper.app" is indistinguishable
	# from a release once it is mounted, which is exactly how an incomplete build
	# ends up being run and trusted.
	local app="$work/$APP_NAME$suffix.app"

	echo "==> macOS $arch"
	rm -rf "$work"
	mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources/bin"

	echo "  compiling"
	# Deployment target matches LSMinimumSystemVersion in Info.plist.in.
	CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
		go build -trimpath -ldflags "$LDFLAGS" \
		-o "$app/Contents/MacOS/$EXE_NAME" "$ROOT/cmd/agent"

	sed -e "s/@@VERSION@@/$VERSION/g" \
		-e "s|@@COPYRIGHT@@|$COPYRIGHT|g" \
		"$PKG/macos/Info.plist.in" >"$app/Contents/Info.plist"
	plutil -lint "$app/Contents/Info.plist" >/dev/null

	echo "  staging bundled tools"
	stage_tool "$VENDOR/$platform/yt-dlp"  "$app/Contents/Resources/bin" yt-dlp  "$platform"
	stage_tool "$VENDOR/$platform/ffmpeg"  "$app/Contents/Resources/bin" ffmpeg  "$platform"
	stage_tool "$VENDOR/$platform/ffprobe" "$app/Contents/Resources/bin" ffprobe "$platform"
	stage_licenses "$app/Contents/Resources/Licenses"

	sign_macos "$app"

	local dmg="$DIST/YouPiper-Helper-$VERSION-$arch$suffix.dmg"
	local appout="$DIST/$APP_NAME$suffix.app"

	rm -rf "$appout"
	cp -R "$app" "$appout"

	echo "  building disk image"
	local dmgroot="$work/dmg"
	mkdir -p "$dmgroot"
	cp -R "$app" "$dmgroot/"
	ln -s /Applications "$dmgroot/Applications"
	cp "$PKG/macos/Uninstall YouPiper Helper.command" "$dmgroot/"
	chmod +x "$dmgroot/Uninstall YouPiper Helper.command"
	cp "$PKG/macos/Read Me.txt" "$dmgroot/Read Me.txt"

	rm -f "$dmg"
	if hdiutil create -quiet -volname "$APP_NAME$suffix" -srcfolder "$dmgroot" \
		-ov -format UDZO "$dmg"; then
		echo "  -> $dmg"
	else
		# hdiutil needs to attach a disk device, which sandboxed and containerised
		# build environments often forbid. The .app is still valid; only the
		# wrapper is missing.
		NO_DMG+=("$dmg")
		echo "  DISK IMAGE FAILED: hdiutil could not create $(basename "$dmg")"
		echo "     The .app was built and is at $appout"
		[[ $ALLOW_NO_DMG -eq 1 ]] || { rm -rf "$work"; return 1; }
	fi

	rm -rf "$work"
	echo "  -> $appout"
}

sign_macos() {
	local app="$1"
	local identity="${YOUPIPER_SIGN_IDENTITY:-}"

	if [[ -z "$identity" ]]; then
		# Look for a Developer ID identity; anything else cannot be notarized.
		identity="$(security find-identity -v -p codesigning 2>/dev/null \
			| awk -F'"' '/Developer ID Application/{print $2; exit}')"
	fi

	# Nested binaries are signed before the bundle that contains them.
	local inner=("$app/Contents/Resources/bin"/*)

	if [[ -z "$identity" ]]; then
		echo "  NOT SIGNED with a Developer ID (none available); applying an ad-hoc signature only"
		echo "     This build is NOT notarized. Gatekeeper will warn on other Macs."
		for f in "${inner[@]}"; do
			[[ -f "$f" ]] && codesign --force --sign - "$f" >/dev/null 2>&1 || true
		done
		codesign --force --deep --sign - "$app" >/dev/null 2>&1 || true
		return
	fi

	echo "  signing with: $identity"
	for f in "${inner[@]}"; do
		[[ -f "$f" ]] || continue
		codesign --force --timestamp --options runtime --sign "$identity" "$f"
	done
	codesign --force --timestamp --options runtime \
		--identifier "$BUNDLE_ID" --sign "$identity" "$app"
	codesign --verify --strict --verbose=2 "$app"

	if [[ -n "${YOUPIPER_NOTARY_PROFILE:-}" ]]; then
		echo "  notarizing (this waits on Apple)"
		local zip="$app.notarize.zip"
		ditto -c -k --keepParent "$app" "$zip"
		xcrun notarytool submit "$zip" \
			--keychain-profile "$YOUPIPER_NOTARY_PROFILE" --wait
		xcrun stapler staple "$app"
		rm -f "$zip"
		echo "  notarized and stapled"
	else
		echo "  signed but NOT notarized (set YOUPIPER_NOTARY_PROFILE to notarize)"
	fi
}

build_windows() {
	local arch="amd64"
	local platform="windows-$arch"
	local work="$DIST/work-$platform"
	local suffix=""
	[[ $DEV -eq 1 ]] && suffix="-dev-incomplete"
	local folder="$work/YouPiper Helper"

	echo "==> Windows $arch"
	rm -rf "$work"
	mkdir -p "$folder/bin"

	echo "  compiling"
	# -H=windowsgui keeps a console window from flashing up at login. The Helper
	# has no console output to show a user.
	CGO_ENABLED=0 GOOS=windows GOARCH="$arch" \
		go build -trimpath -ldflags "$LDFLAGS -H=windowsgui" \
		-o "$folder/YouPiper-Helper.exe" "$ROOT/cmd/agent"

	echo "  staging bundled tools"
	stage_tool "$VENDOR/$platform/yt-dlp.exe"  "$folder/bin" yt-dlp.exe  "$platform"
	stage_tool "$VENDOR/$platform/ffmpeg.exe"  "$folder/bin" ffmpeg.exe  "$platform"
	stage_tool "$VENDOR/$platform/ffprobe.exe" "$folder/bin" ffprobe.exe "$platform"
	stage_licenses "$folder/Licenses"

	cp "$PKG/windows/Install.cmd" "$folder/Install.cmd"
	cp "$PKG/windows/Uninstall.cmd" "$folder/Uninstall.cmd"
	cp "$PKG/windows/Read Me.txt" "$folder/Read Me.txt"

	cp "$folder/YouPiper-Helper.exe" "$DIST/YouPiper-Helper-$VERSION-$arch$suffix.exe"

	local zip="$DIST/YouPiper-Helper-$VERSION-$arch$suffix.zip"
	rm -f "$zip"
	(cd "$work" && zip -q -r "$zip" "YouPiper Helper")

	rm -rf "$work"
	echo "  -> $zip"
	echo "  -> $DIST/YouPiper-Helper-$VERSION-$arch$suffix.exe"
}

# --- run ---------------------------------------------------------------------

command -v go >/dev/null || { echo "error: go toolchain not found" >&2; exit 1; }

# Check the vendor tree before compiling anything. A release build that
# discovered the problem halfway through would leave half-built artifacts in
# dist/ under names that look shippable.
preflight() {
	local absent=()
	for arch in $ARCHES; do
		for tool in yt-dlp ffmpeg ffprobe; do
			[[ -f "$VENDOR/macos-$arch/$tool" ]] || absent+=("macos-$arch/$tool")
		done
	done
	if [[ $DO_WINDOWS -eq 1 ]]; then
		for tool in yt-dlp ffmpeg ffprobe; do
			[[ -f "$VENDOR/windows-amd64/$tool.exe" ]] || absent+=("windows-amd64/$tool.exe")
		done
	fi
	for lic in "${REQUIRED_LICENSES[@]}"; do
		[[ -f "$VENDOR/LICENSES/$lic" ]] || absent+=("LICENSES/$lic")
	done
	[[ ${#absent[@]} -eq 0 ]] && return 0

	echo "Missing from the vendor tree:" >&2
	printf '  - vendor/%s\n' "${absent[@]}" >&2
	if [[ $DEV -eq 1 ]]; then
		echo >&2
		echo "Continuing because --dev was passed. Artifacts will be marked" >&2
		echo "-dev-incomplete and are NOT distributable." >&2
		return 0
	fi
	echo >&2
	echo "error: refusing to build a release artifact without its bundled tools." >&2
	echo "Run ./fetch-vendor.sh first, or pass --dev to build anyway." >&2
	exit 1
}

for arch in $ARCHES; do
	case "$arch" in
	arm64|amd64) ;;
	*) echo "error: unsupported macOS arch '$arch' (use arm64 or amd64)" >&2; exit 1 ;;
	esac
done

preflight
mkdir -p "$DIST"

for arch in $ARCHES; do
	build_macos "$arch"
done

[[ $DO_WINDOWS -eq 1 ]] && build_windows

echo
echo "Artifacts in $DIST:"
find "$DIST" -maxdepth 1 -mindepth 1 \( -name '*.dmg' -o -name '*.zip' -o -name '*.exe' -o -name '*.app' \) \
	-exec du -sh {} \; | sed 's/^/  /'

if [[ ${#NO_DMG[@]} -gt 0 ]]; then
	echo
	echo "Disk images NOT produced (hdiutil failed):"
	printf '  - %s\n' "${NO_DMG[@]##*/}"
	echo "  The .app bundles above are complete; only the .dmg wrapper is missing."
	echo "  Re-run on a machine where hdiutil can attach a disk device."
fi

if [[ ${#MISSING[@]} -gt 0 ]]; then
	echo
	echo "This is a --dev build and is NOT distributable. Tools not found in the"
	echo "vendor tree (some may have been substituted from this machine's PATH):"
	printf '  - %s\n' "${MISSING[@]}"
fi
