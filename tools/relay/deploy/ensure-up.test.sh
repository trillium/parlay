#!/usr/bin/env bash
# Behavior tests for ensure-up.sh's start/wait policy (robots-mpr3).
#
# The defect, in two halves:
#   1. ensure-up ran `launchctl kickstart -k` unconditionally. `-k` KILLS a
#      running job — so a relay that was alive but mid-startup (the relay binds
#      its control socket only after replaying every spooled agent) got killed
#      and had to start its replay over from zero.
#   2. It then waited only 40 x 0.25s = 10s for /health. On a real fleet the
#      replay alone took ~7s, so ensure-up regularly declared a perfectly
#      healthy relay dead — silently breaking agent enrollment.
#
# Hermetic harness: $HOME, $PATH, the runtime dir and the relay's log are all
# redirected into a temp root, and `launchctl`/`curl` are stubs. Nothing touches
# the real LaunchAgent, the real relay, or the real socket. Every launchctl
# invocation is appended to $ROOT/launchctl.log so the tests can assert on
# exactly which subcommands ran.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENSURE_UP="$SELF_DIR/ensure-up.sh"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/pu.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT

RUNTIME="$ROOT/rt"
SOCK="$RUNTIME/relay.sock"
STUB="$ROOT/stubs"
FAKE_HOME="$ROOT/home"
ERR_LOG="$FAKE_HOME/Library/Logs/parlay/relay.err.log"
PLIST="$FAKE_HOME/Library/LaunchAgents/com.parlay.relay.plist"
mkdir -p "$RUNTIME" "$STUB" "$(dirname "$ERR_LOG")" "$(dirname "$PLIST")"

# A real socket FILE must exist: parlay_relay_health_ok short-circuits on `[ -S ]`
# before it ever runs curl. Binding and dropping the socket leaves the file on
# disk, exactly like a relay that exited uncleanly — so `-S` passes and the stub
# curl decides health.
python3 -c "import socket,sys; s=socket.socket(socket.AF_UNIX); s.bind(sys.argv[1])" "$SOCK" \
  || { echo "cannot create test socket at $SOCK" >&2; exit 1; }

# ── Stubs ─────────────────────────────────────────────────────────────────────
# curl: answers /health from $ROOT/health-at — the epoch second at which the
# relay becomes healthy. "never" means it never does.
cat > "$STUB/curl" <<'S'
#!/usr/bin/env bash
at="$(cat "${ROOT}/health-at" 2>/dev/null || echo never)"
[ "$at" = "never" ] && exit 22
[ "$(date +%s)" -ge "$at" ] || exit 22
echo '{"ok":true}'
S

# launchctl: records every invocation, and answers `print` from $ROOT/job-state
# ("running" -> a pid line, "loaded" -> no pid, "absent" -> exit 1).
cat > "$STUB/launchctl" <<'S'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${ROOT}/launchctl.log"
case "${1:-}" in
  print)
    state="$(cat "${ROOT}/job-state" 2>/dev/null || echo absent)"
    [ "$state" = absent ] && exit 1
    echo "${2} = {"
    echo "	state = running"
    [ "$state" = running ] && echo "	pid = 4242"
    echo "	endpoints = {"
    echo "		state = active"     # nested block: must NOT be read as a pid
    echo "	}"
    echo "}"
    ;;
esac
exit 0
S
chmod +x "$STUB/curl" "$STUB/launchctl"

# ── Harness ───────────────────────────────────────────────────────────────────
# run <job-state> <health-at-offset-seconds|never> [extra ensure-up args...]
# Resets recorded state, then runs ensure-up.sh under the redirected env.
# Sets RC and captures stderr in $ROOT/out.
run() {
  local job="$1" offset="$2"; shift 2
  : > "$ROOT/launchctl.log"
  printf '%s\n' "$job" > "$ROOT/job-state"
  if [ "$offset" = never ]; then
    printf 'never\n' > "$ROOT/health-at"
  else
    printf '%s\n' "$(( $(date +%s) + offset ))" > "$ROOT/health-at"
  fi
  RC=0
  env -i \
    PATH="$STUB:/usr/bin:/bin:/usr/sbin:/sbin" \
    HOME="$FAKE_HOME" \
    ROOT="$ROOT" \
    PARLAY_RELAY_RUNTIME="$RUNTIME" \
    PARLAY_RELAY_HEALTH_WAIT="${WAIT:-1}" \
    PARLAY_RELAY_HEALTH_MAX_WAIT="${MAXWAIT:-20}" \
    /bin/bash "$ENSURE_UP" "$@" >"$ROOT/out" 2>&1 || RC=$?
}

