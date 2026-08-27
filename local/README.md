# YouPiper Helper (Go Companion)

## Purpose
YouPiper Helper is a lightweight localhost HTTP daemon that runs directly on the user's computer. It processes media downloads locally and saves them directly to the user's standard Downloads directory (`~/Downloads`).

## Technology
- **Language**: Go 1.22+
- **Core Engine**: `yt-dlp` CLI + `FFmpeg`
- **Port**: `127.0.0.1:47821`
- **Third-party Go modules**: none

## Dependencies

Distributed builds bundle `yt-dlp`, `ffmpeg` and `ffprobe` inside the artifact, so an end user installs nothing. For development you need those three on `PATH` instead:

- Go 1.22+
- `yt-dlp`
- `ffmpeg` (and `ffprobe`)

`internal/binpath` resolves each tool in this order — bundled copy beside the executable, then `PATH`, with `YOUPIPER_YTDLP` / `YOUPIPER_FFMPEG` / `YOUPIPER_FFPROBE` overriding both for testing. `-status` prints which source won.

### The JavaScript runtime: why one is bundled

YouTube will not hand out format URLs until a per-request JavaScript challenge
(the "n challenge") has been solved. `yt-dlp` solves it by running a script in an
external JavaScript runtime — `deno`, `node`, `bun` or `quickjs` — which it looks
for on `PATH` by default.

That default is what broke every local download in the packaged Helper. launchd
starts a LaunchAgent with a fixed, minimal `PATH`:

```
$ ps eww <helper pid> | tr ' ' '\n' | grep ^PATH=
PATH=/usr/bin:/bin:/usr/sbin:/sbin          # no JavaScript runtime here, ever
```

A Helper started from a developer's shell inherits a `PATH` containing
`/usr/local/bin` and works. The same binary started at login by launchd did not,
and every `/metadata` and `/downloads` call failed:

```
[debug] JS runtimes: none
[youtube] [jsc] JS Challenge Providers: bun (unavailable), deno (unavailable),
                                        node (unavailable), quickjs (unavailable)
WARNING: n challenge solving failed
ERROR: No video formats found!            -> exit status 1 -> metadata_failed
```

Two things fix it, and both are in place.

**Deno is bundled** at `Contents/Resources/bin/deno`, beside `yt-dlp`, fetched by
`packaging/fetch-vendor.sh` at a pinned version and verified against Deno's own
published SHA-256. `binpath` resolves it exactly like the other tools — bundled
copy first, `PATH` only as a development fallback — and every `yt-dlp` invocation
names its absolute location:

```
--js-runtimes deno:/Applications/YouPiper Helper.app/Contents/Resources/bin/deno
```

Nothing modifies `PATH`, for the Helper or for its children. Discovery is by
absolute path, so what shipped is what runs, and the Helper behaves identically
at login and from a shell. `packaging/PACKAGING.md` records why Deno rather than
the ~1 MB QuickJS.

**`/health` reports it.** `CheckDependencies` returns a `Dependencies` value
whose `Ready()` requires all three of yt-dlp, FFmpeg and the runtime, and the
endpoint publishes the third alongside the two older fields:

```json
{"status":"ok","version":"0.1.0","ytdlp_available":true,
 "ffmpeg_available":true,"js_runtime_available":true}
```

A Helper missing the runtime answers `"status":"degraded"`, which is what stops
the website offering local downloads that could only fail. This matters
independently of bundling: it is what turns a silent failure into a visible one.
`/metadata` and `/downloads` also refuse up front with `js_runtime_missing`
rather than running yt-dlp and reporting whatever exit status came back.

The regression checks for all of this — including real downloads through the
installed application, started by launchd — are
`packaging/verify-runtime.sh` (`REG-JS-001` … `REG-JS-008`).

## How to Run

```bash
# Run the companion
go run ./cmd/agent

# Show configuration, resolved tool paths and login-item state
go run ./cmd/agent -status

# Run tests
go test ./...
```

### Flags

| Flag | Effect |
|---|---|
| `-addr` | listen address (default `127.0.0.1:47821`; non-loopback hosts are refused) |
| `-output` | download directory |
| `-status` | print configuration, resolved tool paths and login-item state, then exit |
| `-install-startup` | register to start at login, then exit |
| `-uninstall` | remove the login registration, then exit |
| `-no-startup` | run without registering at login |
| `-version` | print the version, then exit |

Only a build produced by `packaging/build.sh` registers itself at login; `go run` never does. The gate is a build-time `-ldflags` constant, not a path heuristic — see `internal/autostart`.

## Packaging

`packaging/` builds the installable end-user artifacts (`YouPiper Helper.app` + `.dmg`, and `YouPiper-Helper.exe` + `.zip`), bundles the third-party tools, and handles login registration and signing. See [packaging/PACKAGING.md](packaging/PACKAGING.md) for the build steps, the bundled-software inventory and licences, the current signing/notarization status, and the test matrix.

## API Specification

- `GET /health`: Returns agent readiness and dependency availability.
- `POST /metadata`: Accepts `{ "url": "..." }`, returns video metadata and format options.
- `POST /downloads`: Accepts `{ "url": "...", "quality": "..." }`, starts async download job.
- `GET /downloads/{id}`: Returns progress and status of a download job.
- `POST /downloads/{id}/cancel`: Cancels an active download job.
