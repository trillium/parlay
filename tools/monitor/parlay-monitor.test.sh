#!/usr/bin/env bash
# Regression harness for parlay-monitor.sh: upstream-server scoping (robots-buu8),
# probe survivability (robots-dcag), and reader lifetime (robots-3pvi).
#
# The defect: a relay is a per-runtime-dir singleton bound to ONE upstream server,
# so enrolling on the shared $TMPDIR/parlay relay registered the agent against
# PRODUCTION :31337 no matter what $PARLAY_SERVER said — a sandbox test silently
# polluted the captain's live agent registry.
#
# Two halves are covered here, matching the two halves of the fix:
#   A. resolution — a non-default $PARLAY_SERVER gets its OWN runtime dir, so it
#      gets its own relay and never shares production's (lib.sh).
#   B. refusal    — even when the runtime dir is pinned past that scoping, the
#      monitor reads the relay's own /agents.server and ABORTS BEFORE /register
#      rather than enrolling into the wrong registry (parlay-monitor.sh).
#   C. probe      — a slow/failed /agents read degrades to unverified, never to
#      a silent death before streaming (robots-dcag).
#   D. lifetime   — a reader never outlives its launcher, a channel keeps exactly
#      one reader, and --reap cleans up what already leaked (robots-3pvi).
#   E. preflight  — --preflight verifies the relay is up and correctly scoped
#      WITHOUT registering or announcing, so `parlay listen`/`parlay claim` can
#      probe BEFORE enrollment and never leave a fresh-clone user registered-but-
#      deaf (issue #173).
#
# No production state is touched: every case runs against a stub relay on a unix
# socket in its own temp dir, with $HOME and the runtime dir redirected there.
# Section D kills processes and sweeps readers, both scoped to that temp dir —
# it can never reach a live fleet reader.
#
# Usage: tools/monitor/parlay-monitor.test.sh [-v]
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
MONITOR="${HERE}/parlay-monitor.sh"
LIB="${HERE}/../relay/deploy/lib.sh"
DEFAULT_SERVER="http://localhost:4242"

VERBOSE=0
[ "${1:-}" = "-v" ] && VERBOSE=1

pass=0
fail=0
ok()   { pass=$((pass + 1)); echo "  ok   — $1"; }
bad()  { fail=$((fail + 1)); echo "  FAIL — $1"; [ -n "${2:-}" ] && echo "         $2"; }
note() { [ "${VERBOSE}" = 1 ] && echo "         $*"; return 0; }

