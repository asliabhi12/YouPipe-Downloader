# Packaging the YouPiper Helper

This directory turns the Helper into something an end user can install by
double-clicking, with no toolchain, runtime or terminal on their machine.

**Target experience**

```
Download YouPiper Helper  ->  Install  ->  Done
Open YouPiper             ->  Helper detected  ->  downloads land on the computer
```

Everything the Helper needs is bundled. The user installs nothing else.

---

## 1. Layout

```
packaging/
  build.sh                  builds every artifact
  fetch-vendor.sh           downloads and checksums the bundled tools
  BUNDLED-SOFTWARE.txt      third-party inventory, shipped inside each artifact
  PACKAGING.md              this file
  macos/
    Info.plist.in           bundle metadata template
    Read Me.txt             end-user text placed on the disk image
    Uninstall YouPiper Helper.command
  windows/
    Install.cmd
    Uninstall.cmd
    Read Me.txt
  vendor/                   fetched binaries (git-ignored, reproducible)
  dist/                     build output (git-ignored)
```

### What ships inside each artifact

**macOS** — `YouPiper Helper.app`

```
Contents/
  Info.plist
  MacOS/youpiper-helper           the Helper
  Resources/bin/                  yt-dlp, ffmpeg, ffprobe
  Resources/Licenses/             licence texts + BUNDLED-SOFTWARE.txt
```

**Windows** — a folder inside `YouPiper-Helper-<version>-amd64.zip`

```
YouPiper Helper/
  YouPiper-Helper.exe
  bin/                            yt-dlp.exe, ffmpeg.exe, ffprobe.exe
  Licenses/
  Install.cmd  Uninstall.cmd  Read Me.txt
```

The Helper finds its own copies first and only falls back to anything on the
system `PATH` when a bundled copy is absent — see
[`internal/binpath`](../internal/binpath/binpath.go). Precedence:

1. `YOUPIPER_YTDLP` / `YOUPIPER_FFMPEG` / `YOUPIPER_FFPROBE` (testing only)
2. bundled copies beside the executable
3. system `PATH` (development convenience)

`-status` prints which of the three each tool came from, so a support question
is one command away from an answer.

---

## 2. Build

```bash
./fetch-vendor.sh          # once, and whenever the bundled tools are updated
./build.sh --windows       # Mac .app + .dmg for this architecture, plus Windows
```

| Flag | Effect |
|---|---|
| `--arch arm64,amd64` | build both Mac architectures (default: host) |
| `--windows` | also produce the Windows `.exe` and `.zip` |
| `--dev` | tolerate an incomplete `vendor/` tree |
| `--no-dmg` | keep going if `hdiutil` cannot create a disk image |

A release build **refuses to start** if any bundled tool is missing from
`vendor/`. That check runs before compilation, so a failed build never leaves
release-named artifacts in `dist/`.

`--dev` relaxes it for exercising the packaging without network access. Those
artifacts are renamed `-dev-incomplete`, the build prints that they are not
distributable, and any substitution from the host `PATH` happens only when the
target platform *is* the host platform — a Mac `ffmpeg` is never staged as
`ffmpeg.exe`.

The version comes from `ServerVersion` in
[`internal/server/server.go`](../internal/server/server.go); there is no second
place to update.

---

## 3. Bundled software

`fetch-vendor.sh` pulls from official release channels only, verifies each
download against the publisher's own `SHA2-256SUMS` where one is published, and
records every hash in `vendor/SHA256SUMS.lock`. On a later run a changed hash is
a loud failure, not a silent update.

| Tool | Source | Licence |
|---|---|---|
| yt-dlp | `github.com/yt-dlp/yt-dlp` official releases (`yt-dlp_macos` universal2, `yt-dlp.exe`) | The Unlicense |
| ffmpeg, ffprobe (macOS) | `ffmpeg.martin-riedl.de` static builds | GPL-2.0-or-later |
| ffmpeg, ffprobe (Windows) | `github.com/BtbN/FFmpeg-Builds` (`win64-gpl`) | GPL-2.0-or-later |

The yt-dlp **standalone** build is used deliberately: it carries its own
interpreter, so the user never installs one.

The FFmpeg builds are configured `--enable-gpl`, which puts them under the GPL
rather than the LGPL, so each artifact carries the GPL text and a written offer
of source. Resolved versions and hashes land in `vendor/VERSIONS.txt` and
`vendor/SHA256SUMS.lock`; those two files are the record that satisfies the
offer. A build configured `--enable-nonfree` may never be used here — such
builds cannot be redistributed at all.

Licence texts and `BUNDLED-SOFTWARE.txt` are copied into every artifact.

