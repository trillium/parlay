#!/usr/bin/env bash
# install.sh — install the fleet layer (inbox/skills glue) into the live
# environment, leaving the core parlay product untouched.
#
# The fleet layer is personal/agent-adjacent tooling that does NOT belong in
# the core parlay repo's main product tree (commits 952c346e, 957634b2
# originally put it in tools/ and skills/). It lives under examples/fleet/ as
# authored source (Gas City authored-vs-live doctrine) and installs into:
#   ~/.local/bin/         inbox, inbox-dispatch            (backup-then-copy)
#   ~/.pi/agent/extensions/parlay-pi-inbox                  (Pi bridge, copy)
#   ~/.claude/skills/<n>   inbox-handler, parlay-spawn,
#                          voice-command-consulting         (symlink)
#
# Commands install via each tool's OWN install.sh (which backup + restore),
# matching tools/mechanic-dispatch and tools/robots-emit convention. Skills are
# symlinked (matching how parlay-spawn already was), so editing the repo source
# immediately updates what agents see.
#
# Usage:
#   examples/fleet/install.sh                 install everything
#   examples/fleet/install.sh --status        report what's installed
#   examples/fleet/install.sh --uninstall     remove fleet bits (restores
#                                             backups; skills symlinks removed)
set -euo pipefail

SELF_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
SKILLS_DIR="$SELF_DIR/skills"
SKILL_NAMES="inbox-handler parlay-spawn voice-command-consulting"

case "${1:-install}" in
  --status)
    echo "— fleet commands —"
    "$SELF_DIR/inbox-emit/install.sh" --status
    "$SELF_DIR/inbox-dispatch/install.sh" --status
    "$SELF_DIR/pi-inbox-bridge/install.sh" 2>/dev/null || echo "  pi-inbox-bridge: no status verb"
    echo "— fleet skills (~/.claude/skills) —"
    for name in $SKILL_NAMES; do
      target="$HOME/.claude/skills/$name"
      if [ -L "$target" ]; then
        echo "  $name → $(readlink "$target")"
      elif [ -e "$target" ]; then
        echo "  $name: real dir (not managed)"
      else
        echo "  $name: not installed"
      fi
    done
    exit 0
    ;;

  --uninstall)
    echo "— uninstalling fleet commands (restore backups) —"
    "$SELF_DIR/inbox-emit/install.sh" --uninstall
    "$SELF_DIR/inbox-dispatch/install.sh" --uninstall
    echo "— removing fleet skills symlinks —"
    for name in $SKILL_NAMES; do
      target="$HOME/.claude/skills/$name"
      if [ -L "$target" ]; then
        rm "$target"
        echo "  removed $target"
      fi
    done
    echo "note: ~/.pi/agent/extensions/parlay-pi-inbox not removed (Pi-owned); remove manually if desired"
    exit 0
    ;;

  install|"") : ;;
  *) echo "usage: install.sh [--status|--uninstall]" >&2; exit 2 ;;
esac

echo "— fleet commands —"
[ -x "$SELF_DIR/inbox-emit/install.sh" ] && "$SELF_DIR/inbox-emit/install.sh"
[ -x "$SELF_DIR/inbox-dispatch/install.sh" ] && "$SELF_DIR/inbox-dispatch/install.sh"
[ -x "$SELF_DIR/pi-inbox-bridge/install.sh" ] && "$SELF_DIR/pi-inbox-bridge/install.sh"

echo "— fleet skills (symlink into ~/.claude/skills) —"
mkdir -p "$HOME/.claude/skills"
for name in $SKILL_NAMES; do
  src="$SKILLS_DIR/$name"
  target="$HOME/.claude/skills/$name"
  if [ ! -d "$src" ]; then
    echo "  skip $name (source $src missing)"
    continue
  fi
  if [ -L "$target" ] && [ "$(readlink "$target")" = "$src" ]; then
    echo "  $name → already linked"
  elif [ -L "$target" ] || [ -e "$target" ]; then
    echo "  WARN $name: $target exists and is not our symlink — leaving it"
  else
    ln -s "$src" "$target"
    echo "  $name → linked"
  fi
done

echo "done. Verify: ls -l ~/.claude/skills | grep -E 'inbox-handler|parlay-spawn|voice-command'"