#!/usr/bin/env bash
# bootstrap-sandbox.sh — instantiate this example into a throwaway sandbox and
# prove it works, without touching your real ~/.parlay, ~/exchange, or any
# running parlay server.
#
# It copies examples/parlay-state → $SANDBOX/.parlay and examples/data-dir →
# $SANDBOX/data, builds the Go CLI, starts packages/server on a free port with
# $HOME redirected into the sandbox, then exercises the CLI against it.
#
# Usage:
#   examples/bootstrap-sandbox.sh              # run, report, clean up
#   examples/bootstrap-sandbox.sh --keep       # leave the sandbox on disk
#   examples/bootstrap-sandbox.sh --port 45999 # pin the port instead of picking one
#
# Requirements: bun, go, curl. Run it from anywhere; it locates the repo itself.

set -euo pipefail

KEEP=0
PORT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --keep)  KEEP=1; shift ;;
    --port)  PORT="${2:?--port needs a value}"; shift 2 ;;
    -h|--help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

EXAMPLES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$EXAMPLES_DIR/.." && pwd)"

for tool in bun go curl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 1; }
done

# An unused high port. --port overrides; 0 lets the kernel pick one for us.
if [ -z "$PORT" ]; then
  PORT="$(bun -e 'const s=Bun.listen({hostname:"127.0.0.1",port:0,socket:{data(){}}});const p=s.port;s.stop(true);console.log(p)')"
fi
BASE="http://127.0.0.1:$PORT"

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/parlay-example.XXXXXX")"
SERVER_PID=""

cleanup() {
  local status=$?
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  if [ "$KEEP" = "1" ]; then
    echo "sandbox kept: $SANDBOX"
  else
    # Only ever remove a directory this script created under the temp root.
    case "$SANDBOX" in
      */parlay-example.*) rm -rf "$SANDBOX" ;;
      *) echo "refusing to remove unexpected path: $SANDBOX" >&2 ;;
    esac
  fi
  exit $status
}
trap cleanup EXIT

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

