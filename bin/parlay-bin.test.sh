#!/usr/bin/env bash
# Regression harness for bin/parlay-bin (docs/scope-go-spawn.md Stage 4: PATH
# activation) — the build-if-stale-then-exec wrapper for tools/parlay-bin,
# mirroring bin/parlay's wrapper for tools/cli.
#
# This harness never invokes a real Go toolchain: the "shell" CI job
# (.github/workflows/ci.yml) only guarantees git/jq/curl/python3 on PATH, not
# go. Instead it puts a stub `go` on PATH that records its argv and, for a
# `build -o <path> .` invocation, writes a trivial recording shell script to
# <path> in its place — so "did the wrapper build?" and "did it exec the
# resulting binary, forwarding argv?" are both observable without a real
# compile. The wrapper is copied into a scratch tree (not run in place) so its
# own symlink-following REPO-root resolution is exercised against a throwaway
# tools/parlay-bin, never this repo's real one.

set -u

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
WRAPPER_SRC="$REPO_ROOT/bin/parlay-bin"

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ok   $1"; }
bad() { FAIL=$((FAIL + 1)); echo "  FAIL $1"; }
eq() { if [[ "$2" == "$3" ]]; then ok "$1"; else bad "$1 — want [$3], got [$2]"; fi; }

SCRATCH="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/parlay-bin-wrapper-test.XXXXXX")" && pwd -P)"
trap 'rm -rf "$SCRATCH"' EXIT

SCRATCH_REPO="$SCRATCH/repo"
mkdir -p "$SCRATCH_REPO/bin" "$SCRATCH_REPO/tools/parlay-bin"
cp "$WRAPPER_SRC" "$SCRATCH_REPO/bin/parlay-bin"
chmod +x "$SCRATCH_REPO/bin/parlay-bin"
echo 'package main' > "$SCRATCH_REPO/tools/parlay-bin/main.go"

STUBDIR="$SCRATCH/stub-path"
mkdir -p "$STUBDIR"
GO_STUB_LOG="$SCRATCH/go-calls.log"
: > "$GO_STUB_LOG"
cat > "$STUBDIR/go" <<'STUB'
#!/bin/sh
echo "$*" >> "$GO_STUB_LOG"
prev=""
out=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out="$a"; fi
  prev="$a"
done
if [ -n "$out" ]; then
  mkdir -p "$(dirname "$out")"
  printf '#!/bin/sh\necho "fake-parlay-bin ran: $*"\n' > "$out"
  chmod +x "$out"
fi
exit "${GO_STUB_EXIT:-0}"
STUB
chmod +x "$STUBDIR/go"

GO_BIN="$SCRATCH_REPO/tools/parlay-bin/bin/parlay-bin"

run_wrapper() {
  ( PATH="$STUBDIR:$PATH" GO_STUB_LOG="$GO_STUB_LOG" GO_STUB_EXIT="${1:-0}" \
    "$SCRATCH_REPO/bin/parlay-bin" spawn some-id "Some Name" "#abcdef" "task" )
}

echo "1. fresh build: no existing binary"
out="$(run_wrapper)"
rc=$?
eq "fresh build exits 0" "$rc" "0"
eq "fresh build invoked go build" "$(wc -l < "$GO_STUB_LOG" | tr -d ' ')" "1"
eq "fresh build execs the result, forwarding argv" "$out" "fake-parlay-bin ran: spawn some-id Some Name #abcdef task"

echo "2. up-to-date binary: no rebuild"
: > "$GO_STUB_LOG"
out="$(run_wrapper)"
rc=$?
eq "up-to-date exits 0" "$rc" "0"
eq "up-to-date does not invoke go build again" "$(wc -l < "$GO_STUB_LOG" | tr -d ' ')" "0"
eq "up-to-date still execs the existing binary" "$out" "fake-parlay-bin ran: spawn some-id Some Name #abcdef task"

echo "3. stale binary: source newer than binary triggers a rebuild"
: > "$GO_STUB_LOG"
sleep 1.1
touch "$SCRATCH_REPO/tools/parlay-bin/main.go"
out="$(run_wrapper)"
rc=$?
eq "stale rebuild exits 0" "$rc" "0"
eq "stale rebuild invoked go build" "$(wc -l < "$GO_STUB_LOG" | tr -d ' ')" "1"

echo "4. go build failure: wrapper exits 1 and does not exec"
rm -f "$GO_BIN"
: > "$GO_STUB_LOG"
out="$(run_wrapper 1)"
rc=$?
eq "build failure propagates a nonzero exit" "$rc" "1"
eq "build failure produced no output (never execs)" "$out" ""

echo
echo "$PASS passed, $FAIL failed"
[[ "$FAIL" -eq 0 ]]
