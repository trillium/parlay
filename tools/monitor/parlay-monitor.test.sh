#!/usr/bin/env bash
# Regression harness for parlay-monitor.sh's upstream-server scoping (robots-buu8).
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
#
# Sections C and D cover the two later monitor defects that share robots-buu8's
# failure shape — the agent stays registered while its stream is gone:
#   C. a best-effort probe written as `VAR=$(cmd)` killed setup (robots-dcag).
#   D. the stream itself was terminal, so anything that killed `tail` killed the
#      agent's only reply channel, silently (robots-gv6t).
#
# No production state is touched: every case runs against a stub relay on a unix
# socket in its own temp dir, with $HOME and the runtime dir redirected there.
#
# Usage: tools/monitor/parlay-monitor.test.sh [-v]
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
MONITOR="${HERE}/parlay-monitor.sh"
LIB="${HERE}/../relay/deploy/lib.sh"
DEFAULT_SERVER="http://localhost:31337"

VERBOSE=0
[ "${1:-}" = "-v" ] && VERBOSE=1

pass=0
fail=0
ok()   { pass=$((pass + 1)); echo "  ok   — $1"; }
bad()  { fail=$((fail + 1)); echo "  FAIL — $1"; [ -n "${2:-}" ] && echo "         $2"; }
note() { [ "${VERBOSE}" = 1 ] && echo "         $*"; return 0; }

command -v bun >/dev/null 2>&1 || { echo "parlay-monitor.test: bun is required" >&2; exit 2; }

# Strip $TMPDIR's trailing slash first: macOS sets it with one, and the doubled
# separator survives into every path derived from ROOT — which breaks matching a
# spool path against a running process's argv (section D), where the kernel
# reports the collapsed form.
TMPROOT="${TMPDIR:-/tmp}"
ROOT="$(mktemp -d "${TMPROOT%/}/parlay-monitor-test.XXXXXX")"
# Space-separated PID list, not an array — macOS ships bash 3.2, where expanding
# an empty array under `set -u` is itself an error.
STUBS=""
cleanup() {
  for p in ${STUBS}; do kill "${p}" 2>/dev/null; done
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
# The monitor execs `tail -F` on success and never exits, so it runs backgrounded
# with a bounded wait; a still-alive process is reported as CODE=running.
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
case "${ERR}" in
  *"refusing to enroll"*"${DEFAULT_SERVER}"*) ok "error names the relay's actual bound server" ;;
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

# ══ D. the stream is supervised, not terminal (robots-gv6t) ══════════════════
# The stream used to be `exec tail -F`: whatever killed tail took the agent's
# only reply channel with it, silently — registration and the "listening"
# announce had already succeeded, so the panel kept showing a ready agent.
echo
echo "D. a dead stream is recovered and reported, never silent"

MON_PID=""
MON_OUT=""
MON_ERR=""

# start_monitor <runtime> <sock> <server> <agent> — like run_monitor, but leaves
# the monitor running so the stream itself can be attacked.
start_monitor() {
  local runtime="$1" sock="$2" server="$3" agent="$4"
  MON_OUT="${ROOT}/mon.out"; MON_ERR="${ROOT}/mon.err"
  : >"${MON_OUT}"; : >"${MON_ERR}"
  (
    export HOME="${ROOT}/home"
    export PARLAY_RELAY_RUNTIME="${runtime}"
    export PARLAY_RELAY_SOCK="${sock}"
    [ -n "${server}" ] && export PARLAY_SERVER="${server}"
    # A caller that needs tail to fail on demand prepends a stub dir (D3).
    [ -n "${MONITOR_PATH_PREFIX:-}" ] && export PATH="${MONITOR_PATH_PREFIX}:${PATH}"
    [ -n "${MIN_UPTIME_OVERRIDE:-}" ] && export PARLAY_MONITOR_MIN_UPTIME="${MIN_UPTIME_OVERRIDE}"
    [ -n "${MAX_RESTARTS_OVERRIDE:-}" ] && export PARLAY_MONITOR_MAX_RESTARTS="${MAX_RESTARTS_OVERRIDE}"
    exec "${MONITOR}" --agent "${agent}"
  ) >"${MON_OUT}" 2>"${MON_ERR}" &
  MON_PID=$!
  STUBS="${STUBS} ${MON_PID}"
}

stop_monitor() {
  [ -n "${MON_PID}" ] || return 0
  pkill -P "${MON_PID}" 2>/dev/null
  kill "${MON_PID}" 2>/dev/null
  wait "${MON_PID}" 2>/dev/null
  MON_PID=""
}

# wait_for_tail <spool> — the "streaming" line is printed a beat BEFORE the loop
# spawns tail and takes its starting offset, and that offset is end-of-spool (the
# old `-n0`: don't replay history). Appending inside that window is genuinely
# skipped, so a test that wants a message delivered must wait for tail itself.
wait_for_tail() {
  for _ in $(seq 1 100); do
    pgrep -f "tail -c .*$1" >/dev/null 2>&1 && return 0
    sleep 0.1
  done
  return 1
}