command -v bun >/dev/null 2>&1 || { echo "parlay-monitor.test: bun is required" >&2; exit 2; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-monitor-test.XXXXXX")"
# Collapse duplicate slashes: $TMPDIR ends in "/" on macOS, and the relay's
# /register response comes back path-normalized. Section D matches reader
# command lines EXACTLY, so "//" here would make every reader unfindable.
ROOT="$(printf '%s' "${ROOT}" | sed 's://*:/:g')"
# Space-separated PID list, not an array — macOS ships bash 3.2, where expanding
# an empty array under `set -u` is itself an error.
STUBS=""
cleanup() {
  for p in ${STUBS}; do kill "${p}" 2>/dev/null; done
  # Section D deliberately orphans readers. A harness for a reader-leak bug must
  # not leak readers itself — sweep anything still tailing a spool under $ROOT,
  # including the debris a FAILED case left behind. Matched on $ROOT, so this can
  # never touch a fleet reader.
  for p in $(ps -axo pid=,command= 2>/dev/null | awk -v dir="${ROOT}/" '
      { pid = $1; sub(/^[[:space:]]*[0-9]+[[:space:]]+/, "")
        if ($0 ~ /^tail -n0 -F /) { spool = substr($0, 13); if (index(spool, dir) == 1) print pid } }'); do
    kill -9 "${p}" 2>/dev/null
  done
  rm -rf "${ROOT}"
}
trap cleanup EXIT

# ── Stub relay: a unix-socket HTTP server speaking the real control protocol ────
# Answers /health, /agents (reporting the server it was told to be bound to), and
# /register — and appends every request path to a log so a test can assert that
# /register was NEVER reached.
cat >"${ROOT}/stub-relay.ts" <<'TS'
import { appendFileSync, writeFileSync } from "fs"
import { join } from "path"

const sock = process.argv[2]
const boundServer = process.argv[3]
const runtime = process.argv[4]
const log = process.argv[5]
// Milliseconds to stall GET /agents before answering, simulating a real relay
// whose registry response grows with the fleet (robots-dcag). /health stays
// instant, matching the real relay: it binds and serves before spool replay.
const agentsDelayMs = Number(process.argv[6] ?? 0)

Bun.serve({
  unix: sock,
  async fetch(req) {
    const path = new URL(req.url).pathname
    appendFileSync(log, `${req.method} ${path}\n`)
    if (path === "/health") return Response.json({ ok: true })
    if (path === "/agents") {
      if (agentsDelayMs > 0)
        await new Promise((r) => setTimeout(r, agentsDelayMs))
      return Response.json({ agents: [], server: boundServer, runtime })
    }
    if (path === "/register") {
      const body = (await req.json()) as { agent: string }
      const spool = join(runtime, `${body.agent}.chan`)
      writeFileSync(spool, "")
      return Response.json({ ok: true, agent: body.agent, spool })
    }
    return Response.json({ error: "not found" }, { status: 404 })
  },
})
TS

# start_stub <dir> <bound-server> [agents-delay-ms] → STUB_SOCK/STUB_LOG/STUB_RUNTIME
start_stub() {
  STUB_RUNTIME="$1"
  local bound="$2"
  local delay="${3:-0}"
  mkdir -p "${STUB_RUNTIME}"
  STUB_SOCK="${STUB_RUNTIME}/relay.sock"
  STUB_LOG="${STUB_RUNTIME}/requests.log"
  : >"${STUB_LOG}"
  bun "${ROOT}/stub-relay.ts" "${STUB_SOCK}" "${bound}" "${STUB_RUNTIME}" "${STUB_LOG}" "${delay}" \
    >"${STUB_RUNTIME}/stub.log" 2>&1 &
  STUBS="${STUBS} $!"
  for _ in $(seq 1 60); do
    [ -S "${STUB_SOCK}" ] && return 0
    sleep 0.1
  done
  echo "parlay-monitor.test: stub relay never bound ${STUB_SOCK}" >&2
  cat "${STUB_RUNTIME}/stub.log" >&2
  return 1
}

# run_monitor <runtime> <sock> <server> <agent> → OUT/ERR/CODE globals.
# The monitor streams with `tail -F` on success and never exits, so it runs
# backgrounded with a bounded wait; a still-alive process is reported as
# CODE=running. Section D covers the streaming path itself.
run_monitor() {
  local runtime="$1" sock="$2" server="$3" agent="$4"
  local out="${ROOT}/run.out" err="${ROOT}/run.err"
  : >"${out}"; : >"${err}"
  (
    export HOME="${ROOT}/home"
    export PARLAY_RELAY_RUNTIME="${runtime}"
    export PARLAY_RELAY_SOCK="${sock}"
    [ -n "${server}" ] && export PARLAY_SERVER="${server}"
    # Set by a caller that needs a specific probe bound (section C); unset
    # otherwise so every other test exercises the shipped default.
    [ -n "${PROBE_TIMEOUT_OVERRIDE:-}" ] \
      && export PARLAY_RELAY_PROBE_TIMEOUT="${PROBE_TIMEOUT_OVERRIDE}"
    exec "${MONITOR}" --agent "${agent}"
  ) >"${out}" 2>"${err}" &
  local pid=$!
  CODE="running"
  for _ in $(seq 1 60); do
    if ! kill -0 "${pid}" 2>/dev/null; then
      wait "${pid}"; CODE=$?
      break
    fi
    # Reached the streaming stage — that is a terminal outcome for this harness.
    grep -q "streaming" "${err}" 2>/dev/null && break
    sleep 0.1
  done
  if [ "${CODE}" = "running" ]; then
    kill "${pid}" 2>/dev/null
    pkill -P "${pid}" 2>/dev/null
    wait "${pid}" 2>/dev/null
  fi
  ERR="$(cat "${err}")"
  note "exit=${CODE} stderr: ${ERR}"
}

mkdir -p "${ROOT}/home"

# ══ A. runtime-dir resolution is scoped by upstream server ════════════════════
echo "A. server-scoped runtime dir (lib.sh)"

# shellcheck source=/dev/null
. "${LIB}"

canonical="$(unset PARLAY_RELAY_RUNTIME; parlay_relay_runtime_dir)"

got="$(unset PARLAY_RELAY_RUNTIME; PARLAY_SERVER="" parlay_relay_scoped_runtime_dir)"
[ "${got}" = "${canonical}" ] \
  && ok "unset PARLAY_SERVER → canonical dir (${canonical})" \
  || bad "unset PARLAY_SERVER should use the canonical dir" "got ${got}"

got="$(unset PARLAY_RELAY_RUNTIME; PARLAY_SERVER="${DEFAULT_SERVER}" parlay_relay_scoped_runtime_dir)"
[ "${got}" = "${canonical}" ] \
  && ok "default server → canonical dir (shares the production relay)" \
  || bad "default server should use the canonical dir" "got ${got}"

got="$(unset PARLAY_RELAY_RUNTIME; PARLAY_SERVER="${DEFAULT_SERVER}/" parlay_relay_scoped_runtime_dir)"
[ "${got}" = "${canonical}" ] \
  && ok "default server with a trailing slash normalizes to the canonical dir" \
  || bad "trailing slash should not fork a scoped dir" "got ${got}"

scratch_a="$(unset PARLAY_RELAY_RUNTIME; PARLAY_SERVER="http://127.0.0.1:45001" parlay_relay_scoped_runtime_dir)"
case "${scratch_a}" in
  "${canonical}") bad "scratch server MUST NOT share production's runtime dir" "got ${scratch_a}" ;;
  "${canonical}"/srv-*) ok "scratch server → its own dir (${scratch_a##*/})" ;;
  *) bad "scratch server dir is not a scoped dir under the canonical dir" "got ${scratch_a}" ;;
