# shellcheck shell=bash
# Shared constants + helpers for the parlay-server (Go) launchd deployment.
#
# Sourced by install.sh, uninstall.sh, and ensure-up.sh. One place defines the
# label, install paths, and log paths so every script agrees. Nothing here has
# side effects beyond defining variables and functions; `set -euo pipefail` is
# left to the sourcing script.
#
# Unlike tools/relay/deploy/lib.sh, there is no unix-socket/runtime-dir
# resolution here: parlay-server (packages/go-server/cmd/parlay-server) is a
# plain TCP HTTP server taking -addr/-state-dir flags (env PARLAY_SERVER_ADDR
# / PARLAY_STATE_HOME), so health is a normal http:// GET, not a socket dial.

# ── launchd identity ──────────────────────────────────────────────────────────
# Reverse-DNS label; also the plist basename (com.parlay.go-server.plist).
# Deliberately distinct from com.parlay.relay and com.parlay.eval-engine.
PARLAY_GOSERVER_LABEL="com.parlay.go-server"

# ── Stable install paths (outside the repo, survive a repo move) ───────────────
# Shares the relay's ".../parlay/bin" directory (distinct filenames avoid any
# collision), but uninstall.sh below only ever removes this script's own
# files — never `rm -rf` of the shared "bin" or "parlay" parent — since that
# directory can hold other services' installed binaries too.
PARLAY_GOSERVER_SUPPORT_DIR="${HOME}/Library/Application Support/parlay"
PARLAY_GOSERVER_BIN_DIR="${PARLAY_GOSERVER_SUPPORT_DIR}/bin"
PARLAY_GOSERVER_BIN="${PARLAY_GOSERVER_BIN_DIR}/parlay-server"
PARLAY_GOSERVER_LIB="${PARLAY_GOSERVER_BIN_DIR}/parlay-server-lib.sh"
# Stable asset directory: built client bundle copied here at install time so
# dev builds in the repo's packages/client/dist never disturb production.
PARLAY_GOSERVER_ASSETS_DIR="${PARLAY_GOSERVER_SUPPORT_DIR}/dist"

# Installed LaunchAgent plist (machine config, outside the repo by design).
PARLAY_GOSERVER_PLIST="${HOME}/Library/LaunchAgents/${PARLAY_GOSERVER_LABEL}.plist"

# ── Logs: stable + non-volatile ─────────────────────────────────────────────────
PARLAY_GOSERVER_LOG_DIR="${HOME}/Library/Logs/parlay"
PARLAY_GOSERVER_OUT_LOG="${PARLAY_GOSERVER_LOG_DIR}/go-server.out.log"
PARLAY_GOSERVER_ERR_LOG="${PARLAY_GOSERVER_LOG_DIR}/go-server.err.log"

# ── Defaults (must match cmd/parlay-server/main.go's own coded defaults) ───────
# main.go's defaultAddr — deliberately NOT :31337, the captain's live
# production Pulse instance (see this repo's CLAUDE.md and main.go's own
# refuseProductionPort, which enforces this at bind time too).
PARLAY_GOSERVER_ADDR_DEFAULT="127.0.0.1:4242"
# main.go's defaultStateHome().
PARLAY_GOSERVER_STATE_DEFAULT="${HOME}/.parlay"
# Unset/empty is guard.AllowedOriginList()'s own "no extra origins" default.
PARLAY_GOSERVER_ALLOWED_ORIGINS_DEFAULT=""

# parlay_goserver_refuse_31337 exits non-zero if addr targets port 31337, in
# any host:port/[::1]:port/bare-port form. Belt-and-suspenders: the binary
# itself refuses to bind that port (main.go's refuseProductionPort), but
# failing fast here avoids even attempting the deploy.
parlay_goserver_refuse_31337() {
  case ":$1" in
    *:31337) return 0 ;;
    *) return 1 ;;
  esac
}

# parlay_goserver_domain prints the launchd user-domain target (gui/<uid>)
# used by bootstrap/bootout/enable/print.
parlay_goserver_domain() {
  printf 'gui/%s\n' "$(id -u)"
}

# parlay_goserver_health_ok returns 0 if parlay-server is answering GET
# /health at the given host:port addr. Bounded so it can never hang a caller.
parlay_goserver_health_ok() {
  local addr="$1"
  curl -fsS --max-time 2 "http://${addr}/health" 2>/dev/null | grep -q '"ok":true'
}

