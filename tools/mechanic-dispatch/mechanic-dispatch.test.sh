#!/usr/bin/env bash
# mechanic-dispatch.test.sh — proves the worktree-isolation behavior:
#   • a git-repo zone (parlay) dispatches with --worktree AND keeps --cwd <repo>
#   • the default/~ zone dispatches WITHOUT --worktree
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
cat >"$STUB/robots" <<'EOF'
#!/bin/sh
if [ "$1" = show ]; then
  printf '%s\n' "robots-test - Fix the thing [OPEN]"
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

if [ "$fail" -eq 0 ]; then
  echo "ALL PASS"
else
  echo "SOME TESTS FAILED"
fi
exit "$fail"
