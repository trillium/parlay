#!/usr/bin/env bash
# install.sh — put `agent-mail` on PATH via a ~/.local/bin symlink.
# Federation convention: a small wrapper on PATH, real implementation
# versioned in this repo (examples/fleet/agent-mail-cli/agent-mail).
# Usage: install.sh [--status|--uninstall]
set -euo pipefail
SELF_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
TARGET="$HOME/.local/bin/agent-mail"

case "${1:-install}" in
  --status)
    if [ -L "$TARGET" ]; then echo "agent-mail → $(readlink "$TARGET")";
    elif [ -e "$TARGET" ]; then echo "agent-mail: real file (not managed)";
    else echo "agent-mail: not installed"; fi
    exit 0 ;;
  --uninstall)
    if [ -L "$TARGET" ]; then rm "$TARGET"; echo "removed $TARGET";
    else echo "nothing to remove ($TARGET not a symlink)"; fi
    if [ -e "$TARGET.bak" ]; then mv "$TARGET.bak" "$TARGET"
      echo "restored backup"; fi
    exit 0 ;;
  install|"") : ;;
  *) echo "usage: install.sh [--status|--uninstall]" >&2; exit 2 ;;
esac

mkdir -p "$HOME/.local/bin"
if [ -L "$TARGET" ] && [ "$(readlink "$TARGET")" = "$SELF_DIR/agent-mail" ]; then
  echo "agent-mail → already linked"
elif [ -e "$TARGET" ] && [ ! -e "$TARGET.bak" ]; then
  mv "$TARGET" "$TARGET.bak" && echo "backed up $TARGET → $TARGET.bak"
  ln -s "$SELF_DIR/agent-mail" "$TARGET" && echo "linked $TARGET"
else
  ln -sf "$SELF_DIR/agent-mail" "$TARGET" && echo "linked $TARGET"
fi
command -v agent-mail >/dev/null && agent-mail status --help >/dev/null \
  && echo "verify: agent-mail status"
