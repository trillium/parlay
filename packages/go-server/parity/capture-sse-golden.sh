#!/usr/bin/env bash
# capture-sse-golden.sh — drive the golden SSE scenario against the Go server
# and print a normalized capture of what it actually emitted, next to the
# committed golden.
#
# ── Why this script does not "refresh" the golden ───────────────────────────
# internal/handlers/testdata/sse-golden.json is a frozen capture of the
# TypeScript server, taken before that server was deleted in the Bun→Go cutover.
# This file used to re-capture it by booting that old server and running this
# scenario; it was left behind by that deletion and had been failing at its
# `cd` into the vanished directory ever since. There is no live reference server
# left to re-capture from, so no flag here writes the golden, deliberately:
# re-baselining from the Go server would make TestSSEGolden compare the Go
# server against itself, and a check that can never fail is worse than no check.
#
# What is still possible is the other half of the job — the diagnosis. This
# boots the Go server, runs the identical scenario, normalizes the result the
# same way the test normalizes its own capture, and diffs the two. That diff is
# what you want when TestSSEGolden fails: it shows the frames the Go server
# really emitted, step by step, beside the ones the test expects.
#
# The diff is a diagnosis aid, NOT a verdict. Frames differ by design — the Go
# server emits a Go-only `commands` burst frame and has no producer for the TS
# `draft`/`presence` frames, and the test applies exactly those differences as
# contract-cited transforms (see transformTSStep/transformGoStep in
# sse_golden_test.go). TestSSEGolden is the check; this is how you read it.
#
# To legitimately re-baseline, recover the reference implementation from git
# history (`git log --all --diff-filter=D -- packages/server`), boot it, and
# drive THIS scenario against it with the same boundary accounting below.
# Anything less is not a re-baseline, it is deleting the evidence.
#
# Local-only diagnostic, not a CI harness: CI's shell job runs an explicit
# whitelist of hermetic harnesses, and this stands up a real server.
# TestSSEGolden is hermetic itself and rides the go job.
#
# Usage: packages/go-server/parity/capture-sse-golden.sh [--keep]
# Requirements: go, curl, jq, bun. Run from anywhere; it locates the repo.

set -euo pipefail

KEEP=0
while [ $# -gt 0 ]; do
  case "$1" in
    --keep) KEEP=1; shift ;;
    -h|--help) awk 'NR>1 && !/^#/ {exit} NR>1 {sub(/^# ?/, ""); print}' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR" && git rev-parse --show-toplevel)"
GO_SERVER_DIR="$REPO/packages/go-server"
GOLDEN="$GO_SERVER_DIR/internal/handlers/testdata/sse-golden.json"

for tool in go curl jq bun; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 1; }
done

PORT="$(bun -e 'const s=Bun.listen({hostname:"127.0.0.1",port:0,socket:{data(){}}});const p=s.port;s.stop(true);console.log(p)')"
BASE="http://127.0.0.1:$PORT"
SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/parlay-ssegolden.XXXXXX")"
SERVER_PID=""
LEGACY_PID=""
CAPS_PID=""
POLL_PID=""

cleanup() {
  local status=$?
  # Explicit PIDs only — never a pkill pattern.
  for pid in "$LEGACY_PID" "$CAPS_PID" "$POLL_PID" "$SERVER_PID"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  if [ "$KEEP" = "1" ]; then
    echo "sandbox kept: $SANDBOX"
  else
    # Only ever remove a directory this script created under the temp root.
    case "$SANDBOX" in
      */parlay-ssegolden.*) rm -rf "$SANDBOX" ;;
      *) echo "refusing to remove unexpected path: $SANDBOX" >&2 ;;
    esac
  fi
  exit $status
}
trap cleanup EXIT

say() { printf '\033[1m== %s\033[0m\n' "$*"; }
fail() {
  echo "FAIL: $1" >&2
  echo "--- legacy capture ---" >&2; cat "$SANDBOX/legacy.raw" >&2 || true
  echo "--- caps capture ---"   >&2; cat "$SANDBOX/caps.raw"   >&2 || true
  echo "--- server log ---"     >&2; cat "$SANDBOX/server.log" >&2 || true
  exit 1
}

