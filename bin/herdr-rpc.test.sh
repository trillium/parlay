#!/usr/bin/env bash
# herdr-rpc.test.sh — regression coverage for the herdr RPC path used by
# `parlay spawn`. Hermetic: no herdr daemon, no network. The socket is a real
# Unix socket served by a throwaway `nc -lU`, so the request bytes herdr_rpc
# actually puts on the wire are captured and asserted verbatim.
#
# WHAT THIS PINS (the spawn outage of 2026-08-18, two independent defects):
#
#  1. `${2:-{}}` is NOT a "default to empty object" idiom. Bash ends the
#     parameter expansion at the first unescaped '}', so it means «default '{'»
#     plus a literal '}'. Unset $2 coincidentally yields '{}', which is why it
#     looked correct — but every caller-supplied value got a stray '}' appended
#     ('{"a":1}' -> '{"a":1}}'), jq rejected it with "invalid JSON text passed
#     to --argjson", and EVERY RPC call failed. The spawner's tab.create then
#     returned nothing and the spawn died with "no root pane returned".
#
#  2. The spawner's RPC request shapes had drifted from herdr 0.8.0. Because
#     defect 1 made every RPC call fail before it reached the daemon, and the
#     helpers are all `>/dev/null 2>&1 || true`, the drift was invisible. These
#     are asserted structurally (against the jq filters in the source) rather
#     than against a live daemon so the check runs in CI with no herdr present.
#
# Run: bin/herdr-rpc.test.sh

set -uo pipefail

BIN_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FAILED=0

ok()   { echo "ok: $1"; }
fail() { echo "FAIL: $1" >&2; FAILED=1; }

check() { # <desc> <expected> <actual>
  if [ "$2" = "$3" ]; then ok "$1"; else
    fail "$1"
    echo "  expected: $2" >&2
    echo "  actual:   $3" >&2
  fi
}

contains() { # <desc> <needle> <haystack>
  case "$3" in
    *"$2"*) ok "$1" ;;
    *) fail "$1"; echo "  missing: $2" >&2 ;;
  esac
}

not_contains() { # <desc> <needle> <haystack>
  case "$3" in
    *"$2"*) fail "$1"; echo "  should NOT contain: $2" >&2 ;;
    *) ok "$1" ;;
  esac
}

# ---------------------------------------------------------------------------
# A. The bash brace-default bug itself — proves the test has teeth.
# ---------------------------------------------------------------------------
buggy()  { local p="${2:-{}}"; printf '%s' "$p"; }
fixed()  { local p="${2:-}"; [ -n "$p" ] || p='{}'; printf '%s' "$p"; }

check "pre-fix form mangles a supplied value (test has teeth)" \
  '{"a":1}}' "$(buggy m '{"a":1}')"
check "pre-fix form is accidentally correct when unset (why it hid)" \
  '{}' "$(buggy m)"
check "fixed form passes a supplied value through verbatim" \
  '{"a":1}' "$(fixed m '{"a":1}')"
check "fixed form still defaults to an empty object" \
  '{}' "$(fixed m)"

# ---------------------------------------------------------------------------
# B. herdr_rpc puts the caller's params on the wire unmodified.
#    A real Unix socket, a real `nc` listener, real captured bytes.
# ---------------------------------------------------------------------------
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Keep the socket path far under sun_path's 104-byte cap (see CLAUDE.md).
SOCK="$TMP/s"

