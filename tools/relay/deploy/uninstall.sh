#!/usr/bin/env bash
# Fully remove the Parlay relay LaunchAgent — leave no trace.
#
# What it does:
#   1. bootout the agent from launchd (stops it; KeepAlive no longer restarts it).
#   2. Remove the installed plist from ~/Library/LaunchAgents/.
#   3. Remove the installed binary/launcher/lib support dir.
#   4. Remove the control socket (spool files are left unless --purge, since a
#      lagging monitor may still be draining one; --purge removes the runtime dir).
#   5. Optionally (--purge) remove logs too.
#
# It does NOT touch any agent monitor process — those are independent. Removing
# the relay simply makes the relay-backed enroll path unavailable again; the
# legacy poll path is unaffected.
#
# Usage:  uninstall.sh [--purge]
#   --purge  also delete the runtime dir (spools) and log files. Default keeps
#            logs + any spools so nothing a monitor is mid-drain-on is yanked.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# Prefer the repo lib; fall back to the installed copy so uninstall works even if
# run from outside the repo.
if [ -r "${HERE}/lib.sh" ]; then
  # shellcheck source=lib.sh
  . "${HERE}/lib.sh"
elif [ -r "${HOME}/Library/Application Support/parlay/bin/lib.sh" ]; then
  # shellcheck source=/dev/null
  . "${HOME}/Library/Application Support/parlay/bin/lib.sh"
else
  echo "uninstall.sh: cannot find lib.sh (repo or installed)" >&2
  exit 1
fi

PURGE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --purge) PURGE=1; shift ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "uninstall.sh: unknown arg: $1" >&2; exit 2 ;;
  esac
done

DOMAIN="$(parlay_relay_domain)"
TARGET="${DOMAIN}/${PARLAY_RELAY_LABEL}"

# ── 1. Stop + unload from launchd ──────────────────────────────────────────────
if launchctl print "${TARGET}" >/dev/null 2>&1; then
  echo "==> booting out ${TARGET}" >&2
  launchctl bootout "${TARGET}" 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    launchctl print "${TARGET}" >/dev/null 2>&1 || break
    sleep 0.3
  done
else
  echo "==> ${TARGET} not loaded (nothing to bootout)" >&2
fi

# ── 2. Remove the plist ────────────────────────────────────────────────────────
if [ -e "${PARLAY_RELAY_PLIST}" ]; then
  echo "==> removing ${PARLAY_RELAY_PLIST}" >&2
  rm -f "${PARLAY_RELAY_PLIST}"
fi

# ── 3. Remove the installed support dir (binary, launcher, lib) ────────────────
if [ -d "${PARLAY_RELAY_SUPPORT_DIR}" ]; then
  echo "==> removing ${PARLAY_RELAY_SUPPORT_DIR}" >&2
  rm -rf "${PARLAY_RELAY_SUPPORT_DIR}"
fi

# ── 4. Remove the control socket (always) and runtime dir (--purge) ────────────
SOCK="$(parlay_relay_sock)"
RUNTIME="$(parlay_relay_runtime_dir)"
if [ -S "${SOCK}" ]; then
  echo "==> removing control socket ${SOCK}" >&2
  rm -f "${SOCK}"
fi
if [ "${PURGE}" = 1 ]; then
  if [ -d "${RUNTIME}" ]; then
    echo "==> purging runtime dir ${RUNTIME} (spools)" >&2
    rm -rf "${RUNTIME}"
  fi
  if [ -d "${PARLAY_RELAY_LOG_DIR}" ]; then
    echo "==> purging logs ${PARLAY_RELAY_LOG_DIR}" >&2
    rm -rf "${PARLAY_RELAY_LOG_DIR}"
  fi
else
  echo "==> kept runtime spools (${RUNTIME}) and logs (${PARLAY_RELAY_LOG_DIR}); pass --purge to delete" >&2
fi

echo "OK: relay LaunchAgent removed." >&2