# ── 1. Boot the Go server in the sandbox ───────────────────────────────────
# HOME and -state-dir/-pai-dir are all pinned into the sandbox, on the same
# isolation recipe as examples/bootstrap-sandbox.sh. The port is kernel-picked
# and refused if somebody else already holds it, so this can never adopt (or be
# confused by) a running production server.
say "starting packages/go-server on port $PORT (sandbox: $SANDBOX)"
mkdir -p "$SANDBOX/.parlay" "$SANDBOX/pai" "$SANDBOX/bin"
if bun -e "try{Bun.listen({hostname:'127.0.0.1',port:$PORT,socket:{data(){}}}).stop(true);process.exit(1)}catch{process.exit(0)}"; then
  echo "port $PORT is already in use; refusing to run against a server this script did not start" >&2
  exit 1
fi
# CGO off: the server is pure standard library, so this only avoids dragging a
# local C toolchain into a diagnostic. `exec` inside the subshell makes $! the
# server itself rather than a wrapper.
(
  cd "$GO_SERVER_DIR"
  CGO_ENABLED=0 go build -o "$SANDBOX/bin/parlay-server" ./cmd/parlay-server || exit 1
  exec env HOME="$SANDBOX" \
    PAI_DIR="$SANDBOX/pai" \
    "$SANDBOX/bin/parlay-server" -addr "127.0.0.1:$PORT" -state-dir "$SANDBOX/.parlay" -pai-dir "$SANDBOX/pai"
) > "$SANDBOX/server.log" 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 120); do
  kill -0 "$SERVER_PID" 2>/dev/null || fail "server exited before becoming ready"
  curl -fsS -m 1 "$BASE/api/chat/agents" >/dev/null 2>&1 && break
  sleep 0.25
done
kill -0 "$SERVER_PID" 2>/dev/null || fail "server is no longer running"
curl -fsS -m 2 "$BASE/api/chat/agents" >/dev/null || fail "server did not come up"

# ── 2. Open both SSE captures ────────────────────────────────────────────────
# The declaration accepts `navigate` only, so the scenario's reload broadcast
# must be suppressed on this stream — the capability-parity half of the golden.
CAPS_DECL='{"schema":"1.0.0","surface":{"kind":"golden_capture"},"accepts":{"navigate":{}}}'
CAPS_ENC="$(jq -rn --arg s "$CAPS_DECL" '$s|@uri')"
: > "$SANDBOX/legacy.raw"
: > "$SANDBOX/caps.raw"
curl -Ns -m 60 "$BASE/api/chat/events" >> "$SANDBOX/legacy.raw" &
LEGACY_PID=$!
curl -Ns -m 60 "$BASE/api/chat/events?caps=$CAPS_ENC" >> "$SANDBOX/caps.raw" &
CAPS_PID=$!

# frames counts ONLY the events the golden models, which is what makes the step
# boundaries recorded below comparable with the committed file:
#   presence_map — the TS server's 10s sweep rebroadcast, wall-clock
#     nondeterministic, excluded by the capture pipeline
#     (capture-to-golden.ts) and by transformTSStep/transformGoStep.
#   commands — Go-only burst snapshot (api-contract.md ledger row 29), dropped
#     by transformGoStep, so it is not in the golden either.
# Counting the raw wire instead would put every boundary one frame past the
# committed one and capture-to-golden.ts would reject the capture as "a frame
# arrived outside the scenario".
frames() { grep '^event: ' "$1" 2>/dev/null | grep -vE '^event: (presence_map|commands)$' | wc -l | tr -d ' '; }
# Wait until both captures hold at least the given frame counts. Never an
# elapsed-time assertion — a deadline on "did the expected frames arrive".
await() { # await <legacy_count> <caps_count> <what>
  for _ in $(seq 1 80); do
    [ "$(frames "$SANDBOX/legacy.raw")" -ge "$1" ] && [ "$(frames "$SANDBOX/caps.raw")" -ge "$2" ] && return 0
    sleep 0.25
  done
  fail "timed out waiting for $3 (want legacy>=$1 caps>=$2, have $(frames "$SANDBOX/legacy.raw")/$(frames "$SANDBOX/caps.raw"))"
}
# Step boundaries, recorded as cumulative frame counts per stream. settle
# sleeps give late same-step frames time to land in the right slice; a frame
# that still leaks lands in the NEXT step's slice and fails the comparison
# loudly rather than silently shifting everything.
LEGACY_BOUNDS=""
CAPS_BOUNDS=""
mark() {
  sleep 0.4
  LEGACY_BOUNDS="${LEGACY_BOUNDS:+$LEGACY_BOUNDS,}$(frames "$SANDBOX/legacy.raw")"
  CAPS_BOUNDS="${CAPS_BOUNDS:+$CAPS_BOUNDS,}$(frames "$SANDBOX/caps.raw")"
}

