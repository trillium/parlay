#!/usr/bin/env bash
# mechanic-dispatch.test.sh — proves the worktree-isolation behavior:
#   • a git-repo zone (parlay) dispatches with --worktree AND keeps --cwd <repo>
#   • the default/~ zone dispatches WITHOUT --worktree
# ...and the beads-required binding (robots-aswz):
#   • --bead <store-qualified id> is always passed, so parlay-spawn's
#     beads-required gate cannot refuse the launch with exit 2
#   • a bare ticket id ("test") still yields the qualified "robots-test"
# parlay-spawn/herdr/robots/parlay are stubbed and $HOME is redirected to a
# sandbox, so no real agent launches and no real repo is touched.
#
# Run: tools/mechanic-dispatch/mechanic-dispatch.test.sh
set -euo pipefail

SELF_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SELF_DIR/mechanic-dispatch"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# Redirect HOME so zone_entry()'s $HOME/... paths land in the sandbox and the
# default zone's CWD=$HOME is a non-git dir.
export HOME="$TMP/home"
mkdir -p "$HOME/.local/bin"

# Stub bin dir, first on PATH.
STUB="$TMP/bin"
mkdir -p "$STUB"
CAPTURE="$TMP/spawn-argv"

# parlay-spawn: record argv (one element per line), launch nothing.
cat >"$STUB/parlay-spawn" <<EOF
#!/bin/sh
printf '%s\n' "\$@" > "$CAPTURE"
EOF

# herdr: '{}' → absent agent + no workspace, so dispatch takes the launch path.
cat >"$STUB/herdr" <<'EOF'
#!/bin/sh
echo '{}'
EOF

# parlay: only reached on the live path (not exercised here); no-op.
cat >"$STUB/parlay" <<'EOF'
#!/bin/sh
exit 0
EOF

# robots: `robots show <id>` prints a non-closed [OPEN] status line so the
# dispatch is not skipped; other subcommands are no-ops.
# Mirrors the real status line: "<glyph> robots-<id> · <title> [... OPEN]", and
# resolves a bare id to its store-qualified form exactly as the real store does.
cat >"$STUB/robots" <<'EOF'
#!/bin/sh
if [ "$1" = show ]; then
  id=$2
  case "$id" in robots-*) : ;; *) id="robots-$id" ;; esac
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
bash "$SCRIPT" robots-test parlay >/dev/null 2>&1

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

# --- case 2: default/~ zone → no --worktree ----------------------------------
: > "$CAPTURE"
bash "$SCRIPT" robots-test default >/dev/null 2>&1

if grep -Fxq -- '--worktree' "$CAPTURE"; then
  fault "default/~ zone must NOT be isolated but got --worktree"
else
  pass "default/~ zone dispatched WITHOUT --worktree"
fi

# --- case 3: --bead is always passed, store-qualified -------------------------
# Without it, parlay-spawn's beads-required mode refuses the launch (exit 2) and
# no mechanic starts at all — the robots-aswz defect.
bead_arg() { grep -A1 -Fx -- '--bead' "$CAPTURE" | tail -1; }

: > "$CAPTURE"
bash "$SCRIPT" robots-test parlay >/dev/null 2>&1
if [ "$(bead_arg)" = robots-test ]; then
  pass "git-repo zone bound --bead robots-test"
else
  fault "git-repo zone did not pass --bead robots-test"
fi

: > "$CAPTURE"
bash "$SCRIPT" robots-test default >/dev/null 2>&1
if [ "$(bead_arg)" = robots-test ]; then
  pass "default/~ zone bound --bead robots-test"
else
  fault "default/~ zone did not pass --bead robots-test"
fi

# --- case 4: a BARE ticket id is qualified before binding ---------------------
# `mechanic-dispatch tnwd` is a legal call (robots resolves bare ids), but
# parlay-spawn derives the store from the id's leading token, so an unqualified
# --bead would resolve to no store at all.
: > "$CAPTURE"
bash "$SCRIPT" test default >/dev/null 2>&1
if [ "$(bead_arg)" = robots-test ]; then
  pass "bare ticket id qualified to robots-test for --bead"
else
  fault "bare ticket id was not qualified for --bead"
fi

if [ "$fail" -eq 0 ]; then
  echo "ALL PASS"
else
  echo "SOME TESTS FAILED"
fi
exit "$fail"