esac

# The scoped dir must never be mistaken for a spool: the production relay's
# resume scan only picks up "<agent>.chan" entries.
case "${scratch_a##*/}" in
  *.chan) bad "scoped dir name ends in .chan — the production relay would resume it as a spool" ;;
  *) ok "scoped dir name cannot be read as a .chan spool" ;;
esac

# sun_path is 104 bytes on macOS. A scoped socket path that overflows it makes the
# relay die with a bare "bind: invalid argument" — this is what a readable slug did.
scratch_sock="${scratch_a}/relay.sock"
if parlay_relay_sock_path_ok "${scratch_sock}"; then
  ok "scoped control-socket path fits sun_path (${#scratch_sock} bytes)"
else
  bad "scoped socket path is ${#scratch_sock} bytes — the relay cannot bind it" "${scratch_sock}"
fi

scratch_b="$(unset PARLAY_RELAY_RUNTIME; PARLAY_SERVER="http://127.0.0.1:45002" parlay_relay_scoped_runtime_dir)"
[ "${scratch_a}" != "${scratch_b}" ] \
  && ok "two different scratch servers get two different dirs" \
  || bad "distinct servers collided onto one runtime dir" "both ${scratch_a}"

# A long URL differing only past the readable-prefix truncation must still split.
long_a="$(unset PARLAY_RELAY_RUNTIME; PARLAY_SERVER="http://a-very-long-host-name-for-truncation.example.test:1/aaa" parlay_relay_scoped_runtime_dir)"
long_b="$(unset PARLAY_RELAY_RUNTIME; PARLAY_SERVER="http://a-very-long-host-name-for-truncation.example.test:1/bbb" parlay_relay_scoped_runtime_dir)"
[ "${long_a}" != "${long_b}" ] \
  && ok "servers differing only past the readable prefix still get distinct dirs" \
  || bad "hash suffix failed to disambiguate truncated slugs" "both ${long_a}"

pinned="${ROOT}/pinned-runtime"
got="$(PARLAY_RELAY_RUNTIME="${pinned}" PARLAY_SERVER="http://127.0.0.1:45001" parlay_relay_scoped_runtime_dir)"
[ "${got}" = "${pinned}" ] \
  && ok "explicit PARLAY_RELAY_RUNTIME still wins over scoping" \
  || bad "explicit PARLAY_RELAY_RUNTIME was overridden" "got ${got}"

# ══ B. the monitor refuses to enroll on a wrong-server relay ══════════════════
echo "B. cross-server enroll is refused before /register"

# B1. THE BUG: runtime dir pinned at a relay bound to production, PARLAY_SERVER
#     pointing at a scratch server. Pre-fix this enrolled into production.
start_stub "${ROOT}/prod-relay" "${DEFAULT_SERVER}" || exit 1
run_monitor "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45001" "verify-agent"
if [ "${CODE}" = 1 ]; then
  ok "exits 1 when the relay's server differs from PARLAY_SERVER"
else
  bad "should exit 1 on a cross-server enroll" "exit=${CODE}"
fi
if grep -q "/register" "${STUB_LOG}"; then
  bad "REGISTERED on the production relay — the robots-buu8 leak is still open" \
      "$(tr '\n' ' ' <"${STUB_LOG}")"