post() { curl -fsS -m 5 -X POST -H 'Content-Type: application/json' -d "$2" "$BASE$1" >/dev/null || fail "POST $1 failed"; }

# ── 3. Drive the scenario ────────────────────────────────────────────────────
# The step names and order must match sseGoldenSteps in sse_golden_test.go — the
# test cross-checks them against the golden before comparing anything. The
# minimum counts below are the committed golden's own step sizes, expressed
# cumulatively; they are minimums because the Go server legitimately adds the
# excluded frames above.
say "scenario: connect-burst"
await 4 4 "connect bursts"      # connected, history, agents, agent_presence
mark

say "scenario: register-agent"
post /api/chat/register-agent '{"id":"golden","name":"Golden","color":"#3FB950"}'
await 5 5 "agent_register broadcast"
mark

say "scenario: poll-park"
curl -Ns -m 25 "$BASE/api/chat/poll?channel=golden" > "$SANDBOX/poll.out" &
POLL_PID=$!
await 6 6 "poll-park broadcast"  # agent_presence true
mark

say "scenario: send"
post /api/chat/send '{"text":"golden message","toAgent":"golden"}'
await 9 9 "send broadcasts"    # message, message_received, agent_presence on both streams: the Go server has no producer for the TS draft/presence frames, so both clients get the same three
wait "$POLL_PID" || fail "parked poll did not receive the sent message"
POLL_PID=""
grep -q '"golden message"' "$SANDBOX/poll.out" || fail "poll response did not carry the sent message"
mark

say "scenario: reload"
post /api/chat/reload '{}'
await 10 9 "reload broadcast"   # legacy only — suppressed for the caps client (accepts navigate, not reload)
mark

say "scenario: unregister"
post /api/chat/unregister '{"id":"golden"}'
await 11 10 "agent_unregister broadcast"
mark

kill "$LEGACY_PID" "$CAPS_PID" 2>/dev/null || true
wait "$LEGACY_PID" 2>/dev/null || true
wait "$CAPS_PID" 2>/dev/null || true
LEGACY_PID=""; CAPS_PID=""

# ── 4. Normalize, and diff against the frozen golden ───────────────────────
say "normalizing the capture (capture-to-golden.ts)"
PROPOSED="$SANDBOX/capture.json"
bun "$SCRIPT_DIR/capture-to-golden.ts" \
  "$SANDBOX/legacy.raw" "$LEGACY_BOUNDS" \
  "$SANDBOX/caps.raw" "$CAPS_BOUNDS" \
  > "$PROPOSED" || fail "capture-to-golden.ts failed (the step boundaries above are the thing to check)"

if [ -f "$GOLDEN" ]; then
  say "diff: committed golden vs this capture"
  if diff -u "$GOLDEN" "$PROPOSED"; then
    echo "identical — the Go server still reproduces the frozen TypeScript capture frame for frame"
  else
    echo
    echo "^^ differences are diagnosis material, not a failure: the Go-only frames and the"
    echo "   absent TS frames are exactly the ones the test's transforms drop. Read them"
    echo "   against transformTSStep/transformGoStep and the api-contract.md ledger."
  fi
  # The structural diff above moves whole slices when a step's frame count
  # changes, which buries the interesting part. This second view is one line
  # per step with the event names in order, so a dropped or added frame is
  # visible at a glance — it is the form to read first.
  say "diff: per-step event sequence (the readable one)"
  summarize() {
    jq -r '(.steps|length) as $n | range(0;$n) as $i |
      "step \($i) \(.steps[$i]): legacy=[\(.legacy[$i]|map(.event)|join(" "))] caps=[\(.caps[$i]|map(.event)|join(" "))]"' "$1"
  }
  summarize "$GOLDEN" > "$SANDBOX/golden.events"
  summarize "$PROPOSED" > "$SANDBOX/capture.events"
  diff -u "$SANDBOX/golden.events" "$SANDBOX/capture.events" || true
else
  echo "no committed golden at $GOLDEN — nothing to diff against" >&2
fi
echo "normalized capture: $PROPOSED"

# ── 5. The check itself ────────────────────────────────────────────────────
say "verifying: go test -run TestSSEGolden"
(cd "$GO_SERVER_DIR" && go test ./internal/handlers/ -run TestSSEGolden -count=1)
say "done"
