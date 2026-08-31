#!/usr/bin/env bash
# refresh-sse-golden.sh — regenerate the TS-server SSE golden capture that
# TestSSEGolden (internal/handlers/sse_golden_test.go) verifies the Go server
# against.
#
# This is a LOCAL-ONLY refresh tool, not a CI harness: it boots the real
# TypeScript server (packages/server) in a throwaway sandbox, drives a fixed
# scenario over two live SSE connections (one legacy, one with a ?caps=
# capability declaration), and writes the normalized frame capture to
# internal/handlers/testdata/sse-golden.json. CI's shell job runs an explicit
# whitelist of hermetic harnesses; this script is deliberately not on it —
# the committed golden is what CI sees, via the plain hermetic Go test.
#
# Isolation follows examples/bootstrap-sandbox.sh: HOME, PARLAY_DATA_DIR,
# PARLAY_STATE_HOME and PAI_DIR all point into a mktemp sandbox (the TS
# server's TTS/tailer paths write under $PAI_DIR and ~/…, not just the data
# dir), the port is kernel-picked and refused if somebody else already holds
# it, and PID liveness is checked alongside reachability so a dead server is
# never silently replaced by whatever answers the port.
#
# Requirements: bun, curl, jq, go. Run from anywhere.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR" && git rev-parse --show-toplevel)"
GO_SERVER_DIR="$REPO/packages/go-server"
GOLDEN="$GO_SERVER_DIR/internal/handlers/testdata/sse-golden.json"

for tool in bun curl jq go; do
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
  # Only ever remove a directory this script created under the temp root.
  case "$SANDBOX" in
    */parlay-ssegolden.*) rm -rf "$SANDBOX" ;;
    *) echo "refusing to remove unexpected path: $SANDBOX" >&2 ;;
  esac
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

# ── 1. Boot the TS server in the sandbox ─────────────────────────────────────
say "starting packages/server on port $PORT (sandbox: $SANDBOX)"
mkdir -p "$SANDBOX/data" "$SANDBOX/pai" "$SANDBOX/.parlay"
if bun -e "try{Bun.listen({hostname:'127.0.0.1',port:$PORT,socket:{data(){}}}).stop(true);process.exit(1)}catch{process.exit(0)}"; then
  echo "port $PORT is already in use; refusing to run against a server this script did not start" >&2
  exit 1
fi
# `exec` is load-bearing: it replaces the subshell so $! is the server itself
# (see examples/bootstrap-sandbox.sh for the macOS bash 3.2 story).
(
  cd "$REPO/packages/server"
  exec env HOME="$SANDBOX" \
    PARLAY_DATA_DIR="$SANDBOX/data" \
    PARLAY_STATE_HOME="$SANDBOX/.parlay" \
    PAI_DIR="$SANDBOX/pai" \
    PARLAY_PORT="$PORT" \
    bun src/index.ts
) > "$SANDBOX/server.log" 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 40); do
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

# presence_map is excluded from frame counting (and from the golden, see
# capture-to-golden.ts): the TS server rebroadcasts it from a 10s sweep timer
# (packages/server/src/sse.ts) whose first tick fires even without a change,
# so its arrivals are wall-clock-nondeterministic — golden poison. The Go
# burst's presence_map placement is pinned by its own unit tests
# (events_presence_test.go) instead.
frames() { grep '^event: ' "$1" 2>/dev/null | grep -vc '^event: presence_map$' || true; }
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
# Step names/order must match sseGoldenSteps in sse_golden_test.go — the test
# cross-checks them against the golden before comparing anything.
say "scenario: connect-burst"
await 4 4 "connect bursts"      # TS burst minus presence_map: connected, history, agents, agent_presence
mark

say "scenario: register-agent"
post /api/chat/register-agent '{"id":"golden","name":"Golden","color":"#3FB950"}'
await 5 5 "agent_register broadcast"
mark

say "scenario: poll-park"
curl -Ns -m 25 "$BASE/api/chat/poll?channel=golden" > "$SANDBOX/poll.out" &
POLL_PID=$!
await 6 6 "poll-park broadcast"  # agent_presence true (the presence_map rebroadcast is excluded)
mark

say "scenario: send"
post /api/chat/send '{"text":"golden message","toAgent":"golden"}'
await 11 10 "send broadcasts"    # TS legacy: message, message_received, agent_presence, draft, presence — draft is a gated presentation command, so the caps client (accepts navigate only) gets 4
wait "$POLL_PID" || fail "parked poll did not receive the sent message"
POLL_PID=""
grep -q '"golden message"' "$SANDBOX/poll.out" || fail "poll response did not carry the sent message"
mark

say "scenario: reload"
post /api/chat/reload '{}'
await 12 10 "reload broadcast"   # legacy only — suppressed for the caps client (accepts navigate, not reload)
mark

say "scenario: unregister"
post /api/chat/unregister '{"id":"golden"}'
await 13 11 "agent_unregister broadcast"
mark

kill "$LEGACY_PID" "$CAPS_PID" 2>/dev/null || true
wait "$LEGACY_PID" 2>/dev/null || true
wait "$CAPS_PID" 2>/dev/null || true
LEGACY_PID=""; CAPS_PID=""

# ── 4. Parse, normalize, write the golden ────────────────────────────────────
say "writing $GOLDEN"
mkdir -p "$(dirname "$GOLDEN")"
bun "$SCRIPT_DIR/capture-to-golden.ts" \
  "$SANDBOX/legacy.raw" "$LEGACY_BOUNDS" \
  "$SANDBOX/caps.raw" "$CAPS_BOUNDS" \
  > "$GOLDEN" || fail "capture-to-golden.ts failed"

# ── 5. Prove the fresh golden passes against the Go server ───────────────────
say "verifying: go test -run TestSSEGolden"
(cd "$GO_SERVER_DIR" && go test ./internal/handlers/ -run TestSSEGolden -count=1)
say "golden refreshed and verified"