else
  ok "never sent /register to the production relay"
fi
# Whichever guard fires first must name the relay's actual bound server. When the
# runtime dir is pinned to a wrong-server relay, ensure-up exits 3 (robots-93xu)
# before the monitor's own pre-enroll guard runs — and ensure-up already prints
# the mismatch, naming the bound server, so accept either form. The monitor must
# NOT add its generic "install the relay" advice on top (a relay is what is wrong).
case "${ERR}" in
  *"refusing to enroll"*"bound to ${DEFAULT_SERVER}"*) ok "error names the relay's actual bound server (monitor guard)" ;;
  *"bound to ${DEFAULT_SERVER}"*) ok "error names the relay's actual bound server (ensure-up exit 3)" ;;
  *"install the relay"*) bad "error was masked by generic 'install the relay' advice" "${ERR}" ;;
  *) bad "error message does not explain the mismatch" "${ERR}" ;;
esac
[ ! -e "${STUB_RUNTIME}/verify-agent.chan" ] \
  && ok "no spool created in the production runtime dir" \
  || bad "left a spool file behind in the production runtime dir"

# B2. Matching server: the same guard must NOT block a legitimate enroll.
start_stub "${ROOT}/scratch-relay" "http://127.0.0.1:45001" || exit 1
run_monitor "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45001" "verify-agent"
if grep -q "/register" "${STUB_LOG}"; then
  ok "enrolls normally when the relay serves the requested server"
else
  bad "matching server was refused — the guard is over-broad" "exit=${CODE} ${ERR}"
fi

# B3. A relay reporting a trailing slash is the same server, not a mismatch.
start_stub "${ROOT}/slash-relay" "http://127.0.0.1:45001/" || exit 1
run_monitor "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45001" "verify-agent"
if grep -q "/register" "${STUB_LOG}"; then
  ok "trailing-slash difference is not treated as a server mismatch"
else
  bad "trailing slash falsely read as a mismatch" "exit=${CODE} ${ERR}"
fi

# B4. lib.sh absent (older/partial checkout): scoping is unavailable, but the
#     refusal guard is self-contained and must STILL close the leak.
mkdir -p "${ROOT}/nolib/tools/monitor"
cp "${MONITOR}" "${ROOT}/nolib/tools/monitor/parlay-monitor.sh"
chmod +x "${ROOT}/nolib/tools/monitor/parlay-monitor.sh"
start_stub "${ROOT}/nolib-relay" "${DEFAULT_SERVER}" || exit 1
MONITOR_SAVED="${MONITOR}"
MONITOR="${ROOT}/nolib/tools/monitor/parlay-monitor.sh"
run_monitor "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45001" "verify-agent"
MONITOR="${MONITOR_SAVED}"
if [ "${CODE}" = 1 ] && ! grep -q "/register" "${STUB_LOG}"; then
  ok "still refuses the cross-server enroll with no lib.sh alongside"
else
  bad "leak reopens when lib.sh is missing" "exit=${CODE} requests: $(tr '\n' ' ' <"${STUB_LOG}")"
fi

# B5. No PARLAY_SERVER set: nothing to compare against, behave as before.
start_stub "${ROOT}/default-relay" "${DEFAULT_SERVER}" || exit 1
run_monitor "${STUB_RUNTIME}" "${STUB_SOCK}" "" "verify-agent"
if grep -q "/register" "${STUB_LOG}"; then
  ok "unset PARLAY_SERVER enrolls unchanged (no new failure mode)"
else
  bad "unset PARLAY_SERVER was blocked" "exit=${CODE} ${ERR}"
fi

# ══ C. a slow /agents probe must never kill the monitor ══════════════════════
# robots-dcag: the section-B probe was a bare `VAR=$(curl … | sed …)`, and under
# the monitor's `set -euo pipefail` a curl timeout (exit 28) became the
# assignment's status and killed the script right here — before /register, with
# curl's stderr sent to /dev/null. `parlay listen` had already registered and
# announced the agent, so it sat in the panel looking healthy with no stream.
echo
echo "C. a slow/failed server probe degrades to unverified, never to death"

# C1. /agents stalls past the probe bound. Pre-fix: exit 28, no /register.
start_stub "${ROOT}/c1" "http://127.0.0.1:45001" 3000 || exit 1
PROBE_TIMEOUT_OVERRIDE=1
run_monitor "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45001" "verify-agent"
if [ "${CODE}" = 28 ]; then
  bad "monitor died with curl's timeout code — robots-dcag is still open" "exit=28"
