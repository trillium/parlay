#!/usr/bin/env bash
# Install the Parlay eval-engine as a supervised, always-on macOS LaunchAgent.
#
# WHY: the eval-engine (packages/eval-engine, compiled Go, :4343) computes every
# voice-command action for the Parlay chat panel. In the pure server-side-eval
# model the client does NO local matching, so if the engine is down EVERY phone
# voice command silently fails (submit "bravely", tab switches, clear, …). The
# relay runs supervised under launchd; the engine used to be a bare `nohup`
# process with no supervisor, so it died on a reboot and stayed down for days
# (robots-t9f). This gives it the same KeepAlive supervision the relay has.
#
# What it does (idempotent — safe to re-run to update):
#   1. Builds the engine binary if it is missing (or --rebuild), via `go build`.
#   2. Renders com.parlay.eval-engine.plist from the template with resolved
#      absolute paths (the plist execs the checkout binary in place — see the
#      template header for why we don't copy it out like the relay does).
#   3. Validates the rendered plist with plutil -lint.
#   4. bootout (if loaded) + bootstrap + enable + kickstart in gui/<uid>, so it
#      runs now, restarts on crash/kill (KeepAlive), and starts at login.
#   5. Verifies the engine answers /health.
#
# Usage:  install.sh [--rebuild]
# Env:    PARLAY_EVAL_ADDR  listen addr (default 127.0.0.1:4343)
set -euo pipefail

case "$(uname -s)" in
  Darwin) : ;;
  *) echo "install.sh: launchd deployment is macOS-only (got $(uname -s))" >&2; exit 1 ;;
esac

HERE="$(cd "$(dirname "$0")" && pwd)"

REBUILD=0
while [ $# -gt 0 ]; do
  case "$1" in
    --rebuild) REBUILD=1; shift ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "install.sh: unknown arg: $1" >&2; exit 2 ;;
  esac
done

# ── Paths ──────────────────────────────────────────────────────────────────────
ENGINE_DIR="$(cd "${HERE}/../../../packages/eval-engine" && pwd)"
ENGINE_BIN="${ENGINE_DIR}/parlay-eval-engine"
LABEL="com.parlay.eval-engine"
PLIST="${HOME}/Library/LaunchAgents/${LABEL}.plist"
LOG_DIR="${HOME}/Library/Logs/parlay"
OUT_LOG="${LOG_DIR}/eval-engine.out.log"
ERR_LOG="${LOG_DIR}/eval-engine.err.log"
EVAL_ADDR="${PARLAY_EVAL_ADDR:-127.0.0.1:4343}"
HEALTH_URL="http://${EVAL_ADDR}/health"

# ── 1. Build if needed ─────────────────────────────────────────────────────────
if [ "${REBUILD}" = 1 ] || [ ! -x "${ENGINE_BIN}" ]; then
  echo "==> building eval-engine binary" >&2
  ( cd "${ENGINE_DIR}" && go build -o parlay-eval-engine . )
fi
[ -x "${ENGINE_BIN}" ] || { echo "install.sh: no engine binary at ${ENGINE_BIN} (run with --rebuild)" >&2; exit 1; }

# ── 2. Render the plist from the template ──────────────────────────────────────
mkdir -p "${LOG_DIR}" "$(dirname "${PLIST}")"
TEMPLATE="${HERE}/com.parlay.eval-engine.plist.template"
[ -r "${TEMPLATE}" ] || { echo "install.sh: missing template ${TEMPLATE}" >&2; exit 1; }
echo "==> writing ${PLIST}" >&2
# sed with a pipe delimiter so paths with slashes are safe.
sed \
  -e "s|__LABEL__|${LABEL}|g" \
  -e "s|__ENGINE_BIN__|${ENGINE_BIN}|g" \
  -e "s|__ENGINE_DIR__|${ENGINE_DIR}|g" \
  -e "s|__EVAL_ADDR__|${EVAL_ADDR}|g" \
  -e "s|__OUT_LOG__|${OUT_LOG}|g" \
  -e "s|__ERR_LOG__|${ERR_LOG}|g" \
  "${TEMPLATE}" > "${PLIST}.new"
mv -f "${PLIST}.new" "${PLIST}"

# ── 3. Validate before asking launchd to load it ───────────────────────────────
if ! plutil -lint "${PLIST}" >/dev/null; then
  echo "install.sh: rendered plist failed plutil -lint" >&2
  exit 1
fi

# ── 4. (Re)load into launchd, gui/<uid> domain ─────────────────────────────────
DOMAIN="gui/$(id -u)"
TARGET="${DOMAIN}/${LABEL}"

if launchctl print "${TARGET}" >/dev/null 2>&1; then
  echo "==> booting out existing ${TARGET} to reload" >&2
  launchctl bootout "${TARGET}" 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    launchctl print "${TARGET}" >/dev/null 2>&1 || break
    sleep 0.3
  done
fi

echo "==> bootstrapping ${TARGET}" >&2
launchctl bootstrap "${DOMAIN}" "${PLIST}"
launchctl enable "${TARGET}"
launchctl kickstart -k "${TARGET}"

# ── 5. Verify it is up ─────────────────────────────────────────────────────────
echo "==> verifying engine health (${HEALTH_URL})" >&2
ok=0
for _ in $(seq 1 20); do
  if curl -fsS -o /dev/null "${HEALTH_URL}" 2>/dev/null; then ok=1; break; fi
  sleep 0.25
done
if [ "${ok}" = 1 ]; then
  echo "OK: eval-engine is up under launchd (${TARGET})" >&2
  echo "    addr : ${EVAL_ADDR}" >&2
  echo "    logs : ${OUT_LOG} / ${ERR_LOG}" >&2
  launchctl print "${TARGET}" 2>/dev/null | grep -E '^\s*(state|pid|program|last exit) ' || true
else
  echo "install.sh: engine did not answer /health within 5s — check ${ERR_LOG}" >&2
  exit 1
fi
