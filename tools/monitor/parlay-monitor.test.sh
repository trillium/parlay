#!/usr/bin/env bash
# Regression harness for parlay-monitor.sh: reader lifetime (robots-3pvi) and
# verify-only preflight (issue #173).
#
# No production state is touched: every case runs against a stub relay on a unix
# socket in its own temp dir, with $HOME and the runtime dir redirected there.
# Section D kills processes and sweeps readers, both scoped to that temp dir —
# it can never reach a live fleet reader.
#
# Usage: tools/monitor/parlay-monitor.test.sh [-v]
set -uo pipefail

# ── Self-isolation: run under a scrubbed, allowlisted environment ────────────
# A developer's PATH can shadow real system binaries with interactive shims —
# the 5 B/C/D failures reproduced 2026-09-04 were all caused by a `sleep` guard
# on PATH (`~/.local/bin`, `~/bin`, etc.) that prints "sleep is not a wait
# strategy" and exits 1. Because the harness runs the monitor and its readers as
# test subprocesses that inherit that PATH, every `sleep 0.1` polling loop in
# sections B/C/D aborted and the suite went red for reasons unrelated to the
# product. This preamble makes the harness self-isolate in-place: it puts the
# system dirs (whose `sleep`/`curl`/`tail` are the real binaries) FIRST on PATH
# so a user shim can never win, and neutralizes the shell-startup hook env vars
# that could otherwise inject functions into every subprocess. Fixed once here
# instead of patching the 5 tests — there is exactly one entry point to harden.
__parlay_sys="/usr/bin:/bin:/usr/sbin:/sbin"
# System dirs are prepended unconditionally: PATH lookup is ORDER-sensitive, so
# the real system `sleep`/`curl`/`tail` (e.g. /bin/sleep on macOS) must sit
# ahead of any user shim directory regardless of whether those dirs also appear
# later in the inherited PATH. Duplicate entries are harmless.
export PATH="${__parlay_sys}:${PATH}"
unset __parlay_sys
# Neutralize shell-startup hook carriers and any exported-function leak from an
# interactive parent so test subprocesses start fresh instead of inheriting them.
unset BASH_ENV ENV PROMPT_COMMAND 2>/dev/null || true
# Scrub the ambient PARLAY_* knobs the harness manages itself. A caller that
# pins a relay runtime would otherwise leak into every test subprocess and skew
# the scenarios. Each run_monitor/launch_monitor subshell re-exports exactly the
# knob it needs, so clearing the inherited values here is the only way to make
# the scenarios deterministic.
unset PARLAY_SERVER PARLAY_RELAY_RUNTIME PARLAY_RELAY_SOCK \
      PARLAY_RELAY_PROBE_TIMEOUT PARLAY_MONITOR_WATCH_INTERVAL \
      PARLAY_MONITOR_NO_ORPHAN_EXIT PARLAY_NOTIFY_BUDGET 2>/dev/null || true
# `${!BASH_FUNC_*}` names every exported-function env entry (e.g.
# `BASH_FUNC_sleep%%`); unsetting them drops a function a developer shell might
# have `export -f`'d. Works in the harness's bash 3.2 (macOS) and 5.x (CI).
for __parlay_f in ${!BASH_FUNC_*}; do
  unset "$__parlay_f" 2>/dev/null || true
done

# ── Regression self-check: the isolation must hold in test SUBPROCESSES, not
# just this shell. Spawn a fresh shell (the way section B/C/D spawn children)
# and assert a poisoned `sleep` no longer survives. On the unhardened baseline
# this resolves `sleep` to the user shim and `sleep 0` fails, so the harness
# refuses to run a red test instead of printing 5 opaque B/C/D failures.
if ! /bin/sh -c 'sleep 0' 2>/dev/null; then
  echo "parlay-monitor.test: self-isolation failed — bare 'sleep' is intercepted" >&2
  echo "   on PATH (resolves to $(command -v sleep 2>/dev/null || echo '<none>'))." >&2
  echo "   A sleep-guard hook would abort the polling loops in sections B/C/D." >&2
  echo "   Refusing to run a red test; fix the PATH shim or the preamble pin." >&2
  exit 2
fi

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
# Answers /health, /agents, and /register — and appends every request path to a
# log so a test can assert that /register was NEVER reached.
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
    /bin/sleep 0.1
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
    # Set by a caller that needs a specific probe bound; unset otherwise.
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
    /bin/sleep 0.1
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
    /bin/sleep 0.1
  done
  READER_PID=""
  return 1
}

# wait_for_gone <pid> [tries] → 0 once the pid is gone
wait_for_gone() {
  for _ in $(seq 1 "${2:-100}"); do
    if ! kill -0 "$1" 2>/dev/null; then return 0; fi
    /bin/sleep 0.1
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
  # The old reader dying (waited for above) and the new monitor's reader being
  # born are separated by the eviction's own sleep + tail spawn, so a blind
  # fixed timer here counts too early under load and reads 0 readers. Poll until
  # the channel stabilizes at exactly one — this makes the assertion
  # deterministic without weakening it: a failed eviction leaves two readers and
  # never reaches one, and a spawn failure leaves zero, both still reported.
  d2_count="0"
  for _ in $(seq 1 100); do
    d2_count="$(readers_of_spool "${d2_spool}" | wc -l | tr -d ' ')"
    [ "${d2_count}" = 1 ] && break
    [ "${d2_count}" != 0 ] && break   # >1 is a real eviction failure; stop early
    /bin/sleep 0.1
  done
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
/bin/sleep 0.5

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
# enrollment: it must walk the same guards as a real stream — ensure-up and
# the socket guard — then exit cleanly at the pre-enroll point. It may never
# touch /register or create a spool.
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

echo
printf 'parlay-monitor.test: %d passed, %d failed\n' "${pass}" "${fail}"
[ "${fail}" = 0 ] || exit 1
