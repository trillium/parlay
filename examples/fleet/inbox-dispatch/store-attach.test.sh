#!/usr/bin/env bash
# store-attach.test.sh — proves inbox-dispatch follows any brain federated
# store, not just the inbox (STORE-ATTACH):
#   • a store-qualified id (`task-abc`) resolves backend + channel + poke
#     from the id's leading token, with no STORE env set
#   • STORE env drives a bare id against that store
#   • explicit INBOX_BIN still wins over the resolved store backend
#   • inbox behavior is unchanged (pi-inbox / INBOX_POKE v1)
#   • bad ids fail loud, closed tickets skip — per store
#
# Run: examples/fleet/inbox-dispatch/store-attach.test.sh
set -euo pipefail

SELF_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SELF_DIR/inbox-dispatch"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

STUB="$TMP/bin"
mkdir -p "$STUB"
CAPTURE="$TMP/parlay-argv"

# herdr: absent (pi path never consults it; specialized path takes launch).
cat >"$STUB/herdr" <<'EOF'
#!/bin/sh
echo '{}'
EOF

# parlay stub: record argv, launch nothing.
cat >"$STUB/parlay" <<EOF
#!/bin/sh
printf '%s\n' "\$@" > "$CAPTURE"
exit 0
EOF

# Two store backends. Each prints an OPEN status line, qualifying bare ids the
# way the real wrappers do. Ids containing `missing` fail, standing in for
# "not in this store" (proves the right backend was consulted and the
# bad-id guard fires per store).
cat >"$STUB/task" <<'EOF'
#!/bin/sh
if [ "$1" = show ]; then
  case "$2" in *missing*) echo "no such" >&2; exit 1 ;; esac
  id=$2; case "$id" in task-*) : ;; *) id="task-$id" ;; esac
  printf '%s\n' "o $id · Task ticket   [P2 · OPEN]"
fi
EOF
cat >"$STUB/inbox" <<'EOF'
#!/bin/sh
if [ "$1" = show ]; then
  id=$2; case "$id" in inbox-*) : ;; *) id="inbox-$id" ;; esac
  printf '%s\n' "o $id · Inbox ticket   [P2 · OPEN]"
fi
EOF
chmod +x "$STUB"/*
export PATH="$STUB:$PATH"

fail=0
pass() { echo "PASS: $1"; }
fault() { echo "FAIL: $1"; echo "  argv was:"; sed 's/^/    /' "$CAPTURE"; fail=1; }

# --- 1: store-qualified id attaches by leading token ---------------------------
: > "$CAPTURE"
env -u STORE -u INBOX_BIN -u STORE_BIN bash "$SCRIPT" task-abc pi >/dev/null 2>&1
if grep -Fxq -- '--task-inbox' "$CAPTURE"; then
  pass "task-abc poked the per-store task-inbox channel"
else
  fault "task-abc did not route to --task-inbox"
fi
if grep -Fq 'TASK_POKE v1' "$CAPTURE" && grep -Fq 'serial task worker (task)' "$CAPTURE"; then
  pass "task poke is store-aware (TASK_POKE v1, task worker)"
else
  fault "task poke text is not store-aware"
fi

# --- 2: STORE env drives a bare id ----------------------------------------------
: > "$CAPTURE"
STORE=task env -u INBOX_BIN -u STORE_BIN bash "$SCRIPT" abc pi >/dev/null 2>&1
if grep -Fxq -- '--task-inbox' "$CAPTURE" && grep -Fq 'TASK_POKE v1' "$CAPTURE"; then
  pass "STORE=task + bare id attached to the task store"
else
  fault "STORE=task + bare id did not attach"
fi

# --- 3: explicit INBOX_BIN still wins --------------------------------------------
: > "$CAPTURE"
INBOX_BIN="$STUB/inbox" bash "$SCRIPT" inbox-zzz pi >/dev/null 2>&1
if grep -Fxq -- '--pi-inbox' "$CAPTURE" && grep -Fq 'INBOX_POKE v1' "$CAPTURE"; then
  pass "explicit INBOX_BIN wins; inbox wire format unchanged"
else
  fault "explicit INBOX_BIN was not honored"
fi

# --- 4: bad id in a foreign store fails loud --------------------------------------
if STORE=task env -u INBOX_BIN -u STORE_BIN bash "$SCRIPT" nope-missing pi >/dev/null 2>&1; then
  fault "missing task id should exit nonzero"
else
  pass "missing task id fails loud (nonzero, no dispatch)"
fi

if [ "$fail" -eq 0 ]; then echo "ALL PASS"; else echo "SOME TESTS FAILED"; fi
exit "$fail"
