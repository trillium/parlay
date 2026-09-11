#!/bin/sh
# Prove inbox create stamps the creating agent's channel for close notification.
set -eu

SELF_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SELF_DIR/inbox"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

STUB="$TMP/bin"
mkdir -p "$STUB"
CAPTURE="$TMP/bd-calls"
EVENTS="$TMP/events.jsonl"
: > "$CAPTURE"

cat > "$STUB/bd" <<'EOF'
#!/bin/sh
case "$1" in
  create)
    printf '%s\n' 'Created issue: inbox-test'
    ;;
  label)
    printf 'label %s\n' "$*" >> "${BD_CAPTURE:?}"
    ;;
  *)
    printf 'unexpected bd call: %s\n' "$*" >&2
    exit 1
    ;;
esac
EOF
chmod +x "$STUB/bd"
export PATH="$STUB:$PATH"
export BD_CAPTURE="$CAPTURE"
export INBOX_EVENTS_FILE="$EVENTS"

PARLAY_AGENT_ID=pi-inbox "$SCRIPT" create 'test' >/dev/null
if grep -Fqx 'label label add inbox-test notify:pi-inbox' "$CAPTURE"; then
  echo 'PASS: create stamps notify:pi-inbox'
else
  echo 'FAIL: create did not stamp notify:pi-inbox' >&2
  cat "$CAPTURE" >&2
  exit 1
fi
if grep -Fq '"agent":"pi-inbox"' "$EVENTS"; then
  echo 'PASS: create emit records creating agent'
else
  echo 'FAIL: create emit omitted creating agent' >&2
  cat "$EVENTS" >&2
  exit 1
fi

: > "$CAPTURE"
: > "$EVENTS"
env -u PARLAY_AGENT_ID "$SCRIPT" create 'test without agent' >/dev/null
if [ -s "$CAPTURE" ]; then
  echo 'FAIL: absent agent still attempted label stamping' >&2
  cat "$CAPTURE" >&2
  exit 1
else
  echo 'PASS: absent agent leaves bead unsubscribed'
fi
if grep -Fq '"agent"' "$EVENTS"; then
  echo 'FAIL: absent agent was recorded in emit' >&2
  cat "$EVENTS" >&2
  exit 1
else
  echo 'PASS: absent agent emit has no agent field'
fi

echo 'ALL PASS'
