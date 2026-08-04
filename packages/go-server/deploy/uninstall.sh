#!/usr/bin/env bash
# Fully remove the parlay-server LaunchAgent — leave no trace, but nothing is
# ever permanently deleted: every removal below moves its target to the real
# Finder Trash (via parlay_goserver_trash_put in lib.sh — `trash` CLI if
# available, else a manual move into ~/.Trash), never `rm -rf`/`rm -f`. This
# is deliberate: an earlier version of this script used plain `rm -rf` for
# --purge and, combined with the state-dir bug below, permanently destroyed a
# live ~/.parlay outside this deploy's own test sandbox during a smoke test.
#
# What it does:
#   1. bootout the agent from launchd (stops it; KeepAlive no longer restarts it).
#   2. Resolve the state dir actually used at install time by reading it back
#      out of the installed plist (before touching the plist) — see
#      parlay_goserver_installed_state_dir in lib.sh. --purge must act on
#      what was really installed, not assume the coded default, since
#      install.sh's --state-dir/PARLAY_STATE_HOME can override it.
#   3. Trash the installed plist from ~/Library/LaunchAgents/.
#   4. Trash the installed binary + lib.sh copy (only these two files — never
#      the shared ".../parlay/bin" directory itself, which other services'
#      installed binaries, e.g. the relay, may also live in).
#   5. Optionally (--purge) also trash logs and the resolved state dir
#      (message/draft/upload history). Default keeps both so a reinstall does
#      not lose data.
#
# Usage:  uninstall.sh [--purge]
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# Prefer the repo lib; fall back to the installed copy so uninstall works even
# if run from outside the repo (e.g. copy-pasted onto another machine).
if [ -r "${HERE}/lib.sh" ]; then
  # shellcheck source=lib.sh
  . "${HERE}/lib.sh"
elif [ -r "${HOME}/Library/Application Support/parlay/bin/parlay-server-lib.sh" ]; then
  # shellcheck source=/dev/null
  . "${HOME}/Library/Application Support/parlay/bin/parlay-server-lib.sh"
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

DOMAIN="$(parlay_goserver_domain)"
TARGET="${DOMAIN}/${PARLAY_GOSERVER_LABEL}"

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

# ── 2. Resolve the real installed state dir BEFORE the plist is removed ────────
# (only matters for --purge, but resolve unconditionally — cheap, and keeps
# this step visibly ordered ahead of plist removal for anyone reading/editing
# this script later).
RESOLVED_STATE_DIR="$(parlay_goserver_installed_state_dir)"

# ── 3. Trash the plist ─────────────────────────────────────────────────────────
if [ -e "${PARLAY_GOSERVER_PLIST}" ]; then
  echo "==> trashing ${PARLAY_GOSERVER_PLIST}" >&2
  parlay_goserver_trash_put "${PARLAY_GOSERVER_PLIST}"
fi

# ── 4. Trash the installed binary + lib copy (surgical, not the shared dir) ────
if [ -e "${PARLAY_GOSERVER_BIN}" ]; then
  echo "==> trashing ${PARLAY_GOSERVER_BIN}" >&2
  parlay_goserver_trash_put "${PARLAY_GOSERVER_BIN}"
fi
if [ -e "${PARLAY_GOSERVER_LIB}" ]; then
  parlay_goserver_trash_put "${PARLAY_GOSERVER_LIB}"
fi

# ── 5. Optionally purge state dir + logs (trashed, not deleted) ────────────────
if [ "${PURGE}" = 1 ]; then
  if [ -d "${RESOLVED_STATE_DIR}" ]; then
    echo "==> trashing state dir ${RESOLVED_STATE_DIR}" >&2
    parlay_goserver_trash_put "${RESOLVED_STATE_DIR}"
  fi
  if [ -f "${PARLAY_GOSERVER_OUT_LOG}" ] || [ -f "${PARLAY_GOSERVER_ERR_LOG}" ]; then
    echo "==> trashing logs" >&2
    parlay_goserver_trash_put "${PARLAY_GOSERVER_OUT_LOG}"
    parlay_goserver_trash_put "${PARLAY_GOSERVER_ERR_LOG}"
  fi
else
  echo "==> kept state dir (${RESOLVED_STATE_DIR}) and logs; pass --purge to trash them" >&2
fi

echo "OK: parlay-server LaunchAgent removed." >&2
