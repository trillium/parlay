#!/bin/sh
# inbox-override.test.sh — proves the inbox wrapper treats store pins as
# defaults, not forces (temp-DB flexibility):
#   • pre-set BEADS_DIR survives (not clobbered by the canonical path)
#   • unset BEADS_DIR defaults to the canonical store
#   • canonical store gets shared-server pins by default
#   • overridden BEADS_DIR gets NO server pins (embedded) unless explicitly set
#   • explicit empty server var disables (never re-defaulted to 1)
#   • INBOX_EVENTS_FILE derives from BEADS_DIR when unset
#   • pre-set INBOX_EVENTS_FILE / BRAIN_KNOWLEDGE_ROOT win
#
# Run: examples/fleet/inbox-emit/inbox-override.test.sh
set -eu

SELF_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SELF_DIR/inbox"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

STUB="$TMP/bin"
mkdir -p "$STUB"

# Stub bd: record env + argv, then answer `create` with a canonical id line.
# NOTE: `${V+x}` probes distinguish set-but-empty (explicit disable) from unset.
cat > "$STUB/bd" <<EOF
#!/bin/sh
{
  if [ -z "\${BEADS_DIR+x}" ]; then printf 'BEADS_DIR=<unset>\\n'; else printf 'BEADS_DIR=%s\\n' "\$BEADS_DIR"; fi
  if [ -z "\${BEADS_DOLT_SERVER_MODE+x}" ]; then printf 'SERVER_MODE=<unset>\\n'; else printf 'SERVER_MODE=%s\\n' "\$BEADS_DOLT_SERVER_MODE"; fi
  if [ -z "\${BEADS_DOLT_SHARED_SERVER+x}" ]; then printf 'SHARED=<unset>\\n'; else printf 'SHARED=%s\\n' "\$BEADS_DOLT_SHARED_SERVER"; fi
  if [ -z "\${BRAIN_KNOWLEDGE_ROOT+x}" ]; then printf 'ROOT=<unset>\\n'; else printf 'ROOT=%s\\n' "\$BRAIN_KNOWLEDGE_ROOT"; fi
  printf 'ARGV=%s\\n' "\$*"
} >> "$TMP/bd-env"
case "\$1" in
  create) printf '%s\\n' 'Created issue: inbox-abc' ;;
  *) printf '%s\\n' 'ok' ;;
esac
EOF
chmod +x "$STUB/bd"
export PATH="$STUB:$PATH"

fail=0
pass() { echo "PASS: $1"; }
fault() { echo "FAIL: $1"; fail=1; }
# Never returns nonzero: records the failure and lets the suite continue
# (a bare failing grep under `set -e` would abort the whole file).
expect_env() {
  if grep -Fqx "$1" "$TMP/bd-env"; then
    pass "$2"
  else
    fault "$2 (missing '$1')"
    sed 's/^/    /' "$TMP/bd-env"
  fi
}

# --- 1: pre-set BEADS_DIR survives -------------------------------------------
: > "$TMP/bd-env"
BEADS_DIR=/tmp/itest/.beads INBOX_EVENTS_FILE="$TMP/e1.jsonl" "$SCRIPT" list >/dev/null
expect_env 'BEADS_DIR=/tmp/itest/.beads' 'pre-set BEADS_DIR survives'

# --- 2: unset BEADS_DIR defaults to canonical ---------------------------------
: > "$TMP/bd-env"
env -u BEADS_DIR INBOX_EVENTS_FILE="$TMP/e2.jsonl" "$SCRIPT" list >/dev/null
expect_env "BEADS_DIR=$HOME/data/inbox/.beads" 'unset BEADS_DIR defaults to canonical'

# --- 3: canonical store gets server pins --------------------------------------
: > "$TMP/bd-env"
env -u BEADS_DIR -u BEADS_DOLT_SERVER_MODE -u BEADS_DOLT_SHARED_SERVER \
  INBOX_EVENTS_FILE="$TMP/e3.jsonl" "$SCRIPT" list >/dev/null
expect_env 'SERVER_MODE=1' 'canonical store defaults SERVER_MODE=1'
expect_env 'SHARED=1' 'canonical store defaults SHARED=1'

# --- 4: overridden store stays embedded (no pins) ------------------------------
: > "$TMP/bd-env"
BEADS_DIR=/tmp/itest/.beads INBOX_EVENTS_FILE="$TMP/e4.jsonl" \
  env -u BEADS_DOLT_SERVER_MODE -u BEADS_DOLT_SHARED_SERVER \
  "$SCRIPT" list >/dev/null
expect_env 'SERVER_MODE=<unset>' 'overridden store leaves SERVER_MODE unset (embedded)'
expect_env 'SHARED=<unset>' 'overridden store leaves SHARED unset (embedded)'

# --- 5: explicit empty disables, never re-defaulted -----------------------------
: > "$TMP/bd-env"
BEADS_DOLT_SERVER_MODE= BEADS_DOLT_SHARED_SERVER= \
  INBOX_EVENTS_FILE="$TMP/e5.jsonl" "$SCRIPT" list >/dev/null
expect_env 'SERVER_MODE=' 'explicit empty SERVER_MODE disables (not re-defaulted)'
expect_env 'SHARED=' 'explicit empty SHARED disables (not re-defaulted)'

# --- 6: BRAIN_KNOWLEDGE_ROOT derives from BEADS_DIR ----------------------------
: > "$TMP/bd-env"
BEADS_DIR=/tmp/itest/.beads INBOX_EVENTS_FILE="$TMP/e6.jsonl" \
  env -u BRAIN_KNOWLEDGE_ROOT "$SCRIPT" list >/dev/null
expect_env 'ROOT=/tmp/itest' 'BRAIN_KNOWLEDGE_ROOT derives from overridden BEADS_DIR'

# --- 7: INBOX_EVENTS_FILE derives from store root when unset --------------------
rm -f /tmp/itest-events-check.jsonl
BEADS_DIR="$TMP/store/.beads" "$SCRIPT" create 'derive check' >/dev/null
if [ -f "$TMP/store/events.jsonl" ] && grep -Fq 'inbox-abc' "$TMP/store/events.jsonl"; then
  pass 'INBOX_EVENTS_FILE derives from BEADS_DIR when unset'
else
  fault 'INBOX_EVENTS_FILE did not derive from BEADS_DIR'
  ls -la "$TMP/store/" 2>/dev/null | sed 's/^/    /'
fi

# --- 8: pre-set INBOX_EVENTS_FILE wins ------------------------------------------
: > "$TMP/custom-events.jsonl"
BEADS_DIR="$TMP/store2/.beads" INBOX_EVENTS_FILE="$TMP/custom-events.jsonl" \
  "$SCRIPT" create 'custom events' >/dev/null
if grep -Fq 'inbox-abc' "$TMP/custom-events.jsonl" && [ ! -f "$TMP/store2/events.jsonl" ]; then
  pass 'pre-set INBOX_EVENTS_FILE wins over derived default'
else
  fault 'pre-set INBOX_EVENTS_FILE was ignored'
fi

if [ "$fail" -eq 0 ]; then echo 'ALL PASS'; else echo 'SOME TESTS FAILED'; fi
exit "$fail"
