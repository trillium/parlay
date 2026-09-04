#!/usr/bin/env bash
# build-gc.sh — build the pinned Gas City `gc` binary from source.
#
# The pin lives in third_party/gascity/PIN (full commit sha; currently
# ac6c9c685, upstream main as of 2026-08-30). Never build the captain's local
# ~/code/gascity HEAD: that branch (progname/monolith) does not compile — see
# docs/gascity-integration-contract.md §1 for the verified evidence.
#
# Source selection, in order:
#   1. $GC_SRC          — path to an existing gascity clone containing the pin.
#   2. ~/code/gascity   — used read-only via `git archive` (never fetch/checkout
#                         there; archive reads the object store and writes only
#                         to the workdir).
#   3. network clone    — shallow-fetch exactly the pinned commit from
#                         $GC_SRC_URL (default github.com/gastownhall/gascity).
#
# Build mode: CGO_ENABLED=0 by default — it sidesteps the ICU cgo toolchain
# entirely and is the mode CI verifies. Pass --cgo to build with cgo; the ICU
# include/lib flags are then YOUR job (docs/gc-prerequisite.md has the recipe
# for this box's keg-only Homebrew icu4c).
set -euo pipefail

usage() {
  cat <<'EOF'
usage: build-gc.sh [--out PATH] [--cgo]

  --out PATH  where to write the built binary (default tools/gc-build/dist/gc)
  --cgo       build with cgo enabled (caller supplies ICU flags; see
              docs/gc-prerequisite.md). Default is CGO_ENABLED=0.
EOF
}

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/tools/gc-build/dist/gc"
CGO=0

while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --cgo) CGO=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "build-gc.sh: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

PIN="$(tr -d '[:space:]' < "$ROOT/third_party/gascity/PIN")"
case "$PIN" in
  *[!0-9a-f]*|'') echo "build-gc.sh: third_party/gascity/PIN is not a commit sha: '$PIN'" >&2; exit 1 ;;
esac
if [ "${#PIN}" -ne 40 ]; then
  echo "build-gc.sh: third_party/gascity/PIN must be a full 40-char sha, got ${#PIN} chars" >&2
  exit 1
fi

GC_SRC_URL="${GC_SRC_URL:-https://github.com/gastownhall/gascity}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/gc-build.XXXXXX")"
SRCDIR="$WORK/src"
mkdir -p "$SRCDIR"

# has_pin CLONE — true if CLONE is a git repo whose object store has the pin.
has_pin() {
  git -C "$1" cat-file -e "$PIN^{commit}" 2>/dev/null
}

materialized=""
if [ -n "${GC_SRC:-}" ]; then
  if ! has_pin "$GC_SRC"; then
    echo "build-gc.sh: \$GC_SRC=$GC_SRC does not contain pinned commit $PIN" >&2
    exit 1
  fi
  git -C "$GC_SRC" archive "$PIN" | tar -x -C "$SRCDIR"
  materialized="git archive from \$GC_SRC ($GC_SRC)"
elif has_pin "$HOME/code/gascity"; then
  # Read-only use of the local clone: archive reads objects, writes nothing.
  git -C "$HOME/code/gascity" archive "$PIN" | tar -x -C "$SRCDIR"
  materialized="git archive from ~/code/gascity (read-only)"
else
  echo "build-gc.sh: fetching $PIN from $GC_SRC_URL …"
  git -C "$SRCDIR" init -q
  git -C "$SRCDIR" remote add origin "$GC_SRC_URL"
  git -C "$SRCDIR" fetch -q --depth 1 origin "$PIN"
  git -C "$SRCDIR" -c advice.detachedHead=false checkout -q FETCH_HEAD
  materialized="shallow fetch from $GC_SRC_URL"
fi
echo "build-gc.sh: source at pin $PIN — $materialized"

mkdir -p "$(dirname "$OUT")"
if [ "$CGO" = 1 ]; then
  echo "build-gc.sh: building with cgo (ICU flags are the caller's responsibility)"
  (cd "$SRCDIR" && go build -o "$OUT" ./cmd/gc)
else
  (cd "$SRCDIR" && CGO_ENABLED=0 go build -o "$OUT" ./cmd/gc)
fi

# Smoke: the binary must run, and must speak the typed --json contract even
# when refusing (outside a city it refuses with {"schema_version":…} on
# stdout — docs/gascity-integration-contract.md §5). GC_HOME and the
# supervisor port are redirected per contract §9.1; `version` and
# `config show` contact no supervisor, the redirect is belt and braces.
SMOKE_HOME="$WORK/gchome"
mkdir -p "$SMOKE_HOME"
printf '[supervisor]\nport = 18372\n' > "$SMOKE_HOME/supervisor.toml"
VERSION="$(GC_HOME="$SMOKE_HOME" "$OUT" version)" || {
  echo "build-gc.sh: built binary does not run: $OUT version failed" >&2; exit 1;
}
PROBE="$(cd "$WORK" && GC_HOME="$SMOKE_HOME" "$OUT" config show --json 2>/dev/null)" || true
case "$PROBE" in
  *'"schema_version"'*) ;;
  *)
    echo "build-gc.sh: built binary did not emit the typed --json contract" >&2
    echo "  probe stdout: $PROBE" >&2
    exit 1
    ;;
esac

echo "build-gc.sh: OK — $OUT (version: $VERSION, pin: $PIN)"
echo "build-gc.sh: note — the captain's interactive shell aliases 'gc' to 'git commit';"
echo "  when running by hand, always use the absolute path: $OUT"
