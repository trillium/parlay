#!/usr/bin/env bash
# Install parlay-server (packages/go-server/cmd/parlay-server, the Go rewrite
# of packages/server, Pulse's HTTP/SSE chat server) as a supervised,
# always-on macOS LaunchAgent.
#
# What it does (idempotent — safe to re-run to update):
#   1. Builds packages/go-server/parlay-server if it is missing (or --rebuild).
#   2. Copies the binary + lib.sh to a stable install dir:
#        ~/Library/Application Support/parlay/bin/
#      so the LaunchAgent never depends on the git checkout location.
#   3. Renders com.parlay.go-server.plist from the template with resolved
#      absolute paths/values and writes it to ~/Library/LaunchAgents/.
#   4. bootstrap + enable + kickstart the agent in the gui/<uid> domain, so it
#      runs now, restarts on crash (KeepAlive), and starts at login (RunAtLoad).
#   5. Verifies the server answers GET /health.
#
# Usage:  install.sh [--rebuild] [--addr <host:port>] [--state-dir <path>]
# Env:    PARLAY_SERVER_ADDR  listen addr (default 127.0.0.1:4242)
#         PARLAY_STATE_HOME   state dir   (default ~/.parlay)
#
# The client bundle (packages/client/dist/) is copied to a stable location
# (~/Library/Application Support/parlay/dist/) so dev builds never disturb
# the live server. To deploy a new client build: re-run install.sh (or just
# copy manually: cp -r packages/client/dist/. "~/Library/Application Support/parlay/dist/").
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "${HERE}/lib.sh"

REBUILD=0
ADDR="${PARLAY_SERVER_ADDR:-${PARLAY_GOSERVER_ADDR_DEFAULT}}"
STATE_DIR="${PARLAY_STATE_HOME:-${PARLAY_GOSERVER_STATE_DEFAULT}}"
while [ $# -gt 0 ]; do
  case "$1" in
    --rebuild)   REBUILD=1; shift ;;
    --addr)      ADDR="${2:?--addr needs a host:port}"; shift 2 ;;
    --state-dir) STATE_DIR="${2:?--state-dir needs a path}"; shift 2 ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "install.sh: unknown arg: $1" >&2; exit 2 ;;
  esac
done

case "$(uname -s)" in
  Darwin) : ;;
  *) echo "install.sh: launchd deployment is macOS-only (got $(uname -s))" >&2; exit 1 ;;
esac

if parlay_goserver_refuse_31337 "${ADDR}"; then
  echo "install.sh: refusing --addr ${ADDR} — :31337 is the captain's live production Pulse server (see this repo's CLAUDE.md)" >&2
  exit 1
fi

MODULE_DIR="$(cd "${HERE}/.." && pwd)"     # packages/go-server
REPO_BIN="${MODULE_DIR}/parlay-server"

# ── 1. Build if needed ─────────────────────────────────────────────────────────
if [ "${REBUILD}" = 1 ] || [ ! -x "${REPO_BIN}" ]; then
  echo "==> building parlay-server binary" >&2
  ( cd "${MODULE_DIR}" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o parlay-server ./cmd/parlay-server )
fi
[ -x "${REPO_BIN}" ] || { echo "install.sh: build did not produce ${REPO_BIN}" >&2; exit 1; }

# ── 2. Install binary + lib + client dist to the stable support dir ─────────────
echo "==> installing to ${PARLAY_GOSERVER_BIN_DIR}" >&2
mkdir -p "${PARLAY_GOSERVER_BIN_DIR}" "${PARLAY_GOSERVER_LOG_DIR}" "${PARLAY_GOSERVER_ASSETS_DIR}" "$(dirname "${PARLAY_GOSERVER_PLIST}")"
# Copy to a temp name then mv, so an install over a running server swaps the
# file atomically rather than truncating a binary that may be exec'd on restart.
install -m 0755 "${REPO_BIN}" "${PARLAY_GOSERVER_BIN}.new" && mv -f "${PARLAY_GOSERVER_BIN}.new" "${PARLAY_GOSERVER_BIN}"
install -m 0644 "${HERE}/lib.sh" "${PARLAY_GOSERVER_LIB}"
# Copy the client bundle to the stable assets dir so dev builds in the repo
# (which write to packages/client/dist) never disturb the live server.
CLIENT_DIST="$(cd "${MODULE_DIR}/../.." && pwd)/packages/client/dist"
if [ -d "${CLIENT_DIST}" ]; then
  echo "==> copying client dist to ${PARLAY_GOSERVER_ASSETS_DIR}" >&2
  cp -r "${CLIENT_DIST}/." "${PARLAY_GOSERVER_ASSETS_DIR}/"
