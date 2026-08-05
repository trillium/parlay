# shellcheck shell=bash
# Shared constants + helpers for the parlay-relay launchd deployment.
#
# Sourced by install.sh, uninstall.sh, ensure-up.sh, and the launcher. One place
# defines the label, install paths, runtime dir, and log paths so every script
# agrees. Nothing here has side effects beyond defining variables and functions;
# `set -euo pipefail` is left to the sourcing script.

# ── launchd identity ──────────────────────────────────────────────────────────
# Reverse-DNS label; also the plist basename (com.parlay.relay.plist).
PARLAY_RELAY_LABEL="com.parlay.relay"

# ── Stable install paths (outside the repo, survive a repo move) ───────────────
# The binary and launcher are copied here by install.sh so the LaunchAgent never
# depends on the git checkout location. ~/Library is per-user and persistent.
PARLAY_RELAY_SUPPORT_DIR="${HOME}/Library/Application Support/parlay"
PARLAY_RELAY_BIN_DIR="${PARLAY_RELAY_SUPPORT_DIR}/bin"
PARLAY_RELAY_BIN="${PARLAY_RELAY_BIN_DIR}/parlay-relay"
PARLAY_RELAY_LAUNCHER="${PARLAY_RELAY_BIN_DIR}/parlay-relay-launch.sh"

# Installed LaunchAgent plist (machine config, outside the repo by design).
PARLAY_RELAY_PLIST="${HOME}/Library/LaunchAgents/${PARLAY_RELAY_LABEL}.plist"

# ── Logs: stable + non-volatile (unlike the runtime dir) ───────────────────────
# The runtime dir lives under $TMPDIR and macOS may wipe it; logs must survive so
# a crash trail is readable after the fact.
PARLAY_RELAY_LOG_DIR="${HOME}/Library/Logs/parlay"
PARLAY_RELAY_OUT_LOG="${PARLAY_RELAY_LOG_DIR}/relay.out.log"
PARLAY_RELAY_ERR_LOG="${PARLAY_RELAY_LOG_DIR}/relay.err.log"

# ── Upstream Pulse server (relay's own default is the same) ────────────────────
PARLAY_RELAY_SERVER_DEFAULT="http://localhost:31337"

# parlay_relay_runtime_dir prints the canonical per-user runtime dir the relay
# and every `parlay monitor` agree on. It MUST match relay/main.go's
# defaultRuntimeDir ($TMPDIR/parlay) and monitor/parlay-monitor.sh's RUNTIME.
#
# Resolution order (mirrors what the monitor script does so the two never point
# at different sockets):
#   1. $PARLAY_RELAY_RUNTIME  — explicit override, if set (monitor honors it too).
#   2. $TMPDIR/parlay via `getconf DARWIN_USER_TEMP_DIR` — the reliable per-user
#      temp dir, identical between an interactive shell and a launchd job (a
#      launchd job's inherited TMPDIR can be absent, so getconf is authoritative).
#   3. $TMPDIR/parlay, then /tmp/parlay — final fallbacks.
parlay_relay_runtime_dir() {
  if [ -n "${PARLAY_RELAY_RUNTIME:-}" ]; then
    printf '%s\n' "${PARLAY_RELAY_RUNTIME%/}"
    return 0
  fi
  local base
  base="$(getconf DARWIN_USER_TEMP_DIR 2>/dev/null || true)"
  [ -n "${base}" ] || base="${TMPDIR:-/tmp}"
  base="${base%/}"
  printf '%s/parlay\n' "${base}"
}

# parlay_relay_sock also honors an explicit $PARLAY_RELAY_SOCK override, matching
# the monitor script's precedence exactly.
_parlay_relay_sock_override() { printf '%s' "${PARLAY_RELAY_SOCK:-}"; }

# parlay_relay_sock prints the control-socket path. Honors an explicit
# $PARLAY_RELAY_SOCK override (same precedence as parlay-monitor.sh), else it is
# relay.sock inside the resolved runtime dir.
parlay_relay_sock() {
  local override
  override="$(_parlay_relay_sock_override)"
  if [ -n "${override}" ]; then
    printf '%s\n' "${override}"
    return 0
  fi
  printf '%s/relay.sock\n' "$(parlay_relay_runtime_dir)"
}

