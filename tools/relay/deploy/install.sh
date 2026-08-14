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
# Usage:  install.sh [--rebuild] [--server <url>] [--allow-non-default-server]
# Env:    PARLAY_SERVER  upstream Pulse server (default http://localhost:31337).
#                        Refused unless it IS the default — see below.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "${HERE}/lib.sh"

REBUILD=0
ALLOW_NON_DEFAULT=0
SERVER="${PARLAY_SERVER:-${PARLAY_RELAY_SERVER_DEFAULT}}"
# Plain `if`, not `[ … ] && VAR=1`: under `set -e` an AND-OR list whose final
# command never runs returns non-zero and takes the whole script down.
SERVER_FROM_ENV=0
if [ -n "${PARLAY_SERVER:-}" ]; then SERVER_FROM_ENV=1; fi
while [ $# -gt 0 ]; do
  case "$1" in
    --rebuild) REBUILD=1; shift ;;
    --server)  SERVER="${2:?--server needs a URL}"; SERVER_FROM_ENV=0; shift 2 ;;
    --allow-non-default-server) ALLOW_NON_DEFAULT=1; shift ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "install.sh: unknown arg: $1" >&2; exit 2 ;;
  esac
done
SERVER="${SERVER%/}"

# ── The LaunchAgent may only serve the DEFAULT upstream server (robots-93xu) ───
# This installs a FIXED singleton: the launcher resolves the CANONICAL runtime
# dir from getconf (launchd does not inherit our env), so whatever server we bake
# in here is what the canonical dir's relay is bound to — permanently, across
# reboots. And the canonical dir is RESERVED for the default server (see
# parlay_relay_scoped_runtime_dir in lib.sh): every agent on the default server
# resolves to it, finds a relay bound to something else, and is refused enrollment
# by parlay-monitor.sh's pre-enroll check. That is a fleet-wide outage whose only
# symptom is agents failing to enroll.
#
# It is exactly how robots-93xu happened: `SERVER` defaults from an AMBIENT
# $PARLAY_SERVER, so an install run from any shell that happened to export a
# non-default one (a go-server dev shell on :4242, say) silently rebound the
# captain's production relay. An ambient env var must never be able to do that.
#
# A non-default server belongs in its own scoped runtime dir with its own,
# unsupervised relay — which ensure-up.sh already starts on demand. The override
# exists for a box whose whole fleet genuinely moved, and must be deliberate.
if [ "${SERVER}" != "${PARLAY_RELAY_SERVER_DEFAULT%/}" ] && [ "${ALLOW_NON_DEFAULT}" != 1 ]; then
  echo "install.sh: refusing to install the LaunchAgent bound to ${SERVER}." >&2
  if [ "${SERVER_FROM_ENV}" = 1 ]; then
    echo "install.sh:   That came from the ambient \$PARLAY_SERVER in this shell, not" >&2
    echo "install.sh:   from --server — almost certainly not what you meant." >&2
  fi
  echo "install.sh: the LaunchAgent always serves the CANONICAL runtime dir, which is" >&2
  echo "install.sh:   reserved for the default server ${PARLAY_RELAY_SERVER_DEFAULT}." >&2
  echo "install.sh:   Binding it elsewhere refuses enrollment for every default-server" >&2
  echo "install.sh:   agent on this box (robots-93xu)." >&2
  echo "install.sh: for the default server:  $0 --server ${PARLAY_RELAY_SERVER_DEFAULT}" >&2
  echo "install.sh: a non-default server needs no install at all — ensure-up.sh starts a" >&2
  echo "install.sh:   scoped relay for it on demand." >&2
  echo "install.sh: if you really mean to rebind this box's supervised relay:" >&2
  echo "install.sh:   $0 --server ${SERVER} --allow-non-default-server" >&2
  exit 2
fi

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
# Adaptive, not a fixed 5s (robots-mpr3): a relay whose runtime dir already holds
# a large spool spends real time resuming those agents, and a fixed bound would
# fail a perfectly good install. parlay_relay_wait_health keeps waiting while the
# relay is demonstrably still working and gives up on a quiet one.
ok=0
if parlay_relay_wait_health; then ok=1; fi
if [ "${ok}" = 1 ]; then
  echo "OK: relay is up under launchd (${TARGET})" >&2
  echo "    socket : $(parlay_relay_sock)" >&2
  echo "    logs   : ${PARLAY_RELAY_OUT_LOG} / ${PARLAY_RELAY_ERR_LOG}" >&2
  launchctl print "${TARGET}" 2>/dev/null | grep -E '^\s*(state|pid|program|last exit) ' || true
else
  echo "install.sh: relay did not answer /health within ${PARLAY_RELAY_HEALTH_WAIT}s of quiet — check ${PARLAY_RELAY_ERR_LOG}" >&2
  exit 1
fi
