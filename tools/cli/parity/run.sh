#!/usr/bin/env bash
# TS-vs-Go CLI parity harness (ticket B10 part 2).
#
# Builds packages/go-server as a disposable fixture backend (the already
# API-contract-compliant Go rewrite of the chat server — see this repo's
# top-level CLAUDE.md for what it covers as of C0-C3) and the tools/cli Go
# CLI binary, then runs a representative set of commands through both the
# TS CLI (`bun packages/cli/src/index.ts`) and the Go CLI against that same
# fixture, diffing stdout+stderr+exit code.
#
# Safety: never touches the real ~/.parlay or the production :31337 port.
# HOME is redirected to a scratch dir for BOTH CLIs, which also safely
# scopes the hardcoded ~/.parlay/agents|worktrees paths used by
# guard/teardown/variant/launch (none of those honor $PARLAY_STATE_HOME —
# see this repo's CLAUDE.md, ticket B4/B9 notes). The fixture server refuses
# to bind :31337 itself (packages/go-server/cmd/parlay-server's
# refuseProductionPort).
#
# Usage: tools/cli/parity/run.sh [-v]
#   -v   also print each diff inline instead of only writing diffs.log

set -uo pipefail

VERBOSE=0
[ "${1:-}" = "-v" ] && VERBOSE=1

REPO="$(cd -P "$(dirname "${BASH_SOURCE[0]}")/../../.." >/dev/null 2>&1 && pwd)"
WORK="$(mktemp -d)"
SERVER_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

FIXTURE_ADDR="127.0.0.1:24242"
FIXTURE_URL="http://$FIXTURE_ADDR"
STATE_DIR="$WORK/server-state"
FAKE_HOME="$WORK/home"
mkdir -p "$STATE_DIR" "$FAKE_HOME"

echo "== building fixture server (packages/go-server) =="
go build -C "$REPO/packages/go-server" -o "$WORK/parlay-fixture-server" ./cmd/parlay-server || exit 1

echo "== building Go CLI (tools/cli) =="
go build -C "$REPO/tools/cli" -o "$WORK/parlay-go" . || exit 1

echo "== starting fixture server on $FIXTURE_URL (state: $STATE_DIR) =="
"$WORK/parlay-fixture-server" -addr "$FIXTURE_ADDR" -state-dir "$STATE_DIR" >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
ready=0
for _ in $(seq 1 40); do
  if curl -sf "$FIXTURE_URL/health" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.25
done
if [ "$ready" -ne 1 ]; then
  echo "fixture server did not come up; log:"
  cat "$WORK/server.log"
  exit 1
fi

DIFF_LOG="$WORK/diffs.log"
: >"$DIFF_LOG"

# Portable stand-in for GNU `timeout` (absent on stock macOS, no
# `gtimeout` either in this environment): perl's alarm+exec kills the child
# after N seconds. Needed for the long-running daemons in the command list
# below (robots-watch/robots-tail without --once would otherwise hang the
# harness forever; --once is used here, but this stays as a safety net).
ptimeout() {
  local secs="$1"; shift
  perl -e 'alarm shift @ARGV; exec @ARGV or die "exec: $!"' "$secs" "$@"
}

run_ts() {
  HOME="$FAKE_HOME" PARLAY_SERVER="$FIXTURE_URL" PARLAY_AGENT_ID="parity-agent" \
  PARLAY_STATE_HOME="$FAKE_HOME/.parlay" \
    ptimeout 8 bun "$REPO/packages/cli/src/index.ts" "$@"
}
run_go() {
  HOME="$FAKE_HOME" PARLAY_SERVER="$FIXTURE_URL" PARLAY_AGENT_ID="parity-agent" \
  PARLAY_STATE_HOME="$FAKE_HOME/.parlay" \
    ptimeout 8 "$WORK/parlay-go" "$@"
}