> **Verified.** `fetch-vendor.sh` has been run successfully and a full release
> build was produced from its output: all three tools bundled, both licence texts
> present, `.dmg` and `.zip` created. `vendor/SHA256SUMS.lock` and
> `vendor/VERSIONS.txt` record the exact builds and are committed — a later run
> that returns different bytes fails loudly rather than updating silently.

---

## 4. Signing and notarization

**macOS — current status: unsigned development artifact.**

`build.sh` looks for a *Developer ID Application* identity via
`security find-identity`. This machine has none, so the build applies an
ad-hoc signature and prints:

```
NOT SIGNED with a Developer ID (none available); applying an ad-hoc signature only
   This build is NOT notarized. Gatekeeper will warn on other Macs.
```

The artifact in `dist/` is **not signed with a Developer ID and not notarized.**
Gatekeeper will refuse to open it by double-click on another Mac; the user has
to right-click → Open, which `Read Me.txt` explains.

To produce a distributable build, on a machine with the credentials:

```bash
export YOUPIPER_NOTARY_PROFILE=youpiper-notary
```

Store that profile once with
`xcrun notarytool store-credentials youpiper-notary --apple-id … --team-id … --password …`
(app-specific password). Then `./build.sh` signs nested binaries first, then the
bundle, with `--timestamp --options runtime`, submits, waits, and staples.
`YOUPIPER_SIGN_IDENTITY` overrides identity selection.

Remaining before public macOS distribution:

- [ ] Apple Developer Program membership
- [ ] Developer ID Application certificate in the login keychain
- [ ] `notarytool` keychain profile
- [ ] one notarized build verified with `spctl -a -vv "YouPiper Helper.app"` on a
      Mac that has never seen the source tree

**Windows — current status: unsigned.**

The `.exe` carries no Authenticode signature. SmartScreen will show
"Windows protected your PC" until the binary earns reputation, and `Read Me.txt`
says so plainly. A self-signed certificate is **not** used: it would not
suppress the warning and would misrepresent the publisher. Proper signing needs
a commercial code-signing certificate (OV, or EV to start with reputation).

No step here obfuscates, packs or otherwise hides what the binary does, and
nothing tries to evade antivirus. The Helper is a plain Go binary that shells
out to two well-known tools; that is exactly what a scanner should see.

---

## 5. Automatic start

Registration happens at first run, from the Helper itself — there is no
installer framework to maintain.

The gate is a build-time constant: `build.sh` passes
`-ldflags "-X ytd-local/internal/autostart.Packaged=true"`. Only a packaged
build ever registers. `go run ./cmd/agent` cannot leave a login item pointing
into a temporary build directory. `-no-startup` suppresses it even in a packaged
build.

**macOS** — a per-user LaunchAgent at
`~/Library/LaunchAgents/com.youpiper.helper.plist`, loaded with
`launchctl bootstrap gui/$UID`.

- `RunAtLoad` — starts at login
- `KeepAlive = {SuccessfulExit: false}` — a crash brings it back; a deliberate
  quit stays quit until next login. Plain `KeepAlive=true` would make quitting
  impossible.
- `ProcessType = Background`, `LowPriorityIO` — the scheduler treats it as the
  background utility it is
- `LSUIElement` in `Info.plist` — no Dock icon, no menu bar item, no window

**Windows** — one string value under
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, named `YouPiper Helper`
so it is recognisable in Task Manager's Startup tab. Written with `reg.exe`.

Neither path needs administrator or root privileges, installs a service or
system daemon, or touches anything outside the current user's account.

`-install-startup`, `-uninstall` and `-status` manage and report this by hand
if needed.

---

## 6. Network exposure

The Helper binds `127.0.0.1:47821` and nothing else. `Start()` rejects any
address whose host is not `127.0.0.1`/`localhost` before binding, so `0.0.0.0`
is refused by construction rather than by convention. Nothing on the LAN can
reach it. Verified by `internal/server` tests.

It requests no camera, microphone, contacts, location or full-disk access, and
writes only to its download directory and its own log file. `Info.plist`
carries no usage-description keys because none are needed.

---

## 7. Uninstall

**macOS** — `Uninstall YouPiper Helper.command`, on the disk image: unloads and
removes the LaunchAgent, stops the process, removes the `.app` from the usual
locations, removes `~/Library/Logs/YouPiper`.

**Windows** — `Uninstall.cmd`: removes the `Run` value *first* (so nothing is
left starting at login even if a file is locked), stops the process, removes
`%LOCALAPPDATA%\YouPiper` and the install folder.

Both leave downloaded files alone and leave no startup entry behind.

---

## 8. Footprint (measured)

