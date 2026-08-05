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

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-monitor-test.XXXXXX")"
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

Bun.serve({
  unix: sock,
  async fetch(req) {
    const path = new URL(req.url).pathname
    appendFileSync(log, `${req.method} ${path}\n`)
    if (path === "/health") return Response.json({ ok: true })
    if (path === "/agents")
      return Response.json({ agents: [], server: boundServer, runtime })
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

# start_stub <dir> <bound-server> → sets STUB_SOCK / STUB_LOG / STUB_RUNTIME
start_stub() {
  STUB_RUNTIME="$1"
  local bound="$2"
  mkdir -p "${STUB_RUNTIME}"
  STUB_SOCK="${STUB_RUNTIME}/relay.sock"
  STUB_LOG="${STUB_RUNTIME}/requests.log"
  : >"${STUB_LOG}"
  bun "${ROOT}/stub-relay.ts" "${STUB_SOCK}" "${bound}" "${STUB_RUNTIME}" "${STUB_LOG}" \
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

echo
echo "parlay-monitor.test: ${pass} passed, ${fail} failed"
[ "${fail}" = 0 ] || exit 1