# ── 1. Instantiate the example ───────────────────────────────────────────────
say "instantiating the example in $SANDBOX"
mkdir -p "$SANDBOX/data" "$SANDBOX/pai" "$SANDBOX/bin" "$SANDBOX/code/example-project"
cp -R "$EXAMPLES_DIR/parlay-state" "$SANDBOX/.parlay"
cp "$EXAMPLES_DIR"/data-dir/*.json "$EXAMPLES_DIR"/data-dir/*.jsonl "$SANDBOX/data/"
# The READMEs are documentation for a human, not state the tools read.
rm -f "$SANDBOX/.parlay/README.md"

# Baseline for the "did the server actually write here?" check below: whatever
# the seeded history ships with, before anything has run against it.
SEEDED_HISTORY_LINES="$(wc -l < "$SANDBOX/data/chat-history.jsonl" | tr -d ' ')"

# The one placeholder a reader has to replace. Here: point it at the sandbox.
find "$SANDBOX/.parlay/agents" -name identity.md -print0 |
  xargs -0 sed -i.bak "s#/path/to/your/project-worktrees#$SANDBOX/code/worktrees#g; s#/path/to/your/project#$SANDBOX/code/example-project#g"
find "$SANDBOX/.parlay/agents" -name '*.bak' -delete

# The example ships a localhost:4242 default; retarget it at this run's port.
printf '{\n  "server": "%s"\n}\n' "$BASE" > "$SANDBOX/.parlay/config.json"

# ── 2. Build the CLI ─────────────────────────────────────────────────────────
say "building the parlay CLI (tools/cli)"
(cd "$REPO/tools/cli" && go build -o "$SANDBOX/bin/parlay" .)

# Every parlay invocation below runs fully inside the sandbox. PARLAY_SERVER is
# deliberately NOT set, so the sandbox's own config.json is what resolves the
# server — that is one of the things this script proves.
parlay() {
  env -u PARLAY_SERVER \
    HOME="$SANDBOX" \
    PARLAY_STATE_HOME="$SANDBOX/.parlay" \
    PARLAY_AGENT_HOME="$SANDBOX/.parlay/agents" \
    "$SANDBOX/bin/parlay" "$@"
}

# ── 3. Start the server ──────────────────────────────────────────────────────
say "starting packages/server on port $PORT"
# Refuse a port somebody else already holds. Everything below both reads and
# WRITES over $BASE, so adopting a foreign server would mutate its data — the
# one thing this script promises not to do.
if bun -e "try{Bun.listen({hostname:'127.0.0.1',port:$PORT,socket:{data(){}}}).stop(true);process.exit(1)}catch{process.exit(0)}"; then
  echo "port $PORT is already in use; refusing to run against a server this script did not start" >&2
  exit 1
fi
# `exec` is load-bearing: without it the subshell is a real intermediate process
# on stock macOS bash 3.2, $! records IT rather than bun, and the kill in
# cleanup() leaves an orphaned server holding this port while $SANDBOX is
# removed underneath it. exec replaces the subshell, so $! is always the server.
(
  cd "$REPO/packages/server"
  exec env HOME="$SANDBOX" \
    PARLAY_DATA_DIR="$SANDBOX/data" \
    PAI_DIR="$SANDBOX/pai" \
    PARLAY_PORT="$PORT" \
    bun src/index.ts
) > "$SANDBOX/server.log" 2>&1 &
SERVER_PID=$!

# A reachable port is not evidence that OUR server is the one answering: if the
# process we started died (EADDRINUSE, a crash on boot), curl would happily
# succeed against whatever else holds the port and every command below would
# read and write that server's data. So the liveness of $SERVER_PID is checked
# alongside reachability, and a dead one is fatal rather than silently adopted.
server_gone() {
  echo "$1" >&2
  echo "server log follows:" >&2
  cat "$SANDBOX/server.log" >&2
  exit 1
}

for _ in $(seq 1 40); do
  kill -0 "$SERVER_PID" 2>/dev/null ||
    server_gone "the server this script started exited before becoming ready (port $PORT may have been taken)"
  curl -fsS -m 1 "$BASE/api/chat/agents" >/dev/null 2>&1 && break
  sleep 0.25
done
kill -0 "$SERVER_PID" 2>/dev/null ||
  server_gone "the server this script started is no longer running; refusing to use port $PORT"
curl -fsS -m 2 "$BASE/api/chat/agents" >/dev/null ||
  server_gone "server did not come up"
sed -n '1,2p' "$SANDBOX/server.log"

# ── 4. Exercise it ───────────────────────────────────────────────────────────
say "parlay remote — server URL resolved from the sandbox config.json"
parlay remote

say "parlay agents — the seeded registry"
parlay agents

say "parlay send --helm — post to an agent's channel"
parlay send --helm "hello from bootstrap-sandbox.sh"

say "parlay history — read it back"
parlay history 5

say "parlay identity --agent helm — the durable self-knowledge (frontmatter stripped)"
parlay identity --agent helm

say "parlay launch — known agents from the sandbox's ~/.parlay/agents"
parlay launch

say "PUT /api/chat/parlay/settings — settings persist into \$PARLAY_DATA_DIR"
curl -fsS -m 5 -X PUT -H 'Content-Type: application/json' \
  -d '{"textScale":123}' "$BASE/api/chat/parlay/settings" >/dev/null

say "parlay doctor — self-diagnosis for PARLAY_AGENT_ID=helm"
# Captured so the checks below can assert which lines PASSed. WARNs are expected
# (no monitor is armed, no eval engine), so a non-zero exit is not a failure here.
env -u PARLAY_SERVER HOME="$SANDBOX" PARLAY_STATE_HOME="$SANDBOX/.parlay" \
  PARLAY_AGENT_HOME="$SANDBOX/.parlay/agents" PARLAY_AGENT_ID=helm \
  "$SANDBOX/bin/parlay" doctor > "$SANDBOX/doctor.log" 2>&1 || true
cat "$SANDBOX/doctor.log"

# ── 5. Assert ────────────────────────────────────────────────────────────────
say "checks"
fail=0
run_check() {
  local label=$1; shift
  if "$@"; then echo "  PASS  $label"; else echo "  FAIL  $label"; fail=1; fi
}

registry_served_both_agents() {
  local out; out="$(parlay agents)" || return 1
  printf '%s\n' "$out" | grep -q '^helm ' || return 1
  printf '%s\n' "$out" | grep -q '^reviewer ' || return 1
}
run_check "registry served the seeded agents" registry_served_both_agents

message_round_tripped() { parlay history 5 --full | grep -q "hello from bootstrap-sandbox.sh"; }
run_check "message round-tripped through the server" message_round_tripped

message_persisted() { grep -q "hello from bootstrap-sandbox.sh" "$SANDBOX/data/chat-history.jsonl"; }
run_check "message persisted to \$PARLAY_DATA_DIR/chat-history.jsonl" message_persisted

remote_resolved_from_config() { parlay remote | grep -q "source: config"; }
run_check "server URL resolved from config.json" remote_resolved_from_config

# Both halves, so a strip regression fails in either direction: the body prose
# must survive, and no line of the launch-spec frontmatter (its `---` fences or
# its keys) may reach the agent.
identity_body_without_frontmatter() {
  local out; out="$(parlay identity --agent helm)" || return 1
  printf '%s\n' "$out" | grep -q "PURPOSE" || return 1
  if printf '%s\n' "$out" | grep -qx -- '---'; then return 1; fi
  if printf '%s\n' "$out" | grep -q '^id: helm'; then return 1; fi
  return 0
}
run_check "identity.md read back with frontmatter stripped" identity_body_without_frontmatter

launch_specs_for_both() {
  local out; out="$(parlay launch)" || return 1
  printf '%s\n' "$out" | grep -q '^[[:space:]]*helm .*\[live\]' || return 1
  printf '%s\n' "$out" | grep -q '^[[:space:]]*reviewer .*\[live\]' || return 1
}
run_check "launch spec discovered for both agents, both reported live" launch_specs_for_both

# The four seeded chat-history.jsonl lines are loaded by the server and served
# back on the channel each one names — two on helm, two on reviewer. `--full`
# prints `id=… channel=…`, so this asserts routing, not just that the text
# survived.
seeded_history_on_both_channels() {
  local out; out="$(parlay history 20 --full)" || return 1
  printf '%s' "$out" | grep -q 'id=00000000-0000-4000-8000-000000000001 channel=helm' || return 1
  printf '%s' "$out" | grep -q 'id=00000000-0000-4000-8000-000000000003 channel=reviewer' || return 1
}
run_check "seeded history served on both channels" seeded_history_on_both_channels

doctor_passes_core_checks() {
  grep -q '^PASS .*identity\.md ok' "$SANDBOX/doctor.log" || return 1
  grep -q '^PASS .*registered as "helm"' "$SANDBOX/doctor.log" || return 1
  grep -q '^PASS .*server reachable' "$SANDBOX/doctor.log" || return 1
}
run_check "doctor PASSes identity, registry membership, reachability" doctor_passes_core_checks

# The server's persisted WRITES must land in $PARLAY_DATA_DIR and nowhere else.
# Both halves have to prove a write: the example's own files are copied into
# $SANDBOX/data before boot, so merely existing there proves nothing. History
# must have grown past its seeded lines (the send above), and settings must carry
# the value the PUT above sent. "Nowhere else" is checked at the exact paths
# packages/server/src/paths.ts falls back to when PARLAY_DATA_DIR is not honored:
# $HOME/exchange and $PAI_DIR/MEMORY/STATE, both inside the sandbox for this run.
persisted_only_in_data_dir() {
  local now
  now="$(wc -l < "$SANDBOX/data/chat-history.jsonl" | tr -d ' ')" || return 1
  [ "$now" -gt "$SEEDED_HISTORY_LINES" ] || return 1
  grep -q '"textScale": *123' "$SANDBOX/data/parlay-settings.json" || return 1
  local stray
  for stray in "$SANDBOX/exchange/chat-history.jsonl" \
               "$SANDBOX/exchange/parlay-settings.json" \
               "$SANDBOX/exchange/chat-draft.txt" \
               "$SANDBOX/pai/MEMORY/STATE/parlay-agents.json" \
               "$SANDBOX/pai/MEMORY/STATE/parlay-session-channels.json"; do
    if [ -e "$stray" ]; then echo "  unexpected write outside \$PARLAY_DATA_DIR: $stray" >&2; return 1; fi
  done
  return 0
}
run_check "server persisted only into \$PARLAY_DATA_DIR" persisted_only_in_data_dir

cat <<'EOF'

  LIMITS of this run — what the PASSes above do not say:

  UNCOVERED  The --agent reply path. Every message above is sent with `parlay
             send` (POST /api/chat/send); POST /api/chat/reply is never called,
             and no check here says anything about it. That path resolves
             ~/.parlay/agents/<id>/context.json from the SERVER process's own
             $HOME, so `parlay say --agent <id>` can succeed while the message
             is filed on the global thread instead of that agent's tab. See
             "The layout" in examples/README.md.

  EXPOSED    While the server above was up it was bound to every interface with
             no authentication, so anyone who could reach that port could read
             this sandbox's history and post as any agent. Seeded fixtures, a
             kernel-picked high port and a few seconds bound the damage — they
             do not make the port private. On an untrusted network, treat that
             window as real.
EOF

if [ "$fail" = "0" ]; then
  printf '\n\033[32mall checks passed\033[0m — port %s, sandbox %s\n' "$PORT" "$SANDBOX"
else
  printf '\n\033[31msome checks failed\033[0m — see above\n' >&2
  exit 1
fi
