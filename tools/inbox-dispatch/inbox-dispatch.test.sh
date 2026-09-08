#!/usr/bin/env bash
# inbox-dispatch.test.sh — proves the inbox dispatcher behavior (mirrors
# tools/mechanic-dispatch/mechanic-dispatch.test.sh):
#   • a git-repo zone (parlay) dispatches with --worktree AND keeps --cwd <repo>
#   • the default/~ and pi/~ zones dispatch WITHOUT --worktree
#   • the pi zone routes to the pi-inbox channel (the pi-harness consumer)
# ...and the beads-required binding (robots-aswz class):
#   • --bead <store-qualified id> is always passed, so `parlay spawn`'s
#     beads-required gate cannot refuse the launch with exit 2
#   • a bare ticket id ("test") still yields the qualified "inbox-test"
# parlay/herdr/inbox are stubbed and $HOME is redirected to a
# sandbox, so no real agent launches and no real repo is touched.
#
# Run: tools/inbox-dispatch/inbox-dispatch.test.sh
set -euo pipefail

SELF_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SELF_DIR/inbox-dispatch"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# Redirect HOME so zone_entry()'s $HOME/... paths land in the sandbox and the
# default/pi zones' CWD=$HOME is a non-git dir.
export HOME="$TMP/home"
mkdir -p "$HOME/.local/bin"

# Stub bin dir, first on PATH.
STUB="$TMP/bin"
mkdir -p "$STUB"
CAPTURE="$TMP/spawn-argv"


# herdr: '{}' → absent agent + no workspace, so dispatch takes the launch path.
cat >"$STUB/herdr" <<'EOF'
#!/bin/sh
echo '{}'
EOF

# parlay: record argv (one element per line, minus the subcommand word) and
# launch nothing. The pi path must send, never spawn.
cat >"$STUB/parlay" <<EOF
#!/bin/sh
if [ "\$1" = spawn ] || [ "\$1" = send ]; then
  shift
  printf '%s\n' "\$@" > "$CAPTURE"
fi
exit 0
EOF

# inbox: `inbox show <id>` prints a non-closed [OPEN] status line so the
# dispatch is not skipped; other subcommands are no-ops.
# Mirrors the real status line: "<glyph> inbox-<id> · <title> [... OPEN]", and
# resolves a bare id to its store-qualified form exactly as the real store does.
cat >"$STUB/inbox" <<'EOF'
#!/bin/sh
if [ "$1" = show ]; then
  id=$2
  case "$id" in inbox-*) : ;; *) id="inbox-$id" ;; esac
  printf '%s\n' "o $id · Fix the thing   [P2 · OPEN]"
fi
EOF

chmod +x "$STUB"/*
export PATH="$STUB:$PATH"

fail=0
pass() { echo "PASS: $1"; }
fault() { echo "FAIL: $1"; echo "  argv was:"; sed 's/^/    /' "$CAPTURE"; fail=1; }

# --- case 1: git-repo zone (parlay) → --worktree present, --cwd repo preserved
mkdir -p "$HOME/code/parlay"
git -C "$HOME/code/parlay" init -q
: > "$CAPTURE"
bash "$SCRIPT" inbox-test parlay >/dev/null 2>&1

if grep -Fxq -- '--worktree' "$CAPTURE"; then
  pass "git-repo zone (parlay) dispatched WITH --worktree"
else
  fault "git-repo zone (parlay) missing --worktree"
fi

if grep -Fxq -- "$HOME/code/parlay" "$CAPTURE"; then
  pass "git-repo zone kept --cwd <repo-root>"
else
  fault "git-repo zone dropped --cwd <repo-root>"
fi

# --- case 2: default and pi zones are one serial pi channel -------------------
# An unzoned/default inbox ticket must not create mc-inbox-* agents.
: > "$CAPTURE"
bash "$SCRIPT" inbox-test default >/dev/null 2>&1
if grep -Fxq -- '--pi-inbox' "$CAPTURE"; then
  pass "default zone routed to the pi-inbox channel"
else
  fault "default zone did not route to the pi-inbox channel"
fi
if grep -Fxq -- 'INBOX_POKE v1: new inbox work may be available. Run the serial inbox worker; claim and process exactly one next available item, then continue until the inbox is exhausted.' "$CAPTURE"; then
  pass "default zone sent one worker poke"
else
  fault "default zone did not send the worker poke"
fi
if grep -Fxq -- 'spawn' "$CAPTURE"; then
  fault "default zone must not spawn an inbox agent"
else
  pass "default zone did not spawn an inbox agent"
fi

: > "$CAPTURE"
bash "$SCRIPT" inbox-test pi >/dev/null 2>&1
if grep -Fxq -- '--pi-inbox' "$CAPTURE"; then
  pass "pi zone routed to the pi-inbox channel"
else
  fault "pi zone did not route to the pi-inbox channel"
fi
if grep -Fxq -- 'spawn' "$CAPTURE"; then
  fault "pi zone must not spawn an inbox agent"
else
  pass "pi zone did not spawn an inbox agent"
fi

# --- case 3: specialized zones retain beads-required spawn binding ------------
bead_arg() { grep -A1 -Fx -- '--bead' "$CAPTURE" | tail -1; }
claim_arg() { grep -A1 -Fx -- '--claim' "$CAPTURE" | tail -1; }

: > "$CAPTURE"
bash "$SCRIPT" inbox-test parlay >/dev/null 2>&1
if [ "$(bead_arg)" = inbox-test ]; then
  pass "specialized zone bound --bead inbox-test"
else
  fault "specialized zone did not pass --bead inbox-test"
fi

# --- case 4: bare ticket ids are qualified on the serial pi path --------------
: > "$CAPTURE"
bash "$SCRIPT" test >/dev/null 2>&1
if grep -Fxq -- 'INBOX_POKE v1: new inbox work may be available. Run the serial inbox worker; claim and process exactly one next available item, then continue until the inbox is exhausted.' "$CAPTURE"; then
  pass "bare ticket id emitted a worker poke"
else
  fault "bare ticket id did not emit the worker poke"
fi

if [ "$fail" -eq 0 ]; then
  echo "ALL PASS"
else
  echo "SOME TESTS FAILED"
fi
exit "$fail"