elif grep -q "/register" "${STUB_LOG}"; then
  ok "enrolls anyway when the /agents probe times out"
else
  bad "a slow /agents probe stopped the monitor from enrolling" "exit=${CODE} ${ERR}"
fi
case "${ERR}" in
  *"proceeding unverified"*) ok "says out loud that the server could not be verified" ;;
  *) bad "skipped verification silently — a real mismatch would slip through" "${ERR}" ;;
esac

# C2. Same, with no lib.sh alongside: the inline fallback probe is a second copy
#     of the same code and must be guarded identically. This path can also name
#     curl's exit code, which the lib.sh helper swallows by design.
start_stub "${ROOT}/c2" "http://127.0.0.1:45001" 3000 || exit 1
MONITOR_SAVED="${MONITOR}"
MONITOR="${ROOT}/nolib/tools/monitor/parlay-monitor.sh"
cp "${MONITOR_SAVED}" "${MONITOR}"; chmod +x "${MONITOR}"
run_monitor "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45001" "verify-agent"
MONITOR="${MONITOR_SAVED}"
if grep -q "/register" "${STUB_LOG}"; then
  ok "inline fallback probe is guarded too (no lib.sh)"
else
  bad "fallback probe still aborts on timeout" "exit=${CODE} ${ERR}"
fi
case "${ERR}" in
  *"curl exit 28"*) ok "names curl's timeout code so the cause is diagnosable" ;;
  *) bad "probe failure does not report why" "${ERR}" ;;
esac
PROBE_TIMEOUT_OVERRIDE=""

# C3. THE GUARD MUST NOT BECOME A NO-OP. A relay that is merely slow — but still
#     answers inside the (now generous) budget — must be read and refused when it
#     serves the wrong server. Tolerating an unknown answer is the fix; treating
#     every answer as unknown would silently reopen robots-buu8.
start_stub "${ROOT}/c3" "${DEFAULT_SERVER}" 1200 || exit 1
PROBE_TIMEOUT_OVERRIDE=10
run_monitor "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45001" "verify-agent"
PROBE_TIMEOUT_OVERRIDE=""
if [ "${CODE}" = 1 ] && ! grep -q "/register" "${STUB_LOG}"; then
  ok "a slow but answering relay is still verified and refused (buu8 intact)"
else
  bad "the cross-server guard went slack — robots-buu8 reopens" \
      "exit=${CODE} requests: $(tr '\n' ' ' <"${STUB_LOG}")"
fi

# C4. Any setup failure must announce the registered-but-deaf consequence. The
#     original bug was survivable only because it was silent; nothing in the
#     monitor's output distinguished "died" from "streaming quietly".
case "${ERR}" in
  *"registered-but-deaf"*) ok "a setup failure names the registered-but-deaf outcome" ;;
  *) bad "setup failure exits without warning the agent is deaf" "${ERR}" ;;
esac

# C5. The default probe bound must be well clear of a real /agents response. The
#     shipped default was 2s; the captain's box answers in >2s at 269 agents.
default_probe="$(sed -n 's/.*PARLAY_RELAY_PROBE_TIMEOUT:-\([0-9]*\)}.*/\1/p' "${MONITOR}" | head -1)"
if [ -n "${default_probe}" ] && [ "${default_probe}" -ge 10 ]; then
  ok "default probe timeout is ${default_probe}s (clear of a real /agents read)"
else
  bad "default probe timeout is too tight for a real fleet" "got '${default_probe}'"
fi

# C6. lib.sh's own helper must honor the same tunable and still return 0 on a
#     timeout — it is the probe the monitor uses whenever lib.sh is present, so a
#     hardcoded 2s there would leave the real >2s /agents read unverifiable.
start_stub "${ROOT}/c6" "${DEFAULT_SERVER}" 1500 || exit 1
got="$(PARLAY_RELAY_PROBE_TIMEOUT=10 parlay_relay_reported_server "${STUB_SOCK}")"
[ "${got}" = "${DEFAULT_SERVER}" ] \
  && ok "parlay_relay_reported_server reads a slow relay when given the budget" \
  || bad "helper ignores PARLAY_RELAY_PROBE_TIMEOUT (still capped at 2s?)" "got '${got}'"

got="$(PARLAY_RELAY_PROBE_TIMEOUT=1 parlay_relay_reported_server "${STUB_SOCK}")"; rc=$?
[ "${rc}" = 0 ] && [ -z "${got}" ] \
  && ok "helper returns 0 with an empty result on timeout (never aborts a caller)" \
  || bad "helper signals failure on timeout — set -e callers would die" "rc=${rc} got='${got}'"

