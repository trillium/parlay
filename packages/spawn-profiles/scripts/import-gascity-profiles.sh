#!/usr/bin/env bash
# import-gascity-profiles.sh — one-way REFERENCE import from gascity's builtin
# provider catalog into parlay's profiles.toml.
#
# This is deliberately NOT an auto-sync. It prints a per-profile summary of
# gascity's canonical builtin specs so a human/agent can eyeball the deltas and
# hand-port them into profiles.toml. parlay's profiles diverge on purpose (e.g.
# opencode-go/* models gascity doesn't list).
#
# Usage:
#   scripts/import-gascity-profiles.sh                 # default source
#   scripts/import-gascity-profiles.sh --source PATH   # override profiles.go
#
# Prints: profile name, command, prompt mode/flag, ready delay, resume flag/style.

set -euo pipefail

SOURCE="${GASCITY_PROFILES:-$HOME/code/gascity/internal/worker/builtin/profiles.go}"

while [ $# -gt 0 ]; do
  case "$1" in
    --source) SOURCE="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

if [ ! -f "$SOURCE" ]; then
  echo "gascity profiles.go not found: $SOURCE" >&2
  echo "pass --source PATH or set GASCITY_PROFILES" >&2
  exit 1
fi

echo "# gascity builtin profiles (reference only — hand-port into profiles.toml)"
echo "# source: $SOURCE"
echo

# Each profile is `"<name>": {`. Print its name plus the spawn fields that map
# onto parlay's schema. Skip the verbose OptionsSchema blocks.
awk '
  function interest(s) {
    return (s ~ /Command:/ || s ~ /PromptMode:/ || s ~ /PromptFlag:/ ||
            s ~ /ReadyDelayMs:/ || s ~ /ResumeFlag:/ || s ~ /ResumeStyle:/ ||
            s ~ /SessionIDFlag:/)
  }
  /^[[:space:]]*"[a-z0-9-]+": \{/ {
    name = $1
    gsub(/["{:]+/, "", name)
    if (first) print ""
    first = 1
    print "## " name
    next
  }
  interest($0) {
    line = $0
    sub(/^[[:space:]]+/, "", line)
    sub(/,[[:space:]]*$/, "", line)
    print "  " line
  }
' "$SOURCE"
