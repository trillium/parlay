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

# The runtime dir must be the CANONICAL one as lib.sh resolves it — i.e.
# <getconf DARWIN_USER_TEMP_DIR>/parlay — not an arbitrary dir pinned via
# $PARLAY_RELAY_RUNTIME. ensure-up's launchd-matching logic (robots-buu8) refuses
# to use launchd for any *other* runtime dir, so pinning one made every launchd
# case below fall through to "no relay binary found" and fail. We stub `getconf`
# instead (see $STUB/getconf), which redirects the canonical dir into $ROOT while
# leaving the override unset — exactly the shape production runs in.
RUNTIME="$ROOT/parlay"
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
# getconf: makes lib.sh's canonical-runtime-dir resolution land inside $ROOT, so
# the tests exercise the real (unpinned) resolution path without touching the
# host's actual $TMPDIR/parlay. Any other query falls through to the real binary.
cat > "$STUB/getconf" <<'S'
#!/usr/bin/env bash
if [ "${1:-}" = DARWIN_USER_TEMP_DIR ]; then echo "${ROOT}"; exit 0; fi
exec /usr/bin/getconf "$@"
S
chmod +x "$STUB/curl" "$STUB/launchctl" "$STUB/getconf"

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
    PARLAY_RELAY_HEALTH_WAIT="${WAIT:-1}" \
    PARLAY_RELAY_HEALTH_MAX_WAIT="${MAXWAIT:-20}" \
    PARLAY_RELAY_LOG_CAP_BYTES="${LOGCAP:-67108864}" \
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

# ── 7. A healthy relay bound to the WRONG server is not "up" (robots-93xu) ────
# /health proves a relay answers; it says nothing about which upstream server it
# is bound to. Pre-fix the fast path returned 0 here — a false green that sent
# the caller on to die at its own enroll guard with a success line above it.
# The relay must NOT be restarted (it is a live singleton), and the exit code
# must be the distinct 3 so callers can tell this from "no relay".
cat > "$STUB/curl" <<'S'
#!/usr/bin/env bash
# /agents reports the server this relay is bound to; /health is always ok.
for a in "$@"; do
  case "$a" in
    */agents) echo '{"agents":[],"server":"http://localhost:4242","runtime":"x"}'; exit 0 ;;
  esac
done
echo '{"ok":true}'
S
chmod +x "$STUB/curl"
run running 0
if [ "$RC" -ne 3 ]; then
  fail "wrong-server relay: exit $RC, want 3 ($(cat "$ROOT/out"))"
elif [ -s "$ROOT/launchctl.log" ]; then
  fail "wrong-server relay: it was restarted/kickstarted — $(lc_log)"
elif ! grep -q "bound to http://localhost:4242" "$ROOT/out"; then
  fail "wrong-server relay: message does not name the relay's actual server ($(cat "$ROOT/out"))"
elif ! grep -q -- "install.sh --server http://localhost:31337" "$ROOT/out"; then
  fail "wrong-server relay: message does not give the repair command ($(cat "$ROOT/out"))"
else
  pass "healthy-but-wrong-server relay exits 3, is left running, and names the repair"
fi

# ── 8. A healthy relay bound to the RIGHT server is still the fast path ────────
# The guard must not become a blanket failure: same stub shape, matching server.
cat > "$STUB/curl" <<'S'
#!/usr/bin/env bash
for a in "$@"; do
  case "$a" in
    */agents) echo '{"agents":[],"server":"http://localhost:31337/","runtime":"x"}'; exit 0 ;;
  esac
done
echo '{"ok":true}'
S
chmod +x "$STUB/curl"
run running 0
if [ "$RC" -ne 0 ]; then
  fail "matching-server relay: exit $RC, want 0 — the guard is over-broad ($(cat "$ROOT/out"))"
elif [ -s "$ROOT/launchctl.log" ]; then
  fail "matching-server relay: launchctl was invoked — $(lc_log)"
else
  pass "matching server (trailing slash and all) still takes the untouched fast path"
fi

# Restore the plain health-only stub for the remaining cases.
cat > "$STUB/curl" <<'S'
#!/usr/bin/env bash
at="$(cat "${ROOT}/health-at" 2>/dev/null || echo never)"
[ "$at" = "never" ] && exit 22
[ "$(date +%s)" -ge "$at" ] || exit 22
echo '{"ok":true}'
S
chmod +x "$STUB/curl"

# ── 9. Unknown flags are rejected rather than silently ignored ────────────────
run running 0 --bogus
if [ "$RC" -ne 2 ]; then
  fail "unknown flag: exit $RC, want 2"
else
  pass "unknown flag exits 2"
fi

# ── 10. An over-cap relay log is rotated in place (robots-dcgg) ───────────────
# relay.err.log reached 277 MB on the live box with no rotation anywhere. Every
# ensure-up now caps the logs: over-cap content is truncated IN PLACE (a live
# launchd relay holds an O_APPEND fd on this exact path, so `mv` would leave it
# growing the renamed file) with the last 1 MiB kept in <log>.1.
python3 -c "import sys; open(sys.argv[1],'w').write('relay: agent poll error\n' * 200)" "$ERR_LOG"
BIG_SIZE="$(wc -c < "$ERR_LOG" | tr -d ' ')"
LOGCAP=1000 run running 0
NEW_SIZE="$(wc -c < "$ERR_LOG" | tr -d ' ')"
if [ "$RC" -ne 0 ]; then
  fail "log rotation: healthy run exited $RC, want 0 ($(cat "$ROOT/out"))"
elif [ "$NEW_SIZE" -ge "$BIG_SIZE" ]; then
  fail "log rotation: err log not truncated (${BIG_SIZE} -> ${NEW_SIZE} bytes)"
elif [ ! -s "$ERR_LOG.1" ]; then
  fail "log rotation: no tail kept in relay.err.log.1"
elif ! grep -q "log rotated" "$ERR_LOG"; then
  fail "log rotation: truncated log carries no rotation marker line"
else
  pass "over-cap err log is truncated in place with its tail kept in .1"
fi

# ── 11. An under-cap log is left alone ────────────────────────────────────────
printf 'small\n' > "$ERR_LOG"
rm -f "$ERR_LOG.1"
run running 0
if [ "$RC" -ne 0 ]; then
  fail "under-cap log: healthy run exited $RC, want 0"
elif [ "$(cat "$ERR_LOG")" != "small" ]; then
  fail "under-cap log: was modified ($(cat "$ERR_LOG"))"
elif [ -e "$ERR_LOG.1" ]; then
  fail "under-cap log: a .1 was created for a log under the cap"
else
  pass "under-cap log is left untouched"
fi

if [ "$FAILED" -ne 0 ]; then
  echo "ensure-up.test.sh: FAILED" >&2
  exit 1
fi
echo "ensure-up.test.sh: all tests passed"