# ══ D. one reader per channel, and no reader outliving its launcher ══════════
# robots-3pvi: nothing ever ended a `tail -F`. A harness kills only the shell it
# spawned; the reader sits below that, reparents to init, and on a quiet channel
# never writes again so it never even earns a SIGPIPE. They accumulated — 168
# live on the captain's box, 142 orphaned, one channel with 20 readers — and
# because the spool is append-only, every extra reader re-delivers every
# directive to a session that is already dead.
#
# Every case here runs against the test's OWN runtime dir, and --reap is scoped
# to that dir, so nothing in this section can reach a live fleet reader.
echo
echo "D. reader lifetime and duplicate eviction (robots-3pvi)"

# readers_of_spool <spool> → pids, matched as a WHOLE command line (never a
# pgrep -f regex, which a metacharacter in a path could widen).
readers_of_spool() {
  ps -axo pid=,command= 2>/dev/null | awk -v want="tail -n0 -F $1" '
    { pid = $1; sub(/^[[:space:]]*[0-9]+[[:space:]]+/, ""); if ($0 == want) print pid }' || true
}

# launch_monitor <runtime> <sock> <server> <agent> → LAUNCHER_PID
# The subshell is a REAL launcher, not an exec-away wrapper: the trailing `true`
# defeats bash's implicit-exec optimization so the monitor runs as its CHILD.
# Killing LAUNCHER_PID then reproduces exactly what a harness does — kill the
# shell it spawned and leave everything below it reparented to init.
launch_monitor() {
  local runtime="$1" sock="$2" server="$3" agent="$4"
  (
    export HOME="${ROOT}/home"
    export PARLAY_RELAY_RUNTIME="${runtime}"
    export PARLAY_RELAY_SOCK="${sock}"
    export PARLAY_MONITOR_WATCH_INTERVAL=1
    [ -n "${server}" ] && export PARLAY_SERVER="${server}"
    [ -n "${NO_ORPHAN_EXIT_OVERRIDE:-}" ] \
      && export PARLAY_MONITOR_NO_ORPHAN_EXIT="${NO_ORPHAN_EXIT_OVERRIDE}"
    "${MONITOR}" --agent "${agent}" || true
    true
  ) >>"${ROOT}/d.out" 2>>"${ROOT}/d.err" &
  LAUNCHER_PID=$!
  STUBS="${STUBS} ${LAUNCHER_PID}"
  # These launchers are SIGKILLed on purpose; disowning keeps bash from printing
  # a "Killed: 9" job notice over the test output.
  disown "${LAUNCHER_PID}" 2>/dev/null || true
}

# wait_for_reader <spool> → READER_PID (empty + rc 1 if none appears)
wait_for_reader() {
  for _ in $(seq 1 100); do
    READER_PID="$(readers_of_spool "$1" | head -1)"
    if [ -n "${READER_PID}" ]; then return 0; fi
    sleep 0.1
  done
  READER_PID=""
  return 1
}

# wait_for_gone <pid> [tries] → 0 once the pid is gone
wait_for_gone() {
  for _ in $(seq 1 "${2:-100}"); do
    if ! kill -0 "$1" 2>/dev/null; then return 0; fi
    sleep 0.1
  done
  return 1
}

D_SERVER="http://127.0.0.1:45003"

# D1. A reader must not outlive its launcher. This IS the leak.
start_stub "${ROOT}/d1" "${D_SERVER}" || exit 1
d1_runtime="${STUB_RUNTIME}"; d1_sock="${STUB_SOCK}"
launch_monitor "${d1_runtime}" "${d1_sock}" "${D_SERVER}" "reap-me"
d1_launcher="${LAUNCHER_PID}"
if wait_for_reader "${d1_runtime}/reap-me.chan"; then
  d1_reader="${READER_PID}"
  # SIGKILL: the launcher gets no chance to pass anything down, exactly like a
  # harness tearing down its shell. Only the watchdog can save us here.
  kill -9 "${d1_launcher}" 2>/dev/null
  if wait_for_gone "${d1_reader}" 150; then
    ok "reader dies when its launcher is killed (no orphaned tail -F)"
  else
    bad "reader survived its launcher — robots-3pvi is still open" "pid ${d1_reader}"
    kill -9 "${d1_reader}" 2>/dev/null
  fi
else
  bad "monitor never started a reader" "$(tail -3 "${ROOT}/d.err")"
fi