Measured on macOS 15 (Darwin 25.5.0), Apple silicon, Go 1.26.6, from the
`arm64` build in `dist/`.

| | Measured |
|---|---|
| Helper executable, macOS arm64 | 5.6 MB |
| Helper executable, Windows amd64 | 6.2 MB |
| `.app`, Helper + metadata + licences only | 5.7 MB |
| `.zip`, Windows, Helper + docs only | 2.6 MB |
| Process start → exit (`-version`, 20 runs) | min 3.4 ms, median 4.1 ms, max 4.7 ms |
| Full init: tool resolution + login-item check (`-status`, 20 runs) | min 6.5 ms, median 7.2 ms, max 8.3 ms |
| **Resident memory, idle 24 min** | **9.5 MB** (RSS 9728 KB) |
| **CPU, idle 24 min** | **0.0 %** |

Idle figures come from a real installed Helper that had been resident for 24
minutes 33 seconds, sampled with:

```bash
ps -o pid,rss,%cpu,etime,command -p "$(pgrep -f 'YouPiper Helper.app/Contents/MacOS/youpiper-helper')"
```

While idle the process holds no timers and runs no polling loop — it blocks in
`Serve` on the listener and in a channel receive for the shutdown signal — which
is what the 0.0 % reading reflects. Neither `yt-dlp` nor `ffmpeg` is kept
running: each is spawned per download and exits with it.

A complete release `.app` with all three tools bundled measures **167 MB**
(ffmpeg 63 MB, ffprobe 63 MB, yt-dlp 35 MB), compressing to a **96 MB `.dmg`**;
the Windows `.zip` is **129 MB**. Almost all of that is FFmpeg.

---

## 9. Test matrix

macOS rows were run on macOS 15 (Darwin 25.5.0, Apple silicon) against a real
release build. No Windows hardware was available.

| # | Check | macOS | Windows |
|---|---|---|---|
| 1 | Builds from a clean tree | PASS | PASS (cross-compiled) |
| 2 | Refuses a release build with tools/licences missing | PASS | PASS |
| 3 | Bundle metadata valid (`plutil -lint`) | PASS | n/a |
| 4 | Bundled tools resolve from inside the artifact | PASS (all three `(bundled)`) | PENDING (no host) |
| 5 | No Dock icon / no console window | PASS (`LSUIElement`) | BUILD PASS, RUNTIME PENDING |
| 6 | Registers at login on first packaged run | PASS (`launchctl print` shows the agent) | PENDING (no host) |
| 7 | Dev build never registers at login | PASS (`Packaged: false`) | PASS |
| 8 | Declines to register from a disk image / translocated path | PASS (unit-tested; fixes an observed field failure) | PASS (unit-tested) |
| 9 | `GET /health` answers on loopback | PASS | PENDING |
| 10 | Second instance exits without a crash loop | PASS (logged and exited 0, first instance kept serving) | PENDING |
| 11 | Survives a reboot | PENDING | PENDING |
| 12 | Real download → `.mp4` / `.mp3` | PENDING | PENDING |
| 13 | Astro site detects the Helper | PENDING | PENDING |
| 14 | Disk image / archive builds | `.dmg` PASS (96 MB) | `.zip` PASS (129 MB) |
| 15 | Uninstall removes the startup entry, keeps downloads | Script reviewed; runtime PENDING | Script reviewed; runtime PENDING |

Rows 11–13 and 15 remain, plus every Windows runtime row. Row 13 needs the Astro
dev server pointed at a Helper that has yt-dlp bundled — not at an older
instance still holding the port.

### Field failure that produced row 8

A Helper opened straight from a mounted disk image registered a login item
pointing at `/Volumes/YouPiper Helper/…`. That path stops existing when the
image is ejected, and because `KeepAlive` retries, the result is a login item
that fails silently and repeatedly forever. Gatekeeper's App Translocation makes
it worse for a *downloaded* image: the app runs from a randomised
`…/AppTranslocation/<uuid>/d/…` path that is discarded when it quits.

`autostart.StableLocation` now declines to register from either kind of path and
logs why. Running from a disk image is not an installation, so declining is the
correct answer, not a workaround — the image's `Applications` shortcut is how the
user is meant to install.

The same episode showed that whichever copy binds the port first keeps serving,
so an older instance can silently outlive an upgrade. The `EADDRINUSE` log line
now says that explicitly, and `-status` distinguishes "nothing registered" from
"a *different* copy is registered" and prints that other path.

---

## 10. Deliberately absent

No auto-update, no analytics or telemetry, no accounts, no tray UI, no database,
no cloud service, no remote control, no crash reporting. The Helper is a small
loopback HTTP server that runs two tools on request, and it should stay that
size.