# Strip fields that legitimately vary between two independent process runs
# (timestamps, PIDs, message ids assigned by the one shared fixture server
# both CLIs write to sequentially) plus wording differences between Go's
# net/http and Bun/JS's fetch for the identical underlying condition
# (connection refused to an intentionally-absent service), so real
# behavioral mismatches aren't drowned in noise.
normalize() {
  sed -E \
    -e 's/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?Z?/<TIMESTAMP>/g' \
    -e 's/\b[0-9]{2}:[0-9]{2}:[0-9]{2}\b/<TIME>/g' \
    -e 's/\bpid[= ][0-9]+/pid=<PID>/gI' \
    -e 's/\(id m[0-9a-z]+\)/(id <MSGID>)/g' \
    -e 's/Error: Unable to connect\. Is the computer able to access the url\?/<CONN_REFUSED>/g' \
    -e 's/dial tcp [^:]+:[0-9]+: connect: connection refused/<CONN_REFUSED>/g' \
    -e 's/Get "[^"]+": <CONN_REFUSED>/<CONN_REFUSED>/g'
}

# --- Go-only verbs ------------------------------------------------------
# Verbs that exist ONLY in the Go CLI, deliberately and permanently: they
# were written after `bin/parlay` started exec'ing the Go binary for
# everything but `lavish-import`, so `packages/cli` (the retired path) was
# never going to grow them. See this repo's CLAUDE.md — each verb's own
# section says "Go-only, no TS port … keep it out of tools/cli/parity/run.sh".
#
# Keeping them out of the CHECK list is not enough, though: `parlay help`
# prints the whole usage block, so every one of these verbs' usage lines
# shows up as a diff on the four help cases below — which is exactly how
# this harness came to report 4 FAILs against a CLI with no defect
# (robots-xaxt). The lines are filtered out of the GO side only, and
# `audit_go_only_verbs` below pins that the list stays honest: a verb that
# vanishes from Go's usage, or that grows a TS side, FAILs rather than
# silently muting a real divergence.
#
# ADDING A GO-ONLY VERB: append it here as well as documenting it in
# CLAUDE.md. Anything not listed here still diffs normally, so a verb that
# was merely FORGOTTEN on the TS side keeps failing the harness, which is
# the point.
GO_ONLY_VERBS=(claim merge-gate branch-audit sweep)

go_only_usage_re() {
  local IFS='|'
  printf '^  parlay (%s)([^A-Za-z0-9-]|$)' "${GO_ONLY_VERBS[*]}"
}

# Drop the usage lines documenting Go-only verbs. Applied to the Go output
# only — if TS ever prints one of these lines the diff must fail, since that
# means the verb gained a TS side and belongs out of GO_ONLY_VERBS.
strip_go_only_usage() {
  # A filter must never be able to fail its caller: grep exits 1 when it
  # emits nothing, which is a legitimate result here (robots-dcag).
  grep -Ev "$(go_only_usage_re)" || true
}

PASS=0
FAIL=0
SKIP=0
declare -a SUMMARY

check() {
  local label="$1"; shift
  local ts_out ts_code go_out go_code ts_norm go_norm
  ts_out="$(run_ts "$@" 2>&1)"; ts_code=$?
  go_out="$(run_go "$@" 2>&1)"; go_code=$?
  ts_norm="$(printf '%s' "$ts_out" | normalize)"
  go_norm="$(printf '%s' "$go_out" | normalize | strip_go_only_usage)"
  if [ "$ts_norm" = "$go_norm" ] && [ "$ts_code" = "$go_code" ]; then
    SUMMARY+=("PASS  $label")
    PASS=$((PASS + 1))
  else
    SUMMARY+=("FAIL  $label  (exit ts=$ts_code go=$go_code)")
    FAIL=$((FAIL + 1))
    {
      echo "=== $label ==="
      echo "args: $*"
      echo "--- ts (exit $ts_code) ---"
      echo "$ts_out"
      echo "--- go (exit $go_code) ---"
      echo "$go_out"
      echo
    } >>"$DIFF_LOG"
    if [ "$VERBOSE" -eq 1 ]; then
      tail -n 20 "$DIFF_LOG"
    fi
  fi
}

skip() {
  SUMMARY+=("SKIP  $1  ($2)")
  SKIP=$((SKIP + 1))
}