# D2. A second monitor on the same channel evicts the first. Two readers on an
#     append-only spool means one directive wakes two sessions.
start_stub "${ROOT}/d2" "${D_SERVER}" || exit 1
d2_runtime="${STUB_RUNTIME}"; d2_sock="${STUB_SOCK}"
d2_spool="${d2_runtime}/dupe.chan"
launch_monitor "${d2_runtime}" "${d2_sock}" "${D_SERVER}" "dupe"
if wait_for_reader "${d2_spool}"; then
  d2_first="${READER_PID}"
  launch_monitor "${d2_runtime}" "${d2_sock}" "${D_SERVER}" "dupe"
  d2_second_launcher="${LAUNCHER_PID}"
  if wait_for_gone "${d2_first}" 100; then
    ok "a new monitor evicts the channel's existing reader"
  else
    bad "two readers now share one channel — every directive lands twice" "first=${d2_first}"
  fi
  sleep 0.5
  d2_count="$(readers_of_spool "${d2_spool}" | wc -l | tr -d ' ')"
  [ "${d2_count}" = 1 ] \
    && ok "exactly one reader remains on the channel" \
    || bad "channel has ${d2_count} readers after eviction" "expected 1"
  case "$(cat "${ROOT}/d.err")" in
    *"already has reader"*) ok "eviction is announced, not silent" ;;
    *) bad "evicted a reader without saying so" "$(tail -3 "${ROOT}/d.err")" ;;
  esac
  kill -9 "${d2_second_launcher}" 2>/dev/null
  for p in $(readers_of_spool "${d2_spool}"); do kill -9 "${p}" 2>/dev/null; done
else
  bad "monitor never started a reader for the duplicate case" "$(tail -3 "${ROOT}/d.err")"
fi

# D3. --reap: dry run reports and kills NOTHING; --apply kills the orphan and
#     spares a live reader. The orphan is made with the documented
#     daemonization escape hatch so the script's own watchdog stays out of it.
start_stub "${ROOT}/d3" "${D_SERVER}" || exit 1
d3_runtime="${STUB_RUNTIME}"; d3_sock="${STUB_SOCK}"

NO_ORPHAN_EXIT_OVERRIDE=1
launch_monitor "${d3_runtime}" "${d3_sock}" "${D_SERVER}" "stray"
d3_stray_launcher="${LAUNCHER_PID}"
NO_ORPHAN_EXIT_OVERRIDE=""
wait_for_reader "${d3_runtime}/stray.chan" || bad "no reader for the stray case"
d3_stray="${READER_PID}"
# Kill the whole chain above the reader with SIGKILL: bash cannot trap it, so
# the tail is left rooted at init — a genuine orphan, built the way real ones
# are built rather than simulated.
pkill -9 -P "${d3_stray_launcher}" 2>/dev/null
kill -9 "${d3_stray_launcher}" 2>/dev/null
sleep 0.5

launch_monitor "${d3_runtime}" "${d3_sock}" "${D_SERVER}" "healthy"
d3_live_launcher="${LAUNCHER_PID}"
wait_for_reader "${d3_runtime}/healthy.chan" || bad "no reader for the live case"
d3_live="${READER_PID}"

reap_out="$(PARLAY_RELAY_RUNTIME="${d3_runtime}" "${MONITOR}" --reap 2>&1)"
note "${reap_out}"
case "${reap_out}" in
  *"pid ${d3_stray}"*) ok "--reap lists the orphaned reader" ;;
  *) bad "--reap missed an orphaned reader" "pid ${d3_stray}: ${reap_out}" ;;
esac
case "${reap_out}" in
  *"pid ${d3_live}"*) bad "--reap flagged a LIVE reader as an orphan" "pid ${d3_live}" ;;
  *) ok "--reap does not flag a reader whose launcher is alive" ;;
esac
if kill -0 "${d3_stray}" 2>/dev/null; then
  ok "--reap without --apply is a dry run (killed nothing)"
else
  bad "--reap killed without --apply" "pid ${d3_stray} is gone"
fi

apply_out="$(PARLAY_RELAY_RUNTIME="${d3_runtime}" "${MONITOR}" --reap --apply 2>&1)"
note "${apply_out}"
if wait_for_gone "${d3_stray}" 60; then
  ok "--reap --apply kills the orphaned reader"
else
  bad "--apply left the orphan running" "pid ${d3_stray}"
  kill -9 "${d3_stray}" 2>/dev/null
fi
if kill -0 "${d3_live}" 2>/dev/null; then
  ok "--reap --apply spares the live reader"