lc_log() { cat "$ROOT/launchctl.log"; }

# ── 1. Fast path: a healthy relay is left completely alone ────────────────────
touch "$PLIST"
run running 0
if [ "$RC" -ne 0 ]; then
  fail "healthy relay: exit $RC, want 0 ($(cat "$ROOT/out"))"
elif [ -s "$ROOT/launchctl.log" ]; then
  fail "healthy relay: launchctl was invoked at all — $(lc_log)"
else
  pass "healthy relay: exits 0 without touching launchctl"
fi

# ── 2. THE defect: a RUNNING relay that is not yet healthy is never restarted ──
# Pre-fix this ran `kickstart -k`, killing a relay that was mid-spool-replay.
# Budget generously here: this case is about the RESTART policy, not the wait.
WAIT=10 run running 3
if [ "$RC" -ne 0 ]; then
  fail "running-but-starting relay: exit $RC, want 0 ($(cat "$ROOT/out"))"
elif lc_log | grep -q kickstart; then
  fail "running-but-starting relay: it was RESTARTED — $(lc_log)"
elif ! grep -q "already running (pid 4242)" "$ROOT/out"; then
  fail "running-but-starting relay: no wait-it-out log line ($(cat "$ROOT/out"))"
else
  pass "running relay is waited out, never kickstarted"
fi

# ── 3. A loaded-but-not-running job IS started — with a plain kickstart ───────
WAIT=10 run loaded 2
if [ "$RC" -ne 0 ]; then
  fail "stopped relay: exit $RC, want 0 ($(cat "$ROOT/out"))"
elif ! lc_log | grep -q '^kickstart gui/'; then
  fail "stopped relay: never kickstarted — $(lc_log)"
elif lc_log | grep -q 'kickstart -k'; then
  fail "stopped relay: used -k (force-kill) on the normal path — $(lc_log)"
else
  pass "stopped relay is started with a plain (non -k) kickstart"
fi

# ── 4. The wait outlives its base budget while the relay is still working ─────
# Health arrives at +5s with a 1s base budget. A fixed bound gives up; the
# adaptive wait keeps going because the relay's log is still growing.
( for _ in 1 2 3 4 5 6 7 8 9 10; do echo "relay: resumed agent from spool" >> "$ERR_LOG"; sleep 0.5; done ) &
LOGGER=$!
WAIT=1 run running 5
kill "$LOGGER" 2>/dev/null; wait "$LOGGER" 2>/dev/null
if [ "$RC" -ne 0 ]; then
  fail "slow startup: exit $RC, want 0 — the wait did not adapt ($(cat "$ROOT/out"))"
else
  pass "slow startup (5s) survives a 1s base budget while the log advances"
fi

# ── 5. A wedged, silent relay still fails — the wait can never hang forever ───
: > "$ERR_LOG"
START="$(date +%s)"
WAIT=1 run running never
ELAPSED=$(( $(date +%s) - START ))
if [ "$RC" -eq 0 ]; then
  fail "wedged relay: exit 0, want nonzero"
elif [ "$ELAPSED" -gt 10 ]; then
  fail "wedged relay: took ${ELAPSED}s to give up on a quiet 1s budget"
elif ! grep -q -- "--force-restart" "$ROOT/out"; then
  fail "wedged relay: failure message does not point at the escape hatch"
else
  pass "wedged relay fails fast (${ELAPSED}s) and names --force-restart"
fi

# ── 6. --force-restart is the one path that may -k a live relay ───────────────
run running 0 --force-restart
if [ "$RC" -ne 0 ]; then
  fail "--force-restart: exit $RC, want 0 ($(cat "$ROOT/out"))"
elif ! lc_log | grep -q 'kickstart -k'; then
  fail "--force-restart: did not force-restart — $(lc_log)"
else
  pass "--force-restart force-restarts even a healthy relay"
fi

# ── 7. Unknown flags are rejected rather than silently ignored ────────────────
run running 0 --bogus
if [ "$RC" -ne 2 ]; then
  fail "unknown flag: exit $RC, want 2"
else
  pass "unknown flag exits 2"
fi

if [ "$FAILED" -ne 0 ]; then
  echo "ensure-up.test.sh: FAILED" >&2
  exit 1
fi
echo "ensure-up.test.sh: all tests passed"