# wait_for <file> <fixed-string> [tenths] — poll until it shows up.
wait_for() {
  local f="$1" pat="$2" tries="${3:-100}"
  for _ in $(seq 1 "${tries}"); do
    grep -qF "${pat}" "${f}" 2>/dev/null && return 0
    sleep 0.1
  done
  return 1
}

# D1/D2. Kill the tail out from under a live stream.
start_stub "${ROOT}/d1" "http://127.0.0.1:45001" || exit 1
D_RUNTIME="${STUB_RUNTIME}"
start_monitor "${D_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45001" "stream-agent"
SPOOL="${D_RUNTIME}/stream-agent.chan"

if wait_for "${MON_ERR}" "streaming" && wait_for_tail "${SPOOL}"; then
  printf 'CHAT_MSG|m1|before the kill\n' >>"${SPOOL}"
  if wait_for "${MON_OUT}" "before the kill"; then
    ok "streams normally before anything goes wrong"
  else
    bad "the baseline stream never delivered a message" "$(cat "${MON_ERR}")"
  fi

  # Kill only the tail — the same shape as whatever signal ended the stream in
  # the field, and precisely what used to end the whole monitor.
  if pkill -f "tail -c .*${SPOOL}" 2>/dev/null; then
    # The message has to arrive AFTER the kill, at a byte offset the dead tail
    # never reached: `-n0` on respawn would drop it, and resuming from the
    # spool's size-at-restart would drop it too.
    printf 'CHAT_MSG|m2|after the kill\n' >>"${SPOOL}"

    if kill -0 "${MON_PID}" 2>/dev/null; then
      ok "the monitor outlives its tail (the stream is no longer terminal)"
    else
      bad "the monitor died with its tail — robots-gv6t is still open" "$(cat "${MON_ERR}")"
    fi
    if wait_for "${MON_OUT}" "MONITOR|restarted|"; then
      ok "announces the restart ON STDOUT, where the harness raises an event"
    else
      bad "a stream death produced no stdout event" "$(cat "${MON_OUT}")"
    fi
    if wait_for "${MON_OUT}" "after the kill"; then
      ok "delivers a message spooled during the gap (byte-offset resume)"
    else
      bad "messages spooled during the restart gap were swallowed" "$(cat "${MON_OUT}")"
    fi
    if [ "$(grep -cF "before the kill" "${MON_OUT}")" = 1 ]; then
      ok "does not re-deliver what the dead tail already emitted"
    else
      bad "resume replayed already-delivered messages" "$(grep -cF "before the kill" "${MON_OUT}") copies"
    fi
  else
    bad "could not find the tail process to kill" "$(ps -o pid=,command= -p "${MON_PID}" 2>/dev/null)"
  fi
else
  bad "the monitor never reached the streaming stage" "$(cat "${MON_ERR}")"
fi
stop_monitor

# D3. A stream that cannot be kept alive must give up LOUDLY and terminally,
#     never spin forever and never fall quiet. A `tail` stub that fails
#     instantly makes every respawn thrash.
mkdir -p "${ROOT}/failtail"
cat >"${ROOT}/failtail/tail" <<'SH'
#!/bin/sh
exit 9
SH
chmod +x "${ROOT}/failtail/tail"

start_stub "${ROOT}/d3" "http://127.0.0.1:45001" || exit 1
MONITOR_PATH_PREFIX="${ROOT}/failtail"
MIN_UPTIME_OVERRIDE=2
MAX_RESTARTS_OVERRIDE=1
start_monitor "${STUB_RUNTIME}" "${STUB_SOCK}" "http://127.0.0.1:45001" "stream-agent"
MONITOR_PATH_PREFIX=""; MIN_UPTIME_OVERRIDE=""; MAX_RESTARTS_OVERRIDE=""

D3_CODE="running"
for _ in $(seq 1 100); do
  if ! kill -0 "${MON_PID}" 2>/dev/null; then
    wait "${MON_PID}"; D3_CODE=$?
    break
  fi
  sleep 0.1
done
if [ "${D3_CODE}" = 1 ]; then
  ok "gives up with exit 1 once restarts stop helping (bounded, not infinite)"
else
  bad "a hopeless stream did not terminate as a runtime error" "exit=${D3_CODE}"
fi
if grep -qF "MONITOR|down|" "${MON_OUT}" && grep -qF "DEAF" "${MON_OUT}"; then
  ok "giving up says so on stdout, in the words the agent needs to act on"
else
  bad "gave up without an agent-visible notice" "$(cat "${MON_OUT}")"
fi
if grep -qF "MONITOR|restarted|" "${MON_OUT}"; then
  ok "tried to recover before giving up"
else
  bad "gave up without attempting a restart" "$(cat "${MON_OUT}")"
fi
stop_monitor

echo
echo "parlay-monitor.test: ${pass} passed, ${fail} failed"
[ "${fail}" = 0 ] || exit 1
