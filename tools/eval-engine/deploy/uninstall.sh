#!/usr/bin/env bash
# Remove the Parlay eval-engine LaunchAgent (com.parlay.eval-engine).
#
# Boots the job out of launchd and deletes the rendered plist. Leaves the checkout
# binary and logs in place. Idempotent — safe to run when nothing is installed.
#
# Usage:  uninstall.sh
set -euo pipefail

LABEL="com.parlay.eval-engine"
PLIST="${HOME}/Library/LaunchAgents/${LABEL}.plist"
TARGET="gui/$(id -u)/${LABEL}"

if launchctl print "${TARGET}" >/dev/null 2>&1; then
  echo "==> booting out ${TARGET}" >&2
  launchctl bootout "${TARGET}" 2>/dev/null || true
fi

if [ -f "${PLIST}" ]; then
  echo "==> removing ${PLIST}" >&2
  rm -f "${PLIST}"
fi

echo "OK: eval-engine LaunchAgent removed" >&2
