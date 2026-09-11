#!/usr/bin/env bash
# inbox-override.test.sh (dispatch) — proves inbox-dispatch honors its command
# seams and inherits temp-store env (flexibility without PATH hacks):
#   • INBOX_BIN override is used instead of bare `inbox`
#   • BEADS_DIR flows through to the inbox backend (temp-DB support)
#   • PARLAY_BIN override is used for the pi poke (no live server touch)
#
# Run: examples/fleet/inbox-dispatch/inbox-override.test.sh
set -euo pipefail

SELF_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SELF_DIR/inbox-dispatch"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

STUB="$TMP/bin"
mkdir -p "$STUB"
CAPTURE="$TMP/parlay-argv"
SEEN_BEADS_DIR="$TMP/seen-beads-dir"

# Fake inbox backend: records BEADS_DIR, prints an OPEN status line.
cat >"$STUB/my-inbox" <<EOF
#!/bin/sh
printf '%s' "\${BEADS_DIR:-<unset>}" > "$SEEN_BEADS_DIR"
id="\$2"
case "\$id" in inbox-*) : ;; *) id="inbox-\$id" ;; esac
printf '%s\n' "o \$id · Temp ticket   [P2 · OPEN]"
EOF

# Fake parlay: record argv, launch nothing.
cat >"$STUB/my-parlay" <<EOF
#!/bin/sh
printf '%s\n' "\$@" > "$CAPTURE"
exit 0
EOF

# Fake herdr: absent (should never be consulted on the pi path).
cat >"$STUB/my-herdr" <<'EOF'
#!/bin/sh
echo '{}'
EOF
chmod +x "$STUB"/*

fail=0
pass() { echo "PASS: $1"; }
fault() { echo "FAIL: $1"; fail=1; }

# --- 1: INBOX_BIN override honored ---------------------------------------------
: > "$CAPTURE"
INBOX_BIN="$STUB/my-inbox" PARLAY_BIN="$STUB/my-parlay" HERDR_BIN="$STUB/my-herdr" \
  bash "$SCRIPT" inbox-t1 pi >/dev/null 2>&1
if grep -Fq 'INBOX_POKE v1' "$CAPTURE"; then
  pass "INBOX_BIN override honored (pi poke still sent)"
else
  fault "INBOX_BIN override not honored"
  sed 's/^/    /' "$CAPTURE"
fi

# --- 2: BEADS_DIR flows through to the backend ----------------------------------
BEADS_DIR=/tmp/itest/.beads INBOX_BIN="$STUB/my-inbox" \
  PARLAY_BIN="$STUB/my-parlay" HERDR_BIN="$STUB/my-herdr" \
  bash "$SCRIPT" inbox-t2 pi >/dev/null 2>&1
if [ "$(cat "$SEEN_BEADS_DIR")" = /tmp/itest/.beads ]; then
  pass "BEADS_DIR flows through to the inbox backend (temp-DB support)"
else
  fault "BEADS_DIR did not reach backend (saw '$(cat "$SEEN_BEADS_DIR")')"
fi

# --- 3: PARLAY_BIN override honored (no live server) ----------------------------
# --- 3: PARLAY_BIN override honored (no live server) ----------------------------
# The stub records argv to $CAPTURE; the live `parlay` would not. A non-empty
# capture containing the poke proves dispatch called $PARLAY_BIN, not PATH parlay.
: > "$CAPTURE"
INBOX_BIN="$STUB/my-inbox" PARLAY_BIN="$STUB/my-parlay" HERDR_BIN="$STUB/my-herdr" \
  bash "$SCRIPT" inbox-t3 >/dev/null 2>&1
if [ -s "$CAPTURE" ] && grep -Fq 'INBOX_POKE v1' "$CAPTURE"; then
  pass "PARLAY_BIN override honored (poke went to stub, not live server)"
else
  fault "PARLAY_BIN override not honored"
  sed 's/^/    /' "$CAPTURE"
fi

if [ "$fail" -eq 0 ]; then echo "ALL PASS"; else echo "SOME TESTS FAILED"; fi
exit "$fail"
