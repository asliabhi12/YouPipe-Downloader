#!/usr/bin/env bash
#
# YouPiper's single verification command.
#
# Runs everything that can be checked without a network, a running Helper or a
# browser: the Go Helper's own suite (plain and under the race detector), go vet,
# the frontend Helper-availability and routing suite, and the production Astro
# build.
#
# Usage:
#   ./test.sh          run everything
#   ./test.sh go       Go only
#   ./test.sh web      frontend tests and build only
#   ./test.sh pkg      packaging checks only
#
# Live checks that need a real Helper and a real video are deliberately not here
# — they cannot pass in CI and a suite that is expected to fail is a suite nobody
# reads. Run those with local/packaging/verify-runtime.sh; see
# local/packaging/PACKAGING.md.

set -u -o pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

TARGET="${1:-all}"

failed=()
passed=()

bold=$'\033[1m'
red=$'\033[31m'
green=$'\033[32m'
dim=$'\033[2m'
reset=$'\033[0m'

# run NAME DIR CMD...  — reports one step, keeps going after a failure so a
# single break does not hide the state of everything after it.
run() {
  local name="$1" dir="$2"
  shift 2
  printf '\n%s==> %s%s %s(%s)%s\n' "$bold" "$name" "$reset" "$dim" "$*" "$reset"
  if (cd "$dir" && "$@"); then
    passed+=("$name")
  else
    printf '%s!! %s failed%s\n' "$red" "$name" "$reset"
    failed+=("$name")
  fi
}

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf '%s!! %s is required but was not found on PATH%s\n' "$red" "$1" "$reset"
    failed+=("$1 missing")
    return 1
  fi
}

if [[ "$TARGET" == all || "$TARGET" == go ]]; then
  if require go; then
    run "go vet"        local go vet ./...
    run "go test"       local go test ./...
    run "go test -race" local go test -race ./...
  fi
fi

if [[ "$TARGET" == all || "$TARGET" == pkg ]]; then
  # The offline half of the bundled-runtime regression checks: that a runtime is
  # pinned for every platform, and that the installed application carries one and
  # finds it without the developer's PATH. Anything needing the network or a real
  # video reports SKIP here and is run by hand.
  run "packaging runtime" local/packaging ./verify-runtime.sh --offline
fi

if [[ "$TARGET" == all || "$TARGET" == web ]]; then
  if require node; then
    # Node runs the TypeScript tests directly, so there is nothing to install
    # before this step.
    run "web tests" web npm test --silent
  fi
  if require npm; then
    if [[ -d web/node_modules ]]; then
      run "astro build" web npm run build --silent
    else
      printf '\n%s==> astro build%s %s(skipped: run `npm install` in web/ first)%s\n' \
        "$bold" "$reset" "$dim" "$reset"
    fi
  fi
fi

printf '\n%s──────── summary ────────%s\n' "$bold" "$reset"
for name in "${passed[@]+"${passed[@]}"}"; do
  printf '%s  ok  %s%s\n' "$green" "$name" "$reset"
done
for name in "${failed[@]+"${failed[@]}"}"; do
  printf '%s  FAIL %s%s\n' "$red" "$name" "$reset"
done

if ((${#failed[@]} > 0)); then
  printf '\n%s%d step(s) failed%s\n' "$red" "${#failed[@]}" "$reset"
  exit 1
fi

printf '\n%sall %d step(s) passed%s\n' "$green" "${#passed[@]}" "$reset"
