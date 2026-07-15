#!/usr/bin/env bash
# ensure-up — guarantee a Parlay relay is answering /health before a monitor
# enrolls, so `parlay monitor --agent <id>` never dead-ends on a missing relay.
#
# Idempotent and concurrency-safe:
#   * If the relay already answers /health, returns 0 immediately (no start).
#   * Otherwise it acquires a per-user lock (so two monitors starting at once do
#     not both launch a relay), re-checks health under the lock, and starts the
#     relay by the best available method:
#       1. launchd  — if the LaunchAgent is installed, `launchctl kickstart` it
#          (this is the supervised path; KeepAlive keeps it up afterwards).
#       2. installed binary — ~/Library/Application Support/parlay/bin via its
#          launcher, backgrounded + disowned (unsupervised fallback).
#       3. repo binary — tools/relay/parlay-relay via the repo launcher
#          (dev fallback when nothing is installed).
#   * Then waits (bounded) for /health to come up and returns 0/1 accordingly.
#
# The relay's own control socket is single-binder (listenControl in main.go
# refuses a second live relay), so even a lost lock race cannot produce two live
# relays — the loser fails to bind and exits.
#
# Usage:  ensure-up.sh            # ensure a relay is up; exit 0 if up, 1 if not
#         ensure-up.sh --quiet    # suppress the informational stderr line
# Exit:   0 relay is up (already, or started); 1 could not bring it up.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# Prefer the installed lib (this script may be run from the install dir); fall
# back to the repo copy.
if [ -r "${HERE}/lib.sh" ]; then
  # shellcheck source=lib.sh
  . "${HERE}/lib.sh"
elif [ -r "${HOME}/Library/Application Support/parlay/bin/lib.sh" ]; then
  # shellcheck source=/dev/null
  . "${HOME}/Library/Application Support/parlay/bin/lib.sh"
else
  echo "ensure-up: cannot find lib.sh" >&2
  exit 1
fi

QUIET=0
[ "${1:-}" = "--quiet" ] && QUIET=1
log() { [ "${QUIET}" = 1 ] || echo "parlay ensure-up: $*" >&2; }

# Fast path: already up, do nothing.
if parlay_relay_health_ok; then
  log "relay already up"
  exit 0
fi

RUNTIME="$(parlay_relay_runtime_dir)"
mkdir -p "${RUNTIME}"

# ── Lock so concurrent monitors do not both start a relay ──────────────────────
# mkdir is atomic across POSIX filesystems; the lock dir is self-cleaning on exit.
LOCK="${RUNTIME}/.ensure-up.lock"
have_lock=0
for _ in $(seq 1 40); do          # up to ~10s waiting for a peer's start to finish
  if mkdir "${LOCK}" 2>/dev/null; then
    have_lock=1
    break
  fi
  # A peer holds the lock — it may already be starting the relay. Re-check health
  # each spin so we return as soon as the peer's relay is up.
  if parlay_relay_health_ok; then
    log "relay came up (started by a concurrent monitor)"
    exit 0
  fi
  sleep 0.25
done
if [ "${have_lock}" != 1 ]; then
  # Could not get the lock and the relay still is not up — last-ditch health read.
  if parlay_relay_health_ok; then exit 0; fi
  log "could not acquire start lock and relay is not up"
  exit 1
fi
# Release the lock on any exit path.
trap 'rmdir "${LOCK}" 2>/dev/null || true' EXIT

# Re-check under the lock: a peer may have started it between our first check and
# acquiring the lock.
if parlay_relay_health_ok; then
  log "relay already up (won the race, nothing to do)"
  exit 0
fi

DOMAIN="$(parlay_relay_domain)"
TARGET="${DOMAIN}/${PARLAY_RELAY_LABEL}"
started=""

# ── Method 1: supervised launchd agent, if installed ───────────────────────────
if [ -e "${PARLAY_RELAY_PLIST}" ] && launchctl print "${TARGET}" >/dev/null 2>&1; then
  log "starting via launchd (${TARGET})"
  launchctl enable "${TARGET}" 2>/dev/null || true
  launchctl kickstart -k "${TARGET}" 2>/dev/null || true
  started="launchd"
elif [ -e "${PARLAY_RELAY_PLIST}" ]; then
  # Plist on disk but not bootstrapped (e.g. after a logout that unloaded it).
  log "bootstrapping launchd agent from ${PARLAY_RELAY_PLIST}"
  launchctl bootstrap "${DOMAIN}" "${PARLAY_RELAY_PLIST}" 2>/dev/null || true
  launchctl enable "${TARGET}" 2>/dev/null || true
  launchctl kickstart -k "${TARGET}" 2>/dev/null || true
  started="launchd"
fi

# ── Method 2: installed binary directly (unsupervised) ─────────────────────────
if [ -z "${started}" ] && [ -x "${PARLAY_RELAY_LAUNCHER}" ] && [ -x "${PARLAY_RELAY_BIN}" ]; then
  log "no launchd agent — starting installed binary (unsupervised)"
  PARLAY_SERVER="${PARLAY_SERVER:-${PARLAY_RELAY_SERVER_DEFAULT}}" \
    nohup /bin/bash "${PARLAY_RELAY_LAUNCHER}" \
      >>"${PARLAY_RELAY_OUT_LOG}" 2>>"${PARLAY_RELAY_ERR_LOG}" &
  disown 2>/dev/null || true
  started="binary"
fi

# ── Method 3: repo binary (dev fallback) ───────────────────────────────────────
if [ -z "${started}" ]; then
  REPO_LAUNCHER="${HERE}/parlay-relay-launch.sh"
  REPO_BIN="${HERE}/../parlay-relay"
  if [ -x "${REPO_LAUNCHER}" ] && [ -x "${REPO_BIN}" ]; then
    log "no install — starting repo binary (dev fallback)"
    mkdir -p "${PARLAY_RELAY_LOG_DIR}"
    PARLAY_SERVER="${PARLAY_SERVER:-${PARLAY_RELAY_SERVER_DEFAULT}}" \
      nohup /bin/bash "${REPO_LAUNCHER}" \
        >>"${PARLAY_RELAY_OUT_LOG}" 2>>"${PARLAY_RELAY_ERR_LOG}" &
    disown 2>/dev/null || true
    started="repo"
  fi
fi

if [ -z "${started}" ]; then
  log "no relay binary found (install it: tools/relay/deploy/install.sh)"
  exit 1
fi

# ── Wait (bounded) for /health ─────────────────────────────────────────────────
for _ in $(seq 1 40); do          # ~10s
  if parlay_relay_health_ok; then
    log "relay is up (started via ${started})"
    exit 0
  fi
  sleep 0.25
done
log "relay did not answer /health within 10s (started via ${started}); check ${PARLAY_RELAY_ERR_LOG}"
exit 1
