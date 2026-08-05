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

# ── Upstream-server scoping (robots-buu8) ─────────────────────────────────────
# A relay process is a per-runtime-dir singleton bound to exactly ONE upstream
# Pulse server, chosen when it starts. Enrolling on a relay therefore registers
# the agent against THAT relay's server — not against whatever $PARLAY_SERVER the
# enrolling process happens to have set. Before this, a sandbox or test that ran
# `PARLAY_SERVER=http://127.0.0.1:<scratch> parlay listen` enrolled through the
# shared $TMPDIR/parlay relay (bound to production :31337) and its agent appeared
# in the captain's LIVE registry — silent pollution with no error anywhere.
#
# The rule: the canonical runtime dir is RESERVED for the default server. Any
# other target server gets its own runtime dir, and therefore its own relay.

# parlay_relay_target_server prints the upstream server this process wants,
# normalized without a trailing slash. Mirrors relay/main.go's -server default.
parlay_relay_target_server() {
  local s="${PARLAY_SERVER:-${PARLAY_RELAY_SERVER_DEFAULT}}"
  printf '%s\n' "${s%/}"
}

# parlay_relay_server_slug prints a short, filesystem-safe directory name for a
# server URL: `srv-` plus 10 hex of its SHA-256.
#
# Hash-only, NOT a readable "http-127-0-0-1-45999-…" prefix, because a Unix
# domain socket path is capped at 104 bytes (sun_path) on macOS. The canonical
# runtime dir is already ~53 bytes ($TMPDIR is /var/folders/xx/<28 chars>/T), and
# "/relay.sock" is 11 more — a readable slug blew straight past the cap and the
# relay died with the deeply unhelpful "bind: invalid argument". 14 bytes leaves
# ample headroom. The server URL is recoverable anyway: the relay reports it on
# GET /agents, and parlay-monitor.sh drops a `server` marker file in the dir.
parlay_relay_server_slug() {
  local url="$1" hash
  hash="$(printf '%s' "${url}" | { shasum -a 256 2>/dev/null || sha256sum; } | cut -c1-10)"
  printf 'srv-%s\n' "${hash}"
}

# parlay_relay_scoped_runtime_dir prints the runtime dir to use for the target
# server:
#   1. explicit $PARLAY_RELAY_RUNTIME always wins — the caller pinned a dir.
#   2. default server            → the canonical dir (shared production relay).
#   3. any other PARLAY_SERVER   → <canonical>/srv-<hash>, its own relay.
# Nesting under the canonical dir is deliberate and safe: the production relay's
# spool-resume scan (main.go) skips subdirectories, and a scoped dir has no
# `.chan` suffix, so it can never be mistaken for one of its spools.
parlay_relay_scoped_runtime_dir() {
  if [ -n "${PARLAY_RELAY_RUNTIME:-}" ]; then
    printf '%s\n' "${PARLAY_RELAY_RUNTIME%/}"
    return 0
  fi
  local server base
  server="$(parlay_relay_target_server)"
  base="$(parlay_relay_runtime_dir)"
  if [ "${server}" = "${PARLAY_RELAY_SERVER_DEFAULT%/}" ]; then
    printf '%s\n' "${base}"
    return 0
  fi
  printf '%s/%s\n' "${base}" "$(parlay_relay_server_slug "${server}")"
}

# parlay_relay_sock_path_ok returns 0 if a control-socket path fits in sun_path.
# The kernel cap is 104 bytes on macOS (108 on Linux) INCLUDING the terminating
# NUL, so 103 usable; 104 is the portable-safe check. Over the cap, bind() fails
# with EINVAL — "invalid argument", which says nothing about length — so callers
# check first and say what actually went wrong.
parlay_relay_sock_path_ok() {
  [ "${#1}" -le 103 ]
}

# parlay_relay_reported_server prints the upstream server a live relay reports on
# its control socket (GET /agents → {"agents":…,"server":…,"runtime":…}). Prints
# nothing (and still returns 0) when no relay answers — callers distinguish
# "unknown" from "mismatched" by testing for an empty string.
#
# The timeout is $PARLAY_RELAY_PROBE_TIMEOUT (default 15s), NOT the 2s used for
# /health: /health is answered from a socket bound before any real work, but
# /agents serializes the whole registry and grows with the fleet — on a
# 269-agent box it routinely takes >2s, so a 2s cap timed out against a
# perfectly healthy relay (robots-dcag). This helper always returns 0; every
# caller must treat an empty result as "unknown", never as a failure to abort
# on. See parlay-monitor.sh's probe for why that distinction is load-bearing
# under `set -e`.
parlay_relay_reported_server() {
  local sock="${1:-}" body
  [ -n "${sock}" ] && [ -S "${sock}" ] || return 0
  body="$(curl -fsS --max-time "${PARLAY_RELAY_PROBE_TIMEOUT:-15}" \
    --unix-socket "${sock}" http://relay/agents 2>/dev/null)" || return 0
  printf '%s' "${body}" | sed -n 's/.*"server":"\([^"]*\)".*/\1/p'
}

# parlay_relay_installed_plist_server prints the PARLAY_SERVER baked into the
# installed LaunchAgent plist — the server the launchd-supervised relay is
# actually bound to, which is the only durable record of what `install.sh
# --server` resolved to. Empty if there is no plist or no such key.
parlay_relay_installed_plist_server() {
  [ -e "${PARLAY_RELAY_PLIST}" ] || return 0
  /usr/libexec/PlistBuddy -c "Print :EnvironmentVariables:PARLAY_SERVER" \
    "${PARLAY_RELAY_PLIST}" 2>/dev/null || true
}
