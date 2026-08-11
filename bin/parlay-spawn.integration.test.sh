#!/usr/bin/env bash
# Integration test: verify parlay-spawn can load and interpolate templates
# without actually launching herdr.
#
# This test extracts the template loading logic and tests it in isolation,
# mimicking what parlay-spawn does but without side effects.

set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPAWN="$SELF_DIR/parlay-spawn"
BIN_DIR="$SELF_DIR"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

# Lift load_template from parlay-spawn
eval "$(sed -n '/^load_template()/,/^}/p' "$SPAWN")"
if ! declare -F load_template >/dev/null; then
  fail "could not lift load_template from $SPAWN"
  exit 1
fi

# ── Simulate default prompt loading (non-claim path) ──────────────────────
echo "Testing default prompt template loading..." >&2

TEMPLATES_DIR="$BIN_DIR/../launch-templates"
PARLAY="http://localhost:31337"
AGENT_ID="test-agent"
NAME="Test Agent"
COLOR="#abc123"
MONITOR_CMD_JSON='{"command": "test"}'
SETUP_BLOCK="## Setup\n\nWorktree test"
PROMPT="Do something important"
DOD="Mark it done"

STARTUP_PROMPT=$(load_template "$TEMPLATES_DIR/default.txt" \
  "PARLAY=$PARLAY" \
  "AGENT_ID=$AGENT_ID" \
  "NAME=$NAME" \
  "COLOR=$COLOR" \
  "MONITOR_CMD_JSON=$MONITOR_CMD_JSON" \
  "SETUP_BLOCK=$SETUP_BLOCK" \
  "PROMPT=$PROMPT" \
  "DOD=$DOD")

if printf '%s' "$STARTUP_PROMPT" | grep -q "test-agent"; then
  pass "default template contains interpolated AGENT_ID"
else
  fail "default template missing AGENT_ID interpolation"
fi

if printf '%s' "$STARTUP_PROMPT" | grep -q "http://localhost:31337"; then
  pass "default template contains interpolated PARLAY"
else
  fail "default template missing PARLAY interpolation"
fi

if printf '%s' "$STARTUP_PROMPT" | grep -q "Do something important"; then
  pass "default template contains interpolated PROMPT"
else
  fail "default template missing PROMPT interpolation"
fi

if printf '%s' "$STARTUP_PROMPT" | grep -q "Monitor(.*persistent: true"; then
  pass "default template contains Monitor() call structure"
else
  fail "default template missing Monitor structure"
fi

if printf '%s' "$STARTUP_PROMPT" | grep -q "Definition of done"; then
  pass "default template contains DOD section"
else
  fail "default template missing DOD section"
fi

if printf '%s' "$STARTUP_PROMPT" | grep -q "Status protocol"; then
  pass "default template contains Status protocol section"
else
  fail "default template missing Status protocol section"
fi

# ── Simulate claim prompt loading ───────────────────────────────────────
echo "Testing claim prompt template loading..." >&2

CLAIM="task-123"
STARTUP_CLAIM=$(load_template "$TEMPLATES_DIR/claim.txt" \
  "AGENT_ID=$AGENT_ID" \
  "CLAIM=$CLAIM" \
  "SETUP_BLOCK=$SETUP_BLOCK")

if printf '%s' "$STARTUP_CLAIM" | grep -q "parlay claim task-123"; then
  pass "claim template contains interpolated CLAIM"
else
  fail "claim template missing CLAIM interpolation"
fi

if printf '%s' "$STARTUP_CLAIM" | grep -q "test-agent"; then
  pass "claim template contains interpolated AGENT_ID"
else
  fail "claim template missing AGENT_ID in claim template"
fi

if printf '%s' "$STARTUP_CLAIM" | grep -q "identity --park"; then
  pass "claim template contains fallback instructions"
else
  fail "claim template missing fallback instructions"
fi

# ── Exit summary ────────────────────────────────────────────────────────
echo ""
if [ "$FAILED" -eq 0 ]; then
  echo "All integration tests passed."
  exit 0
else
  echo "Some integration tests failed."
  exit 1
fi
