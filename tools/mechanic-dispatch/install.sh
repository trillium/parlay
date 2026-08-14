#!/bin/sh
# install.sh — install the robots mechanic-dispatch command to
# ~/.local/bin/mechanic-dispatch, backing up any pre-existing copy once.
# Idempotent and reversible (mirrors tools/robots-emit/install.sh):
#   install:   tools/mechanic-dispatch/install.sh
#   uninstall: tools/mechanic-dispatch/install.sh --uninstall   (restores the backup)
#   status:    tools/mechanic-dispatch/install.sh --status
#
# mechanic-dispatch is the deterministic robots-ticket -> mechanic-agent
# dispatcher (see the script header). It is the canonical source; the
# ~/.local/bin copy is an installed artifact — edit the repo file, then re-run
# this installer.
set -eu

SELF_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
SRC="$SELF_DIR/mechanic-dispatch"
DEST="$HOME/.local/bin/mechanic-dispatch"
BAK="$HOME/.local/bin/mechanic-dispatch.pre-worktree.bak"

case "${1:-install}" in
  --status)
    if [ -f "$BAK" ]; then echo "canonical mechanic-dispatch INSTALLED (backup at $BAK)"; else echo "no prior backup recorded"; fi
    grep -q 'WORKTREE_ARG' "$DEST" 2>/dev/null && echo "  live $DEST has the worktree-isolation logic" || echo "  live $DEST has NO worktree-isolation logic"
    exit 0
    ;;
  --uninstall)
    if [ -f "$BAK" ]; then
      cp "$BAK" "$DEST"; chmod +x "$DEST"; rm -f "$BAK"
      echo "restored original $DEST from backup"
    else
      echo "no backup at $BAK — nothing to restore" >&2; exit 1
    fi
    exit 0
    ;;
  install|"") : ;;
  *) echo "usage: install.sh [--status|--uninstall]" >&2; exit 2 ;;
esac

[ -f "$SRC" ] || { echo "missing source $SRC" >&2; exit 1; }
mkdir -p "$HOME/.local/bin"
# Back up the current command ONCE (never overwrite an existing backup, so a
# re-install can't clobber the true original with an already-modified copy).
if [ -f "$DEST" ] && [ ! -f "$BAK" ]; then
  cp "$DEST" "$BAK"
  echo "backed up existing → $BAK"
fi
cp "$SRC" "$DEST"
chmod +x "$DEST"
echo "installed mechanic-dispatch → $DEST"
echo "verify: mechanic-dispatch passes --worktree to parlay-spawn for git-repo zones; default/~ zone stays non-isolated"
