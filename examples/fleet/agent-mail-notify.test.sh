#!/usr/bin/env bash
# agent-mail-notify.test.sh — regression coverage for the signal->Parlay
# wake bridge (agent-mail-notify.py). Proves two contracts stay fixed:
# 1. Pokes MUST emit the distinct `MAIL_POKE v1:` wire line, because the
#    pi-inbox-bridge routes MAIL_POKE to the agent-mail prompt (MCP verbs)
#    and a store-prefixed poke would hand the pane inbox instructions.
#    A legacy per-seat "poke" key in the map is ignored, never emitted.
# 2. One poke per mail (mtime dedupe), re-arm on fetch, unmapped = log only.
#
# Hermetic: `parlay` is stubbed (records argv, exits 0), signals/map/state
# all live under a mktemp dir. No daemon, no network, no repo touched.
#
# Run: examples/fleet/agent-mail-notify.test.sh
set -euo pipefail

SELF_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SELF_DIR/agent-mail-notify.py"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# Stub parlay first on PATH: log argv per call, always succeed.
STUB="$TMP/bin"
mkdir -p "$STUB"
CALLS="$TMP/parlay-calls.log"
touch "$CALLS"
cat > "$STUB/parlay" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >> "$CALLS"
exit 0
EOF
chmod +x "$STUB/parlay"
export PATH="$STUB:$PATH"

SIG="$TMP/signals"
MAP="$TMP/map.json"
STATE="$TMP/state.json"
mkdir -p "$SIG/projects/pool/agents"

poke_count() { grep -c '^send ' "$CALLS" || true; }
fail() { echo "FAIL: $1" >&2; exit 1; }

# Fixture: one seat with a legacy poke key (must be IGNORED), one
# bare-string seat, one unmapped seat. All have pending mail.
echo '{}' > "$SIG/projects/pool/agents/SeatA.signal"
echo '{}' > "$SIG/projects/pool/agents/SeatB.signal"
echo '{}' > "$SIG/projects/pool/agents/SeatC.signal"
cat > "$MAP" <<'EOF'
{"SeatA": {"pane": "pane-a", "poke": "SANDBOX_POKE"}, "SeatB": "pane-b"}
EOF

# 1. First pass: two pokes, both on the MAIL_POKE wire line.
python3 "$SCRIPT" --signals "$SIG" --map "$MAP" --state "$STATE" --once > "$TMP/out1.txt"
[ "$(poke_count)" = 2 ] || fail "expected 2 pokes, got $(poke_count)"
grep -q '^send --agent pane-a MAIL_POKE v1: agent-mail for SeatA (project pool)' "$CALLS" \
  || fail "SeatA poke missing MAIL_POKE line: $(cat "$CALLS")"
grep -q '^send --agent pane-b MAIL_POKE v1: agent-mail for SeatB (project pool)' "$CALLS" \
  || fail "SeatB poke missing MAIL_POKE line: $(cat "$CALLS")"
grep -q 'SANDBOX_POKE' "$CALLS" && fail "legacy poke key leaked onto the wire: $(cat "$CALLS")"
grep -q 'unmapped seat with mail: pool/SeatC' "$TMP/out1.txt" \
  || fail "unmapped SeatC not logged"

# 2. Second pass, nothing changed: silence (dedupe by mtime).
python3 "$SCRIPT" --signals "$SIG" --map "$MAP" --state "$STATE" --once > "$TMP/out2.txt"
[ "$(poke_count)" = 2 ] || fail "dedupe broken: $(poke_count) pokes after rerun"

# 3. New mail for SeatA (bumped mtime): exactly one re-poke.
sleep 1
touch "$SIG/projects/pool/agents/SeatA.signal"
python3 "$SCRIPT" --signals "$SIG" --map "$MAP" --state "$STATE" --once > /dev/null
[ "$(poke_count)" = 3 ] || fail "new mail did not re-arm poke"
tail -1 "$CALLS" | grep -q '^send --agent pane-a MAIL_POKE v1:' \
  || fail "re-poke lost MAIL_POKE: $(tail -1 "$CALLS")"

# 4. Signal gone (fetched): state entry cleared, no poke, re-arms cleanly.
rm "$SIG/projects/pool/agents/SeatB.signal"
python3 "$SCRIPT" --signals "$SIG" --map "$MAP" --state "$STATE" --once > /dev/null
[ "$(poke_count)" = 3 ] || fail "fetch should not poke"
python3 -c "import json,sys; s=json.load(open('$STATE')); sys.exit(0 if 'pool/SeatB' not in s else 1)" \
  || fail "fetched seat state not cleared"

echo "PASS: agent-mail-notify (MAIL_POKE wire line, dedupe, re-arm, unmapped)"
