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
# Usage:  install.sh [--rebuild] [--build] [--addr <host:port>] [--state-dir <path>]
#                     [--allowed-origins <comma,separated,origins>]
# Env:    PARLAY_SERVER_ADDR    listen addr      (default 127.0.0.1:4242)
#         PARLAY_STATE_HOME     state dir        (default ~/.parlay)
#         PARLAY_ALLOWED_ORIGINS  origin allow-list (default: preserve, see below)
#
# --allowed-origins bakes internal/guard's PARLAY_ALLOWED_ORIGINS allow-list
# into the rendered plist's EnvironmentVariables (launchd does not inherit
# the shell environment, so this is the only way the installed, launchd-run
# server ever sees it). Precedence when re-installing: an explicit
# --allowed-origins (even "") wins; otherwise PARLAY_ALLOWED_ORIGINS in this
# script's own environment; otherwise whatever value the currently-installed
# plist already has (parlay_goserver_installed_allowed_origins in lib.sh) —
# so a plain re-install (e.g. after --rebuild) never silently drops a
# previously configured allow-list.
#
# Frontend bundles are copied to a stable location so dev builds in the repo
# never disturb the live server:
#   packages/client/dist/   → ~/Library/Application Support/parlay/dist/        (panel, served at /)
#   packages/webview/dist/  → ~/Library/Application Support/parlay/dist/fleet/  (fleet dashboard, /fleet/)
# Default is copy-only: whatever dist/ each package already has is what ships,
# and a missing dist/ is a warning, not a failure. --build (opt-in) runs both
# packages' `bun run build` first, with the client's live-reload ping disabled
# (PARLAY_RELOAD_TARGET=off) so building for THIS install never reloads panels
# connected to some other server mid-copy.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "${HERE}/lib.sh"

