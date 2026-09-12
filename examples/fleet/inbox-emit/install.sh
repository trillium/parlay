#!/bin/sh
# install.sh — install the inbox create-emit wrapper (inbox routing) over
# ~/.local/bin/inbox, backing up the original once. Idempotent and reversible:
#   install:   tools/inbox-emit/install.sh
#   uninstall: tools/inbox-emit/install.sh --uninstall   (restores the backup)
#   status:    tools/inbox-emit/install.sh --status
set -eu

SELF_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
SRC="$SELF_DIR/inbox"
DEST="$HOME/.local/bin/inbox"
BAK="$HOME/.local/bin/inbox.pre-emit.bak"

case "${1:-install}" in
  --status)
    if [ -f "$BAK" ]; then echo "emit wrapper INSTALLED (backup at $BAK)"; else echo "original (no emit wrapper installed)"; fi
    grep -q 'inbox-create' "$DEST" 2>/dev/null && echo "  live $DEST has the create-emit hook" || echo "  live $DEST has NO emit hook"
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
# Back up the current wrapper ONCE (never overwrite an existing backup, so a
# re-install can't clobber the true original with an already-modified copy).
if [ -f "$DEST" ] && [ ! -f "$BAK" ]; then
  cp "$DEST" "$BAK"
  echo "backed up original → $BAK"
fi
cp "$SRC" "$DEST"
chmod +x "$DEST"
echo "installed create-emit wrapper → $DEST"
echo "verify: inbox create emits to \${INBOX_EVENTS_FILE:-~/data/inbox/events.jsonl}; all other subcommands unchanged"