else
  echo "install.sh: warning: packages/client/dist not found at ${CLIENT_DIST} — run 'bun run build' in packages/client first" >&2
fi

# ── 3. Render the plist from the template ──────────────────────────────────────
echo "==> writing ${PARLAY_GOSERVER_PLIST}" >&2
TEMPLATE="${HERE}/com.parlay.go-server.plist.template"
[ -r "${TEMPLATE}" ] || { echo "install.sh: missing template ${TEMPLATE}" >&2; exit 1; }
mkdir -p "${STATE_DIR}"
# sed with a pipe delimiter so paths containing slashes are safe.
sed \
  -e "s|__LABEL__|${PARLAY_GOSERVER_LABEL}|g" \
  -e "s|__BIN__|${PARLAY_GOSERVER_BIN}|g" \
  -e "s|__ADDR__|${ADDR}|g" \
  -e "s|__STATE_DIR__|${STATE_DIR}|g" \
  -e "s|__ASSETS_DIR__|${PARLAY_GOSERVER_ASSETS_DIR}|g" \
  -e "s|__OUT_LOG__|${PARLAY_GOSERVER_OUT_LOG}|g" \
  -e "s|__ERR_LOG__|${PARLAY_GOSERVER_ERR_LOG}|g" \
  "${TEMPLATE}" > "${PARLAY_GOSERVER_PLIST}.new"
mv -f "${PARLAY_GOSERVER_PLIST}.new" "${PARLAY_GOSERVER_PLIST}"
# Validate the rendered plist before asking launchd to load it.
if ! plutil -lint "${PARLAY_GOSERVER_PLIST}" >/dev/null; then
  echo "install.sh: rendered plist failed plutil -lint" >&2
  exit 1
fi

# ── 4. (Re)load into launchd, gui/<uid> domain ─────────────────────────────────
DOMAIN="$(parlay_goserver_domain)"
TARGET="${DOMAIN}/${PARLAY_GOSERVER_LABEL}"

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
launchctl bootstrap "${DOMAIN}" "${PARLAY_GOSERVER_PLIST}"
launchctl enable "${TARGET}"
# kickstart -k forces a (re)start now regardless of throttle, so install is live
# immediately rather than waiting for the next RunAtLoad.
launchctl kickstart -k "${TARGET}"

# ── 5. Verify it is up ─────────────────────────────────────────────────────────
echo "==> verifying parlay-server health (http://${ADDR}/health)" >&2
ok=0
for _ in $(seq 1 20); do
  if parlay_goserver_health_ok "${ADDR}"; then ok=1; break; fi
  sleep 0.25
done
if [ "${ok}" = 1 ]; then
  echo "OK: parlay-server is up under launchd (${TARGET})" >&2
  echo "    addr   : http://${ADDR}" >&2
  echo "    state  : ${STATE_DIR}" >&2
  echo "    assets : ${PARLAY_GOSERVER_ASSETS_DIR}" >&2
  echo "    logs   : ${PARLAY_GOSERVER_OUT_LOG} / ${PARLAY_GOSERVER_ERR_LOG}" >&2
  launchctl print "${TARGET}" 2>/dev/null | grep -E '^\s*(state|pid|program|last exit) ' || true
else
  echo "install.sh: parlay-server did not answer /health within 5s — check ${PARLAY_GOSERVER_ERR_LOG}" >&2
  exit 1
fi
