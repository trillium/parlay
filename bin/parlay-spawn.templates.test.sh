#!/usr/bin/env bash
# Template loading and interpolation tests for parlay-spawn.
#
# Tests verify that:
# 1. Templates exist and are readable
# 2. load_template function loads and interpolates correctly
# 3. Both claim and default variants work
# 4. Variable substitution preserves special characters
#
# Run from the repo root or from bin/ directory.

set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPAWN="$SELF_DIR/parlay-spawn"
TEMPLATES_DIR="$SELF_DIR/../launch-templates"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

# Lift the load_template function from parlay-spawn so we test the real impl
eval "$(sed -n '/^load_template()/,/^}/p' "$SPAWN")"
if ! declare -F load_template >/dev/null; then
  fail "could not lift load_template out of $SPAWN"
  exit 1
fi

# ── Template files exist ────────────────────────────────────────────────
if [ -f "$TEMPLATES_DIR/default.txt" ]; then
  pass "default template exists"
else
  fail "default template not found: $TEMPLATES_DIR/default.txt"
fi

if [ -f "$TEMPLATES_DIR/claim.txt" ]; then
  pass "claim template exists"
else
  fail "claim template not found: $TEMPLATES_DIR/claim.txt"
fi

# ── load_template interpolation ─────────────────────────────────────────
# Create a temporary test template
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

cat > "$tmpdir/test.txt" <<'EOF'
Agent: {{AGENT_ID}}
Name: {{NAME}}
Server: {{PARLAY}}
Task: {{PROMPT}}
EOF

result=$(load_template "$tmpdir/test.txt" \
  "AGENT_ID=test-agent" \
  "NAME=Test Agent" \
  "PARLAY=http://localhost:4242" \
  "PROMPT=Do something")

if printf '%s' "$result" | grep -q "^Agent: test-agent$"; then
  pass "load_template interpolates AGENT_ID"
else
  fail "load_template failed to interpolate AGENT_ID"
fi

if printf '%s' "$result" | grep -q "^Name: Test Agent$"; then
  pass "load_template interpolates NAME with spaces"
else
  fail "load_template failed to interpolate NAME with spaces"
fi

if printf '%s' "$result" | grep -q "^Server: http://localhost:4242$"; then
  pass "load_template interpolates PARLAY (URL)"
else
  fail "load_template failed to interpolate PARLAY"
fi

# ── load_template handles missing files ─────────────────────────────────
if ! load_template "$tmpdir/nonexistent.txt" "VAR=value" >/dev/null 2>&1; then
  pass "load_template returns non-zero for missing file"
else
  fail "load_template should return non-zero for missing file"
fi

# ── load_template preserves special characters ──────────────────────────
# Test with values containing quotes, backticks, dollar signs
# Use a simpler test: quotes and dollar signs that aren't supposed to expand
result=$(load_template "$tmpdir/test.txt" \
  "AGENT_ID=test" \
  "NAME=Agent with \$VAR and \"quotes\"" \
  "PARLAY=http://localhost" \
  "PROMPT=task")

if printf '%s' "$result" | grep -qF 'Name: Agent with $VAR and "quotes"'; then
  pass "load_template preserves special characters in values"
else
  fail "load_template did not preserve special characters"
fi

# ── Actual templates can be loaded and interpolated ─────────────────────
result=$(load_template "$TEMPLATES_DIR/claim.txt" \
  "AGENT_ID=my-agent" \
  "CLAIM=task-123" \
  "SETUP_BLOCK=")

if printf '%s' "$result" | grep -q "my-agent"; then
  pass "claim.txt template loads and interpolates"
else
  fail "claim.txt template failed to load or interpolate"
fi

# ── Default template is more complex ────────────────────────────────────
result=$(load_template "$TEMPLATES_DIR/default.txt" \
  "PARLAY=http://localhost:4242" \
  "AGENT_ID=tester" \
  "NAME=Test Agent" \
  "COLOR=#aabbcc" \
  "MONITOR_CMD_JSON={\"cmd\": \"test\"}" \
  "SETUP_BLOCK=## Setup test" \
  "PROMPT=Do the work" \
  "DOD=Mark it done")

if printf '%s' "$result" | grep -q "tester"; then
  pass "default.txt template loads and interpolates (AGENT_ID)"
else
  fail "default.txt template failed to interpolate AGENT_ID"
fi

if printf '%s' "$result" | grep -q "Do the work"; then
  pass "default.txt template interpolates PROMPT"
else
  fail "default.txt template failed to interpolate PROMPT"
fi

if printf '%s' "$result" | grep -q "## Task"; then
  pass "default.txt template structure is preserved"
else
  fail "default.txt template structure was corrupted"
fi

# ── Exit with summary ───────────────────────────────────────────────────
if [ "$FAILED" -eq 0 ]; then
  echo ""
  echo "All template tests passed."
  exit 0
else
  echo ""
  echo "Some template tests failed."
  exit 1
fi