# parlay_goserver_installed_state_dir prints the state dir actually baked
# into the installed plist's -state-dir argument — i.e. what install.sh's
# --state-dir/PARLAY_STATE_HOME actually resolved to at install time — by
# reading it back out of ${PARLAY_GOSERVER_PLIST}'s ProgramArguments. This is
# the persistence mechanism: install.sh writes the resolved value into the
# plist it renders, and this is the only durable record of it afterwards.
#
# Falls back to PARLAY_STATE_HOME/the coded default only if the plist is
# missing or unparseable (e.g. it was hand-removed outside these scripts).
# Callers that act on the result (uninstall.sh --purge) must call this BEFORE
# removing the plist.
parlay_goserver_installed_state_dir() {
  if [ -r "${PARLAY_GOSERVER_PLIST}" ] && [ -x /usr/libexec/PlistBuddy ]; then
    local args found=0 line
    args="$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments' "${PARLAY_GOSERVER_PLIST}" 2>/dev/null || true)"
    if [ -n "${args}" ]; then
      while IFS= read -r line; do
        line="${line#"${line%%[![:space:]]*}"}"   # trim leading whitespace
        line="${line%"${line##*[![:space:]]}"}"   # trim trailing whitespace
        if [ "${found}" = 1 ]; then
          printf '%s\n' "${line}"
          return 0
        fi
        [ "${line}" = "-state-dir" ] && found=1
      done <<EOF
${args}
EOF
    fi
  fi
  printf '%s\n' "${PARLAY_STATE_HOME:-${PARLAY_GOSERVER_STATE_DEFAULT}}"
}

# parlay_goserver_installed_allowed_origins prints the PARLAY_ALLOWED_ORIGINS
# value actually baked into the installed plist's EnvironmentVariables dict —
# i.e. what a prior install.sh --allowed-origins really used — by reading it
# back out of ${PARLAY_GOSERVER_PLIST}. This is the persistence mechanism: a
# re-install run WITHOUT --allowed-origins calls this to carry the existing
# value forward instead of silently wiping it (mirrors
# parlay_goserver_installed_state_dir above).
#
# Falls back to the coded default (empty — no extra origins) if the plist is
# missing, has no EnvironmentVariables entry yet (a plist from before this
# key existed), or is unparseable.
parlay_goserver_installed_allowed_origins() {
  if [ -r "${PARLAY_GOSERVER_PLIST}" ] && [ -x /usr/libexec/PlistBuddy ]; then
    local value
    value="$(/usr/libexec/PlistBuddy -c 'Print :EnvironmentVariables:PARLAY_ALLOWED_ORIGINS' "${PARLAY_GOSERVER_PLIST}" 2>/dev/null || true)"
    if [ -n "${value}" ]; then
      printf '%s\n' "${value}"
      return 0
    fi
  fi
  printf '%s\n' "${PARLAY_GOSERVER_ALLOWED_ORIGINS_DEFAULT}"
}

# parlay_goserver_trash_put moves PATH to a recoverable trash location instead
# of permanently deleting it (never `rm -rf`/`rm -f`). Prefers a real `trash`
# CLI — checked in PATH, then Homebrew's keg-only install locations (the
# `trash` formula, hasseg.org/trash, is keg-only on macOS because the OS ships
# its own `/usr/bin/trash` since Sequoia — both share the same CLI shape), then
# the macOS built-in itself. Falls back to a manual move into ~/.Trash (the
# same real Finder Trash those tools use) with a timestamp suffix to dodge
# name collisions, for hosts with none of the above. No-op if PATH does not
# exist.
parlay_goserver_trash_put() {
  local path="$1"
  [ -e "${path}" ] || [ -L "${path}" ] || return 0

  local candidate
  for candidate in \
    "$(command -v trash 2>/dev/null || true)" \
    "/opt/homebrew/opt/trash/bin/trash" \
    "/usr/local/opt/trash/bin/trash" \
    "/usr/bin/trash"
  do
    if [ -n "${candidate}" ] && [ -x "${candidate}" ]; then
      "${candidate}" "${path}"
      return $?
    fi
  done

  local trash_dir="${HOME}/.Trash"
  mkdir -p "${trash_dir}" 2>/dev/null || true
  local base dest
  base="$(basename "${path}")"
  dest="${trash_dir}/${base}"
  if [ -e "${dest}" ] || [ -L "${dest}" ]; then
    dest="${trash_dir}/${base}.$(date +%Y%m%dT%H%M%S)-$$"
  fi
  mv -f "${path}" "${dest}"
}
