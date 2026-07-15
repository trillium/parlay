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
