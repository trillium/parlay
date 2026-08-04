#!/usr/bin/env bash
# ensure-up — guarantee parlay-server is answering /health, so a caller (CLI,
# client, another tool) never dead-ends on a missing server.
#
# Idempotent and concurrency-safe:
#   * If the server already answers /health, returns 0 immediately (no start).
#   * Otherwise it acquires a per-user lock (so two callers starting at once
#     do not both launch a server), re-checks health under the lock, and
#     starts the server by the best available method:
#       1. launchd — if the LaunchAgent is installed, `launchctl kickstart` it
#          (the supervised path; KeepAlive keeps it up afterwards).
#       2. installed binary — ~/Library/Application Support/parlay/bin,
#          backgrounded + disowned (unsupervised fallback).
#       3. repo binary — packages/go-server/parlay-server, built if missing,
#          backgrounded + disowned (dev fallback when nothing is installed).
#   * Then waits (bounded) for /health to come up and returns 0/1 accordingly.
#
# Usage:  ensure-up.sh            # ensure the server is up; exit 0 if up, 1 if not
#         ensure-up.sh --quiet    # suppress the informational stderr line
# Env:    PARLAY_SERVER_ADDR  target addr (default 127.0.0.1:4242)
#         PARLAY_STATE_HOME   state dir   (default ~/.parlay)
# Exit:   0 server is up (already, or started); 1 could not bring it up.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# Prefer the installed lib (this script may be run from the install dir); fall
# back to the repo copy.
if [ -r "${HERE}/lib.sh" ]; then
  # shellcheck source=lib.sh
  . "${HERE}/lib.sh"
elif [ -r "${HOME}/Library/Application Support/parlay/bin/parlay-server-lib.sh" ]; then
  # shellcheck source=/dev/null
  . "${HOME}/Library/Application Support/parlay/bin/parlay-server-lib.sh"
else
  echo "ensure-up: cannot find lib.sh" >&2
  exit 1
fi

QUIET=0
[ "${1:-}" = "--quiet" ] && QUIET=1
log() { [ "${QUIET}" = 1 ] || echo "parlay-server ensure-up: $*" >&2; }

ADDR="${PARLAY_SERVER_ADDR:-${PARLAY_GOSERVER_ADDR_DEFAULT}}"
STATE_DIR="${PARLAY_STATE_HOME:-${PARLAY_GOSERVER_STATE_DEFAULT}}"

if parlay_goserver_refuse_31337 "${ADDR}"; then
  log "refusing addr ${ADDR} — :31337 is the captain's live production Pulse server"
  exit 1
fi

# Fast path: already up, do nothing.
if parlay_goserver_health_ok "${ADDR}"; then
  log "server already up"
  exit 0
fi

RUNTIME="${TMPDIR:-/tmp}/parlay-go-server"
mkdir -p "${RUNTIME}"

# ── Lock so concurrent callers do not both start a server ──────────────────────
# mkdir is atomic across POSIX filesystems; the lock dir is self-cleaning on exit.
LOCK="${RUNTIME}/.ensure-up.lock"
have_lock=0
for _ in $(seq 1 40); do          # up to ~10s waiting for a peer's start to finish
  if mkdir "${LOCK}" 2>/dev/null; then
    have_lock=1
    break
  fi
  # A peer holds the lock — it may already be starting the server. Re-check
  # health each spin so we return as soon as the peer's server is up.
  if parlay_goserver_health_ok "${ADDR}"; then
    log "server came up (started by a concurrent caller)"
    exit 0
  fi
  sleep 0.25
done
if [ "${have_lock}" != 1 ]; then
  # Could not get the lock and the server still is not up — last-ditch health read.
  if parlay_goserver_health_ok "${ADDR}"; then exit 0; fi
  log "could not acquire start lock and server is not up"
  exit 1
fi
# Release the lock on any exit path.
trap 'rmdir "${LOCK}" 2>/dev/null || true' EXIT

# Re-check under the lock: a peer may have started it between our first check
# and acquiring the lock.
if parlay_goserver_health_ok "${ADDR}"; then
  log "server already up (won the race, nothing to do)"
  exit 0
fi

DOMAIN="$(parlay_goserver_domain)"
TARGET="${DOMAIN}/${PARLAY_GOSERVER_LABEL}"
started=""

# ── Method 1: supervised launchd agent, if installed ───────────────────────────
if [ -e "${PARLAY_GOSERVER_PLIST}" ] && launchctl print "${TARGET}" >/dev/null 2>&1; then
  log "starting via launchd (${TARGET})"
  launchctl enable "${TARGET}" 2>/dev/null || true
  launchctl kickstart -k "${TARGET}" 2>/dev/null || true
  started="launchd"
elif [ -e "${PARLAY_GOSERVER_PLIST}" ]; then
  # Plist on disk but not bootstrapped (e.g. after a logout that unloaded it).
  log "bootstrapping launchd agent from ${PARLAY_GOSERVER_PLIST}"
  launchctl bootstrap "${DOMAIN}" "${PARLAY_GOSERVER_PLIST}" 2>/dev/null || true
  launchctl enable "${TARGET}" 2>/dev/null || true
  launchctl kickstart -k "${TARGET}" 2>/dev/null || true
  started="launchd"
fi

# ── Method 2: installed binary directly (unsupervised) ─────────────────────────
if [ -z "${started}" ] && [ -x "${PARLAY_GOSERVER_BIN}" ]; then
  log "no launchd agent — starting installed binary (unsupervised)"
  mkdir -p "${PARLAY_GOSERVER_LOG_DIR}"
  nohup "${PARLAY_GOSERVER_BIN}" -addr "${ADDR}" -state-dir "${STATE_DIR}" \
    >>"${PARLAY_GOSERVER_OUT_LOG}" 2>>"${PARLAY_GOSERVER_ERR_LOG}" &
  disown 2>/dev/null || true
  started="binary"
fi

# ── Method 3: repo binary (dev fallback, built if missing) ─────────────────────
if [ -z "${started}" ]; then
  MODULE_DIR="$(cd "${HERE}/.." && pwd)"
  REPO_BIN="${MODULE_DIR}/parlay-server"
  if [ ! -x "${REPO_BIN}" ]; then
    log "no repo binary — building ${REPO_BIN}"
    ( cd "${MODULE_DIR}" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o parlay-server ./cmd/parlay-server ) \
      2>>"${PARLAY_GOSERVER_ERR_LOG}" || true
  fi
  if [ -x "${REPO_BIN}" ]; then
    log "no install — starting repo binary (dev fallback)"
    mkdir -p "${PARLAY_GOSERVER_LOG_DIR}"
    nohup "${REPO_BIN}" -addr "${ADDR}" -state-dir "${STATE_DIR}" \
      >>"${PARLAY_GOSERVER_OUT_LOG}" 2>>"${PARLAY_GOSERVER_ERR_LOG}" &
    disown 2>/dev/null || true
    started="repo"
  fi
fi

if [ -z "${started}" ]; then
  log "no parlay-server binary found (install it: packages/go-server/deploy/install.sh)"
  exit 1
fi

# ── Wait (bounded) for /health ─────────────────────────────────────────────────
for _ in $(seq 1 40); do          # ~10s
  if parlay_goserver_health_ok "${ADDR}"; then
    log "server is up (started via ${started})"
    exit 0
  fi
  sleep 0.25
done
log "server did not answer /health within 10s (started via ${started}); check ${PARLAY_GOSERVER_ERR_LOG}"
exit 1
