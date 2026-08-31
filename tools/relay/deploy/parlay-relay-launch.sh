#!/usr/bin/env bash
# Launcher execed by the com.parlay.relay LaunchAgent.
#
# Why a launcher and not the binary directly: a plist cannot expand $TMPDIR, and a
# launchd job's inherited TMPDIR is unreliable. This script resolves the canonical
# per-user runtime dir the same way an interactive shell does, then execs the
# relay with an explicit -runtime-dir so the launchd relay and shell-launched
# `parlay monitor` provably share one socket + spool directory.
#
# It is installed next to the binary at
#   ~/Library/Application Support/parlay/bin/parlay-relay-launch.sh
# so both live at a stable path independent of the git checkout.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"

# Prefer the installed lib.sh (copied beside this launcher by install.sh); fall
# back to computing the runtime dir inline so the launcher still works if run from
# the repo before install.
if [ -r "${HERE}/lib.sh" ]; then
  # shellcheck source=/dev/null
  . "${HERE}/lib.sh"
  RUNTIME="$(parlay_relay_runtime_dir)"
  BIN="${HERE}/parlay-relay"
  # Cap the logs before the relay starts appending to them (robots-dcgg: no
  # rotation existed and relay.err.log reached 277 MB). Best-effort.
  parlay_relay_rotate_logs || true
else
  BASE="$(getconf DARWIN_USER_TEMP_DIR 2>/dev/null || true)"
  [ -n "${BASE}" ] || BASE="${TMPDIR:-/tmp}"
  RUNTIME="${BASE%/}/parlay"
  BIN="${HERE}/parlay-relay"
fi

SERVER="${PARLAY_SERVER:-http://localhost:31337}"

mkdir -p "${RUNTIME}"

# exec so the relay is the direct child of launchd — KeepAlive tracks it, and
# SIGTERM on bootout reaches the relay for a clean shutdown.
exec "${BIN}" -server "${SERVER}" -runtime-dir "${RUNTIME}"
