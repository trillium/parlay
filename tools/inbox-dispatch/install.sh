#!/bin/sh
# install.sh — install the inbox dispatcher command to
# ~/.local/bin/inbox-dispatch, backing up any pre-existing copy once.
# Idempotent and reversible (mirrors tools/mechanic-dispatch/install.sh):
#   install:   tools/inbox-dispatch/install.sh
#   uninstall: tools/inbox-dispatch/install.sh --uninstall   (restores the backup)
#   status:    tools/inbox-dispatch/install.sh --status
set -eu

SELF_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
SRC="$SELF_DIR/inbox-dispatch"
DEST="$HOME/.local/bin/inbox-dispatch"
BAK="$HOME/.local/bin/inbox-dispatch.pre-install.bak"

case "${1:-install}" in
  --status)
    if [ -f "$BAK" ]; then echo "canonical inbox-dispatch INSTALLED (backup at $BAK)"; else echo "no prior backup recorded"; fi
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
# Back up the current copy ONCE (never overwrite an existing backup).
if [ -f "$DEST" ] && [ ! -f "$BAK" ]; then
  cp "$DEST" "$BAK"
  echo "backed up original → $BAK"
fi
cp "$SRC" "$DEST"
chmod +x "$DEST"
echo "installed inbox dispatcher → $DEST"