else
  bad "--apply killed a healthy monitor's reader" "pid ${d3_live}"
fi

# D4. The reaper must never reach outside the runtime dir it was pointed at. An
#     unscoped host-wide kill would take the captain's live readers with it.
scoped_out="$(PARLAY_RELAY_RUNTIME="${ROOT}/no-such-runtime" "${MONITOR}" --reap 2>&1)"
case "${scoped_out}" in
  *"0 reader(s)"*) ok "--reap is scoped to its runtime dir (sees none elsewhere)" ;;
  *) bad "--reap reached beyond its runtime dir" "${scoped_out}" ;;
esac

kill -9 "${d3_live_launcher}" 2>/dev/null
for p in $(readers_of_spool "${d3_runtime}/healthy.chan"); do kill -9 "${p}" 2>/dev/null; done

# ══ E. --preflight verifies the relay WITHOUT registering (issue #173) ═════════
# The defect: `parlay listen` posted register-agent + the "listening" announce,
# then shelled out to parlay-monitor.sh whose ensure-up failed ("no relay
# binary") — a fresh-clone user ends up with a permanently enrolled, deaf agent.
# --preflight is the probe `parlay listen`/`parlay claim` run BEFORE that
# enrollment: it must walk the same guards as a real stream — runtime-dir
# scoping, ensure-up, the socket guard, the cross-server refusal — then exit
# cleanly at the pre-enroll point. It may never touch /register or create a spool.
echo "E. --preflight verifies the relay without registering (issue #173)"

# run_preflight <runtime> <sock> <server> <agent> → CODE/ERR globals, no stream.
# Preflight exits (it never reaches `tail`), so unlike run_monitor this can be a
# plain foreground command — no backgrounded process, no bounded wait.
run_preflight() {
  local runtime="$1" sock="$2" server="$3" agent="$4"
  local out="${ROOT}/pre.out" err="${ROOT}/pre.err"
  : >"${out}"; : >"${err}"
  (
    export HOME="${ROOT}/home"
    export PARLAY_RELAY_RUNTIME="${runtime}"
    export PARLAY_RELAY_SOCK="${sock}"
    [ -n "${server}" ] && export PARLAY_SERVER="${server}"
    exec "${MONITOR}" --preflight --agent "${agent}"
  ) >"${out}" 2>"${err}"
  CODE=$?
  ERR="$(cat "${err}")"
  note "exit=${CODE} stderr: ${ERR}"
}

# E1. A relay that serves the requested server verifies ready: exit 0, and — the
#     point — /register is never reached and no spool is created.
e1_runtime="${ROOT}/e1-relay"
start_stub "${e1_runtime}" "http://127.0.0.1:45004" || exit 1
run_preflight "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45004" "preflight-agent"
[ "${CODE}" = 0 ] \
  && ok "preflight exits 0 when the relay serves the requested server" \
  || bad "preflight should exit 0 on a matching relay" "exit=${CODE}: ${ERR}"
if grep -q "/register" "${STUB_LOG}"; then
  bad "preflight sent /register (verify-only)"
else
  ok "preflight sent no /register (verify-only)"
fi
case "${ERR}" in
  *"preflight OK"*) ok "preflight announces readiness on stderr" ;;
  *) bad "preflight did not announce readiness" "${ERR}" ;;
esac
if [ -e "${STUB_RUNTIME}/preflight-agent.chan" ]; then
  bad "preflight created a spool"
else
  ok "preflight created no spool"
fi

# E2. A relay bound to a DIFFERENT upstream server fails the cross-server guard
#     (robots-buu8) — and, being --preflight, reports that nothing was registered
#     rather than the registered-but-deaf consequence.
e2_runtime="${ROOT}/e2-relay"
start_stub "${e2_runtime}" "http://127.0.0.1:45006" || exit 1
run_preflight "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45005" "preflight-agent"
if [ "${CODE}" != 0 ]; then
  ok "preflight exits non-zero on a cross-server relay"
else
  bad "preflight should refuse a cross-server relay" "exit=${CODE}"
fi
if grep -q "/register" "${STUB_LOG}"; then
  bad "preflight sent /register on the cross-server refusal"
else
  ok "preflight never sent /register on the cross-server refusal"
fi
case "${ERR}" in
  *"never registered"*) ok "preflight diagnosis says nothing was registered (not deaf)" ;;
  *) bad "preflight did not say nothing was registered" "${ERR}" ;;
esac

echo
echo "parlay-monitor.test: ${pass} passed, ${fail} failed"
[ "${fail}" = 0 ] || exit 1