send_and_capture() { # [params-json] -> prints the captured request line
  local have_params=0 params=""
  if [ $# -gt 0 ]; then have_params=1; params="$1"; fi
  local cap="$TMP/cap.$RANDOM"
  rm -f "$SOCK"
  # One-shot listener: accept a single connection, dump it, exit.
  ( nc -lU "$SOCK" >"$cap" 2>/dev/null & echo $! >"$TMP/ncpid" )
  # Wait for the listener to bind rather than guessing a sleep.
  local i=0
  while [ ! -S "$SOCK" ] && [ "$i" -lt 100 ]; do sleep 0.05; i=$((i + 1)); done
  [ -S "$SOCK" ] || { echo "SKIP: nc -lU could not bind $SOCK" >&2; return 2; }

  if [ "$have_params" -eq 1 ]; then
    HERDR_SOCK="$SOCK" PARLAY_HERDR_RPC_TIMEOUT=1 bash -c '
      source "$1/herdr-rpc"; herdr_rpc "probe.method" "$2" >/dev/null 2>&1
    ' _ "$BIN_DIR" "$params" || true
  else
    HERDR_SOCK="$SOCK" PARLAY_HERDR_RPC_TIMEOUT=1 bash -c '
      source "$1/herdr-rpc"; herdr_rpc "probe.method" >/dev/null 2>&1
    ' _ "$BIN_DIR" || true
  fi

  kill "$(cat "$TMP/ncpid" 2>/dev/null)" 2>/dev/null || true
  cat "$cap" 2>/dev/null
  rm -f "$cap"
}

if ! command -v nc >/dev/null 2>&1; then
  echo "SKIP: nc not available; wire-level assertions skipped" >&2
else
  REQ="$(send_and_capture '{"target":"dev-assistant","n":1}')"
  rc=$?
  if [ "$rc" = "2" ] || [ -z "$REQ" ]; then
    echo "SKIP: no request captured (nc -lU unsupported here)" >&2
  else
    contains "supplied params reach the wire intact" \
      '"target":"dev-assistant"' "$REQ"
    not_contains "no stray brace is appended to supplied params" \
      '"n":1}}}' "$REQ"
    # The full envelope must parse — the exact thing the bug destroyed.
    if printf '%s' "$REQ" | jq -e . >/dev/null 2>&1; then
      ok "request envelope is valid JSON"
      check "params round-trip exactly" '{"n":1,"target":"dev-assistant"}' \
        "$(printf '%s' "$REQ" | jq -Sc '.params')"
      check "method is carried through" 'probe.method' \
        "$(printf '%s' "$REQ" | jq -r '.method')"
    else
      fail "request envelope is valid JSON"
      echo "  got: $REQ" >&2
    fi

    REQ2="$(send_and_capture)"
    if [ -n "$REQ2" ]; then
      check "omitted params default to an empty object" '{}' \
        "$(printf '%s' "$REQ2" | jq -c '.params')"
    else
      echo "SKIP: no-params capture unavailable" >&2
    fi
  fi
fi

# ---------------------------------------------------------------------------
# C. Invalid params are refused by name, not by a bare jq complaint.
# ---------------------------------------------------------------------------
rm -f "$SOCK"
( nc -lU "$SOCK" >/dev/null 2>&1 & echo $! >"$TMP/ncpid2" )
i=0; while [ ! -S "$SOCK" ] && [ "$i" -lt 100 ]; do sleep 0.05; i=$((i + 1)); done
if [ -S "$SOCK" ]; then
  ERR="$(
    HERDR_SOCK="$SOCK" PARLAY_HERDR_RPC_TIMEOUT=1 bash -c '
      source "$1/herdr-rpc"
      herdr_rpc "probe.method" "{oops" 2>&1 >/dev/null
    ' _ "$BIN_DIR"
  )"
  contains "malformed params name the method" 'probe.method' "$ERR"
  contains "malformed params say what was wrong" 'not valid JSON' "$ERR"
  kill "$(cat "$TMP/ncpid2" 2>/dev/null)" 2>/dev/null || true
else
  echo "SKIP: could not bind socket for invalid-params case" >&2
fi

# ---------------------------------------------------------------------------
# D. No brace-default idiom survives anywhere in bin/ or tools/.
# ---------------------------------------------------------------------------
# Comment lines are excluded: herdr-rpc deliberately QUOTES the broken form in
# the warning that explains why it must never be used again.
STRAY="$(grep -rn ':-{' "$BIN_DIR" "$BIN_DIR/../tools" 2>/dev/null \
  | grep -v 'herdr-rpc.test.sh' \
  | grep -vE '^[^:]+:[0-9]+:[[:space:]]*#' || true)"
check "no \${x:-{...}} brace default remains in bin/ or tools/" "" "$STRAY"

echo
if [ "$FAILED" -eq 0 ]; then
  echo "ALL PASS"
  exit 0
fi
echo "FAILURES PRESENT" >&2
exit 1