# Keep GO_ONLY_VERBS honest. Filtering a line out of the diff is only safe
# while the reason for filtering it still holds, so assert both directions
# per verb against the two CLIs' real `help` output:
#   - Go must still document it — otherwise the entry is stale and the
#     filter is muting nothing (or, worse, is about to mute a real line);
#   - TS must NOT document it — a TS side means the verb is no longer
#     Go-only and belongs in the ordinary check list instead.
audit_go_only_verbs() {
  local ts_usage go_usage verb
  ts_usage="$(run_ts help 2>&1)"
  go_usage="$(run_go help 2>&1)"
  for verb in "${GO_ONLY_VERBS[@]}"; do
    if ! printf '%s\n' "$go_usage" | grep -Eq "^  parlay $verb([^A-Za-z0-9-]|$)"; then
      SUMMARY+=("FAIL  go-only verb '$verb'  (no longer in Go's usage — stale GO_ONLY_VERBS entry)")
      FAIL=$((FAIL + 1))
    elif printf '%s\n' "$ts_usage" | grep -Eq "^  parlay $verb([^A-Za-z0-9-]|$)"; then
      SUMMARY+=("FAIL  go-only verb '$verb'  (now in TS's usage too — drop it from GO_ONLY_VERBS and add real checks)")
      FAIL=$((FAIL + 1))
    else
      SUMMARY+=("GO-ONLY  $verb  (no TS side by design; usage line filtered from the help diffs)")
    fi
  done
}

# --- representative command surface -----------------------------------
audit_go_only_verbs
check "help (usage)" help
check "help status" help status
check "help agents" help agents
check "help doctor" help doctor
check "bare (fleet snapshot)"
check "subscribers" subscribers
check "subscribers --full" subscribers --full
check "agents" agents
check "agents --full" agents --full
check "remote (default)" remote
check "remote set" remote set "http://example.invalid:9"
check "remote (after set)" remote
check "remote clear" remote clear
check "stats" stats
check "history" history
check "history --full" history --full
check "send (list)" send
check "doctor" doctor
check "health" health
check "context-check ok" context-check 50
check "context-check rotate" context-check 90
check "context-check bad" context-check abc
check "status (read, empty)" status
check "status working" status working "parity harness note"
check "status (read, after write)" status
check "crew-state" crew-state parity-agent
check "supervise" supervise parity-agent
check "agent-down" agent-down some-nonexistent-agent
check "nickname" nickname parity-nick
check "identity (read, empty)" identity
check "identity --reap-ephemeral --dry" identity --reap-ephemeral --dry
check "scratchpad (read, empty)" scratchpad
check "guard --beat" guard --beat
check "launch (list)" launch
check "robots-watch --once" robots-watch --once
check "robots-tail --once" robots-tail --once
check "alert" alert "parity harness alert"
check "say" say "parity harness say"
check "teardown (usage error)" teardown
check "variant list" variant list
check "drawdown (usage error)" drawdown
check "idle (usage error)" idle
check "unknown command" bogus-command-xyz

# lavish-import has no Go port (known gap — see bin/parlay's comment); Go
# reports "unknown command" where TS succeeds. Not a mismatch to fix here —
# recorded as an explicit, expected divergence instead of a FAIL.
ts_out="$(run_ts lavish-import 2>&1)"; ts_code=$?
go_out="$(run_go lavish-import 2>&1)"; go_code=$?
if echo "$go_out" | grep -q "unknown command"; then
  SUMMARY+=("KNOWN-GAP  lavish-import  (no Go port; ts exit=$ts_code go exit=$go_code — see bin/parlay comment)")
else
  SUMMARY+=("FAIL  lavish-import  (expected known-gap 'unknown command' from Go, got something else)")
  FAIL=$((FAIL + 1))
fi

echo
echo "== results =="
printf '%s\n' "${SUMMARY[@]}"
echo
echo "pass=$PASS fail=$FAIL skip=$SKIP"
if [ "$FAIL" -gt 0 ]; then
  PERSIST_LOG="$REPO/tools/cli/parity/last-diffs.log"
  cp "$DIFF_LOG" "$PERSIST_LOG"
  echo
  echo "diffs copied to: $PERSIST_LOG (gitignored scratch output)"
fi

[ "$FAIL" -eq 0 ]