# parlay_relay_domain prints the launchd user-domain target (gui/<uid>) used by
# bootstrap/bootout/enable/print. GUI domain is required for a LaunchAgent that
# must run in the logged-in user's Aqua session (same TMPDIR as the shell).
parlay_relay_domain() {
  printf 'gui/%s\n' "$(id -u)"
}

# parlay_relay_health_ok returns 0 if a relay is answering /health on its socket.
# Bounded so it can never hang a caller. Requires curl (present on macOS).
parlay_relay_health_ok() {
  local sock
  sock="$(parlay_relay_sock)"
  [ -S "${sock}" ] || return 1
  curl -fsS --max-time 2 --unix-socket "${sock}" http://relay/health 2>/dev/null \
    | grep -q '"ok":true'
}

# ── Waiting for a relay to become healthy ─────────────────────────────────────
# Base budget in seconds for a relay to answer /health after a start, and the
# hard ceiling the adaptive extension can never exceed. Both overridable so a
# test (or a very large fleet) can tune them without editing this file.
PARLAY_RELAY_HEALTH_WAIT="${PARLAY_RELAY_HEALTH_WAIT:-45}"
PARLAY_RELAY_HEALTH_MAX_WAIT="${PARLAY_RELAY_HEALTH_MAX_WAIT:-300}"

# parlay_relay_launchd_pid prints the pid of the running LaunchAgent job, or
# nothing if the job is loaded-but-not-running, unknown, or launchctl is absent.
# A pid here means "a relay process exists" — which is NOT the same as "a relay
# is answering /health", because the relay may still be starting up.
# Arg 1: the launchd target (default gui/<uid>/<label>).
parlay_relay_launchd_pid() {
  local target="${1:-$(parlay_relay_domain)/${PARLAY_RELAY_LABEL}}"
  # `launchctl print` emits one top-level `pid = N` for a running job (nested
  # blocks have no pid), so the first match is the job's own pid.
  launchctl print "${target}" 2>/dev/null \
    | awk '/^[[:space:]]*pid = [0-9]+$/ { gsub(/[^0-9]/, "", $0); print; exit }'
}

# _parlay_relay_progress_mark prints a cheap liveness fingerprint: the byte size
# of the relay's stderr log. A relay that is still doing startup work keeps
# logging (spool resume, poll errors), so a changing mark means "still working".
# Prints nothing when the log is missing/unreadable — the caller treats "no
# mark" as "no evidence of progress". Guarded with -r because a `<` redirect on
# a missing file is a shell error that `2>/dev/null` on wc would not suppress.
_parlay_relay_progress_mark() {
  [ -r "${PARLAY_RELAY_ERR_LOG}" ] || return 0
  wc -c < "${PARLAY_RELAY_ERR_LOG}" 2>/dev/null | tr -d '[:space:]'
}

# parlay_relay_wait_health polls /health until it passes or the budget runs out.
# Returns 0 as soon as the relay is healthy, 1 on timeout.
#
# The deadline is ADAPTIVE (robots-mpr3): a relay replaying a large spool can
# take far longer than any fixed bound is comfortable with, so whenever the base
# budget expires the relay's log is checked for growth. If it grew, the relay is
# demonstrably alive and still working, and the budget is granted again — up to
# the hard ceiling. A wedged relay that has gone quiet still fails on the base
# budget, so this can never wait forever.
#
# Arg 1: base budget seconds  (default $PARLAY_RELAY_HEALTH_WAIT)
# Arg 2: hard ceiling seconds (default $PARLAY_RELAY_HEALTH_MAX_WAIT)
parlay_relay_wait_health() {
  local budget="${1:-${PARLAY_RELAY_HEALTH_WAIT}}"
  local ceiling="${2:-${PARLAY_RELAY_HEALTH_MAX_WAIT}}"
  local start now deadline hard mark last
  start="$(date +%s)"
  deadline=$(( start + budget ))
  hard=$(( start + ceiling ))
  last="$(_parlay_relay_progress_mark)"
  while :; do
    if parlay_relay_health_ok; then
      return 0
    fi
    now="$(date +%s)"
    if [ "${now}" -ge "${hard}" ]; then
      return 1
    fi
    if [ "${now}" -ge "${deadline}" ]; then
      mark="$(_parlay_relay_progress_mark)"
      if [ -n "${mark}" ] && [ "${mark}" != "${last}" ]; then
        last="${mark}"
        deadline=$(( now + budget ))   # still logging — grant another budget
      else
        return 1                        # gone quiet and still not healthy
      fi
    fi
    sleep 0.25
  done
}
