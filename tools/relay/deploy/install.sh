#!/usr/bin/env bash
# Install the Parlay relay as a supervised, always-on macOS LaunchAgent.
#
# What it does (idempotent — safe to re-run to update):
#   1. Builds tools/relay/parlay-relay if it is missing (or --rebuild).
#   2. Copies the binary + launcher + lib.sh to a stable install dir:
#        ~/Library/Application Support/parlay/bin/
#      so the LaunchAgent never depends on the git checkout location.
#   3. Renders com.parlay.relay.plist from the template with resolved absolute
#      paths and writes it to ~/Library/LaunchAgents/.
#   4. bootstrap + enable + kickstart the agent in the gui/<uid> domain, so it
#      runs now, restarts on crash (KeepAlive), and starts at login (RunAtLoad).
#   5. Verifies the relay answers /health on its control socket.
#
# ADDITIVE ONLY: this touches nothing about existing agent monitors. It stands up
# a relay on the shared per-user runtime dir; new `parlay monitor` enrollments use
# it, and the legacy poll path keeps working untouched.
#
# Usage:  install.sh [--rebuild] [--server <url>]
# Env:    PARLAY_SERVER  upstream Pulse server (default http://localhost:31337)
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "${HERE}/lib.sh"

REBUILD=0
SERVER="${PARLAY_SERVER:-${PARLAY_RELAY_SERVER_DEFAULT}}"
while [ $# -gt 0 ]; do
  case "$1" in
    --rebuild) REBUILD=1; shift ;;
    --server)  SERVER="${2:?--server needs a URL}"; shift 2 ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "install.sh: unknown arg: $1" >&2; exit 2 ;;
  esac
done

case "$(uname -s)" in
  Darwin) : ;;
  *) echo "install.sh: launchd deployment is macOS-only (got $(uname -s))" >&2; exit 1 ;;
esac

REPO_BIN="${HERE}/../parlay-relay"          # tools/relay/parlay-relay
BUILD_SH="${HERE}/../build.sh"

# ── 1. Build if needed ─────────────────────────────────────────────────────────
if [ "${REBUILD}" = 1 ] || [ ! -x "${REPO_BIN}" ]; then
  echo "==> building relay binary" >&2
  bash "${BUILD_SH}"
fi
[ -x "${REPO_BIN}" ] || { echo "install.sh: build did not produce ${REPO_BIN}" >&2; exit 1; }

# ── 2. Install binary + launcher + lib to the stable support dir ───────────────
echo "==> installing to ${PARLAY_RELAY_BIN_DIR}" >&2
mkdir -p "${PARLAY_RELAY_BIN_DIR}" "${PARLAY_RELAY_LOG_DIR}" "$(dirname "${PARLAY_RELAY_PLIST}")"
# Copy to a temp name then mv, so an install over a running relay swaps the file
# atomically rather than truncating a binary that may be exec'd on restart.
install -m 0755 "${REPO_BIN}" "${PARLAY_RELAY_BIN}.new" && mv -f "${PARLAY_RELAY_BIN}.new" "${PARLAY_RELAY_BIN}"
install -m 0755 "${HERE}/parlay-relay-launch.sh" "${PARLAY_RELAY_LAUNCHER}"
install -m 0644 "${HERE}/lib.sh" "${PARLAY_RELAY_BIN_DIR}/lib.sh"

# ── 3. Render the plist from the template ──────────────────────────────────────
echo "==> writing ${PARLAY_RELAY_PLIST}" >&2
TEMPLATE="${HERE}/com.parlay.relay.plist.template"
[ -r "${TEMPLATE}" ] || { echo "install.sh: missing template ${TEMPLATE}" >&2; exit 1; }
# sed with a control char delimiter so paths containing spaces/slashes are safe.
sed \
  -e "s|__LABEL__|${PARLAY_RELAY_LABEL}|g" \
  -e "s|__LAUNCHER__|${PARLAY_RELAY_LAUNCHER}|g" \
  -e "s|__SERVER__|${SERVER}|g" \
  -e "s|__OUT_LOG__|${PARLAY_RELAY_OUT_LOG}|g" \
  -e "s|__ERR_LOG__|${PARLAY_RELAY_ERR_LOG}|g" \
  "${TEMPLATE}" > "${PARLAY_RELAY_PLIST}.new"
mv -f "${PARLAY_RELAY_PLIST}.new" "${PARLAY_RELAY_PLIST}"
# Validate the rendered plist before asking launchd to load it.
if ! plutil -lint "${PARLAY_RELAY_PLIST}" >/dev/null; then
  echo "install.sh: rendered plist failed plutil -lint" >&2
  exit 1
fi

# ── 4. (Re)load into launchd, gui/<uid> domain ─────────────────────────────────
DOMAIN="$(parlay_relay_domain)"
TARGET="${DOMAIN}/${PARLAY_RELAY_LABEL}"

# If already loaded, bootout first so we load the new plist cleanly. Ignore the
# "not loaded" error on a fresh install.
if launchctl print "${TARGET}" >/dev/null 2>&1; then
  echo "==> booting out existing ${TARGET} to reload" >&2
  launchctl bootout "${TARGET}" 2>/dev/null || true
  # Wait for the old job to fully unload so bootstrap does not race it.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    launchctl print "${TARGET}" >/dev/null 2>&1 || break
    sleep 0.3
  done
fi

echo "==> bootstrapping ${TARGET}" >&2
launchctl bootstrap "${DOMAIN}" "${PARLAY_RELAY_PLIST}"
launchctl enable "${TARGET}"
# kickstart -k forces a (re)start now regardless of throttle, so install is live
# immediately rather than waiting for the next RunAtLoad.
launchctl kickstart -k "${TARGET}"

# ── 5. Verify it is up ─────────────────────────────────────────────────────────
echo "==> verifying relay health" >&2
ok=0
for _ in $(seq 1 20); do
  if parlay_relay_health_ok; then ok=1; break; fi
  sleep 0.25
done
if [ "${ok}" = 1 ]; then
  echo "OK: relay is up under launchd (${TARGET})" >&2
  echo "    socket : $(parlay_relay_sock)" >&2
  echo "    logs   : ${PARLAY_RELAY_OUT_LOG} / ${PARLAY_RELAY_ERR_LOG}" >&2
  launchctl print "${TARGET}" 2>/dev/null | grep -E '^\s*(state|pid|program|last exit) ' || true
else
  echo "install.sh: relay did not answer /health within 5s — check ${PARLAY_RELAY_ERR_LOG}" >&2
  exit 1
fi