REBUILD=0
BUILD_FRONTEND=0
ADDR="${PARLAY_SERVER_ADDR:-${PARLAY_GOSERVER_ADDR_DEFAULT}}"
STATE_DIR="${PARLAY_STATE_HOME:-${PARLAY_GOSERVER_STATE_DEFAULT}}"
ALLOWED_ORIGINS=""
ALLOWED_ORIGINS_ARG_SET=0
while [ $# -gt 0 ]; do
  case "$1" in
    --rebuild)   REBUILD=1; shift ;;
    --build)     BUILD_FRONTEND=1; shift ;;
    --addr)      ADDR="${2:?--addr needs a host:port}"; shift 2 ;;
    --state-dir) STATE_DIR="${2:?--state-dir needs a path}"; shift 2 ;;
    --allowed-origins)
      # Not "${2:?...}": that treats an empty string ("" — the explicit
      # "clear it" spelling) the same as a missing arg. Only a truly absent
      # $2 is an error.
      [ $# -ge 2 ] || { echo "install.sh: --allowed-origins needs a value" >&2; exit 2; }
      ALLOWED_ORIGINS="$2"; ALLOWED_ORIGINS_ARG_SET=1; shift 2 ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "install.sh: unknown arg: $1" >&2; exit 2 ;;
  esac
done

# Resolve --allowed-origins precedence: explicit flag > env var > whatever a
# prior install already baked into the plist (never silently dropped).
if [ "${ALLOWED_ORIGINS_ARG_SET}" != 1 ]; then
  if [ -n "${PARLAY_ALLOWED_ORIGINS:-}" ]; then
    ALLOWED_ORIGINS="${PARLAY_ALLOWED_ORIGINS}"
  else
    ALLOWED_ORIGINS="$(parlay_goserver_installed_allowed_origins)"
  fi
fi

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
REPO_ROOT="$(cd "${MODULE_DIR}/../.." && pwd)"

# ── 0. Optionally build the frontend bundles first (--build, opt-in) ───────────
# Copy-only is the default: the live server's assets come from whatever dist/
# each package already holds, and building stays a deliberate step. The
# client's build.ts normally POSTs /api/chat/reload after a successful build
# (live-upgrade for connected panels) — here that ping is disabled, because
# this build's output only goes live at step 2's copy, and reloading panels
# against a half-copied bundle is the exact race the stable assets dir exists
# to avoid. Re-run install.sh to deploy; the server picks the new files up
# per-request (no restart needed for assets).
if [ "${BUILD_FRONTEND}" = 1 ]; then
  echo "==> building packages/client (reload ping disabled)" >&2
  ( cd "${REPO_ROOT}/packages/client" && PARLAY_RELOAD_TARGET=off bun run build )
  echo "==> building packages/webview" >&2
  ( cd "${REPO_ROOT}/packages/webview" && bun run build )
fi

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
# Copy the frontend bundles to the stable assets dir so dev builds in the repo
# (which write to each package's dist/) never disturb the live server. The
# layout mirrors what cmd/parlay-server/main.go serves: the assets root is the
# panel (mux "/"), and <assets-dir>/fleet is the webview fleet dashboard
# (mux "/fleet/" — main.go joins "fleet" onto -assets-dir itself).
CLIENT_DIST="${REPO_ROOT}/packages/client/dist"
if [ -d "${CLIENT_DIST}" ]; then
  echo "==> copying client dist to ${PARLAY_GOSERVER_ASSETS_DIR}" >&2
  cp -r "${CLIENT_DIST}/." "${PARLAY_GOSERVER_ASSETS_DIR}/"
else
  echo "install.sh: warning: packages/client/dist not found at ${CLIENT_DIST} — run install.sh --build (or 'bun run build' in packages/client) first" >&2
fi
WEBVIEW_DIST="${REPO_ROOT}/packages/webview/dist"
if [ -d "${WEBVIEW_DIST}" ]; then
  echo "==> copying webview dist to ${PARLAY_GOSERVER_ASSETS_DIR}/fleet" >&2
  mkdir -p "${PARLAY_GOSERVER_ASSETS_DIR}/fleet"
  cp -r "${WEBVIEW_DIST}/." "${PARLAY_GOSERVER_ASSETS_DIR}/fleet/"
else
  echo "install.sh: warning: packages/webview/dist not found at ${WEBVIEW_DIST} — /fleet/ will 404; run install.sh --build (or 'bun run build' in packages/webview) first" >&2
fi

# ── 3. Render the plist from the template ──────────────────────────────────────
echo "==> writing ${PARLAY_GOSERVER_PLIST}" >&2
TEMPLATE="${HERE}/com.parlay.go-server.plist.template"
[ -r "${TEMPLATE}" ] || { echo "install.sh: missing template ${TEMPLATE}" >&2; exit 1; }
mkdir -p "${STATE_DIR}"
# ALLOWED_ORIGINS is the one templated value that can hold arbitrary
# user-supplied text (URLs, possibly with "&"), so it needs both XML-entity
# escaping (it lands inside a plist <string>) and sed-replacement escaping
# (a literal "&"/"|"/"\" in a sed replacement is special) before substitution.
allowed_origins_xml="${ALLOWED_ORIGINS//&/&amp;}"
allowed_origins_xml="${allowed_origins_xml//</&lt;}"
allowed_origins_xml="${allowed_origins_xml//>/&gt;}"
allowed_origins_sed="${allowed_origins_xml//\\/\\\\}"
allowed_origins_sed="${allowed_origins_sed//|/\\|}"
allowed_origins_sed="${allowed_origins_sed//&/\\&}"
# sed with a pipe delimiter so paths containing slashes are safe.
sed \
  -e "s|__LABEL__|${PARLAY_GOSERVER_LABEL}|g" \
  -e "s|__BIN__|${PARLAY_GOSERVER_BIN}|g" \
  -e "s|__ADDR__|${ADDR}|g" \
  -e "s|__STATE_DIR__|${STATE_DIR}|g" \
  -e "s|__ASSETS_DIR__|${PARLAY_GOSERVER_ASSETS_DIR}|g" \
  -e "s|__OUT_LOG__|${PARLAY_GOSERVER_OUT_LOG}|g" \
  -e "s|__ERR_LOG__|${PARLAY_GOSERVER_ERR_LOG}|g" \
  -e "s|__ALLOWED_ORIGINS__|${allowed_origins_sed}|g" \
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
  echo "    allowed-origins : ${ALLOWED_ORIGINS:-(none)}" >&2
  echo "    logs   : ${PARLAY_GOSERVER_OUT_LOG} / ${PARLAY_GOSERVER_ERR_LOG}" >&2
  launchctl print "${TARGET}" 2>/dev/null | grep -E '^\s*(state|pid|program|last exit) ' || true
else
  echo "install.sh: parlay-server did not answer /health within 5s — check ${PARLAY_GOSERVER_ERR_LOG}" >&2
  exit 1
fi
