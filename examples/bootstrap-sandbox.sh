#!/usr/bin/env bash
# bootstrap-sandbox.sh — instantiate this example into a throwaway sandbox and
# prove it works, without touching your real ~/.parlay or any running parlay
# server.
#
# It copies examples/parlay-state → $SANDBOX/.parlay and examples/data-dir →
# $SANDBOX/data, builds the Go CLI and the Go server, starts that server on a
# free port with $HOME redirected into the sandbox, then exercises the CLI
# against it.
#
# Usage:
#   examples/bootstrap-sandbox.sh              # run, report, clean up
#   examples/bootstrap-sandbox.sh --keep       # leave the sandbox on disk
#   examples/bootstrap-sandbox.sh --port 45999 # pin the port instead of picking one
#
# Requirements: go, curl. Run it from anywhere; it locates the repo itself.

set -euo pipefail

KEEP=0
PORT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --keep)  KEEP=1; shift ;;
    --port)  PORT="${2:?--port needs a value}"; shift 2 ;;
    -h|--help) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

EXAMPLES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$EXAMPLES_DIR/.." && pwd)"

for tool in go curl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 1; }
done

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

# Port helper, in Go — the same two answers this script needs from the network
# stack, with no dependency beyond the Go toolchain the rest of the run already
# requires. `go run` on a single stdlib-only file needs no module, so this does
# not depend on which of the repo's four Go modules the cwd happens to be in.
# (The previous version of this script used `bun -e` for both, which put a
# hard Bun requirement on a script the README otherwise describes as Go-only.)
cat > "$SANDBOX/porttool.go" <<'GOEOF'
package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

func main() {
	switch os.Args[1] {
	case "pick":
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintln(os.Stderr, "pick:", err)
			os.Exit(1)
		}
		defer l.Close()
		fmt.Println(l.Addr().(*net.TCPAddr).Port)
	case "check":
		port, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "check: bad port:", err)
			os.Exit(2)
		}
		// Exit 0 when the port is FREE (nothing is listening), 1 when it is
		// held, 2 when the probe itself could not run. The last one must not
		// be collapsed into "free" or "held": the caller would then either
		// adopt somebody else's server or refuse a port nobody owns.
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			l.Close()
			os.Exit(0)
		}
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, "usage: porttool pick | porttool check <port>")
		os.Exit(2)
	}
}
GOEOF
porttool() { (cd "$SANDBOX" && go run ./porttool.go "$@"); }

# An unused high port. --port overrides; otherwise the kernel picks one for us.
if [ -z "$PORT" ]; then
  PORT="$(porttool pick)" || { echo "could not pick a free port" >&2; exit 1; }
fi
BASE="http://127.0.0.1:$PORT"

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
SEEDED_HISTORY_LINES="$(wc -l < "$SANDBOX/data/messages.jsonl" | tr -d ' ')"

# The one placeholder a reader has to replace. Here: point it at the sandbox.
find "$SANDBOX/.parlay/agents" -name identity.md -print0 |
  xargs -0 sed -i.bak "s#/path/to/your/project-worktrees#$SANDBOX/code/worktrees#g; s#/path/to/your/project#$SANDBOX/code/example-project#g"
find "$SANDBOX/.parlay/agents" -name '*.bak' -delete

# The example ships a localhost:4242 default; retarget it at this run's port.
printf '{\n  "server": "%s"\n}\n' "$BASE" > "$SANDBOX/.parlay/config.json"

# ── 2. Build the CLI and the server ──────────────────────────────────────────
# CGO_ENABLED=0 for the CLI: the beads dependency's embedded-Dolt tree drags in
# ICU C++ headers under default CGO on macOS, which bricks the build. Nothing in
# the CLI needs cgo, and `bin/parlay` sets it for the same reason.
say "building the parlay CLI (tools/cli)"
(cd "$REPO/tools/cli" && CGO_ENABLED=0 go build -o "$SANDBOX/bin/parlay" .)

say "building the parlay server (packages/go-server)"
(cd "$REPO/packages/go-server" && CGO_ENABLED=0 go build -o "$SANDBOX/bin/parlay-server" ./cmd/parlay-server)

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
say "starting packages/go-server on $BASE"
# Refuse a port somebody else already holds. Everything below both reads and
# WRITES over $BASE, so adopting a foreign server would mutate its data — the
# one thing this script promises not to do.
if porttool check "$PORT"; then
  :
else
  # 1 means held, anything else means the probe did not run. Collapsing the
  # second into the first would refuse a port nobody owns; collapsing it into
  # "free" would adopt somebody else's server.
  rc=$?
  if [ "$rc" = 1 ]; then
    echo "port $PORT is already in use; refusing to run against a server this script did not start" >&2
  else
    echo "could not probe port $PORT (porttool check exited $rc); refusing to guess" >&2
  fi
  exit 1
fi
# `exec` is load-bearing: without it the subshell is a real intermediate process
# on stock macOS bash 3.2, $! records IT rather than the server, and the kill in
# cleanup() leaves an orphaned server holding this port while $SANDBOX is
# removed underneath it. exec replaces the subshell, so $! is always the server.
#
# Every path the server can write is pinned into the sandbox: -state-dir holds
# messages/registry/draft/settings/uploads, -pai-dir the TTS cache, and
# -assets-dir is pointed at an empty directory so a real
# packages/client/dist next to the repo is never served (or read) here.
(
  exec env HOME="$SANDBOX" \
    "$SANDBOX/bin/parlay-server" \
      -addr "127.0.0.1:$PORT" \
      -state-dir "$SANDBOX/data" \
      -pai-dir "$SANDBOX/pai" \
      -assets-dir "$SANDBOX/assets"
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

say "parlay say --agent helm — the agent-role reply path (POST /api/chat/reply)"
parlay say --agent helm "replying from the bootstrap sandbox"

say "parlay identity --agent helm — the durable self-knowledge (frontmatter stripped)"
parlay identity --agent helm

say "parlay launch — known agents from the sandbox's ~/.parlay/agents"
parlay launch

say "PUT /api/chat/parlay/settings — settings persist into the server state dir"
curl -fsS -m 5 -X PUT -H 'Content-Type: application/json' \
  -d '{"textScale":123}' "$BASE/api/chat/parlay/settings" >/dev/null

say "parlay doctor — self-diagnosis for PARLAY_AGENT_ID=helm"
# Captured so the checks below can assert which lines PASSed. WARNs are expected
# (no monitor is armed, no eval engine), so a non-zero exit is not a failure here.
# PARLAY_EVAL_ENGINE_URL is pinned to a port nothing can be listening on
# (binding :1 needs root): without it doctor probes the hardcoded :4343 and,
# on a box running a real eval engine there, reports PASS off an engine that
# has nothing to do with this sandbox. The WARN this forces is the honest
# answer — the sandbox provides no eval engine.
env -u PARLAY_SERVER HOME="$SANDBOX" PARLAY_STATE_HOME="$SANDBOX/.parlay" \
  PARLAY_AGENT_HOME="$SANDBOX/.parlay/agents" PARLAY_AGENT_ID=helm \
  PARLAY_EVAL_ENGINE_URL="http://127.0.0.1:1" \
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

message_persisted() { grep -q "hello from bootstrap-sandbox.sh" "$SANDBOX/data/messages.jsonl"; }
run_check "message persisted to the state dir's messages.jsonl" message_persisted

# The --agent reply path, which send never touches. The server files a reply on
# the `agent` field of the request, so this asserts both that the route works and
# that the id actually routed the message to that agent's channel rather than
# landing on the global thread.
reply_routed_to_the_agent_channel() {
  local out; out="$(parlay history 20 --full)" || return 1
  # --full prints the id/channel header on one line and the text on the next,
  # so the text line must FOLLOW a helm header rather than merely share a line.
  printf '%s\n' "$out" | grep -A1 'channel=helm' | grep -q 'replying from the bootstrap sandbox' || return 1
}
run_check "reply path routed --agent helm onto the helm channel" reply_routed_to_the_agent_channel

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

# [ghost] is the truthful state here: both agents are registered but nothing in
# this sandbox arms a listener (the relay is deliberately out of scope — see
# "Not verified" in examples/README.md). Liveness is registry ∩ process table
# since robots-jkwc, so asserting [live] would require the display lie back.
launch_specs_for_both() {
  local out; out="$(parlay launch)" || return 1
  printf '%s\n' "$out" | grep -q '^[[:space:]]*helm .*\[ghost\]' || return 1
  printf '%s\n' "$out" | grep -q '^[[:space:]]*reviewer .*\[ghost\]' || return 1
}
run_check "launch spec discovered for both agents, both reported ghost (registered, no listener)" launch_specs_for_both

# The four seeded messages.jsonl lines are loaded by the server and served
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

# The server's persisted WRITES must land in the state dir it was given and
# nowhere else. Both halves have to prove a write: the example's own files are
# copied into $SANDBOX/data before boot, so merely existing there proves nothing.
# History must have grown past its seeded lines (the send above), and settings
# must carry the value the PUT above sent. "Nowhere else" is checked at the
# paths a server that ignored -state-dir would use instead — $HOME/.parlay, the
# CLI's own state root and the one the default is — plus the $PAI_DIR tree, all
# inside the sandbox for this run.
persisted_only_in_state_dir() {
  local now
  now="$(wc -l < "$SANDBOX/data/messages.jsonl" | tr -d ' ')" || return 1
  [ "$now" -gt "$SEEDED_HISTORY_LINES" ] || return 1
  grep -q '"textScale": *123' "$SANDBOX/data/settings.json" || return 1
  local stray
  for stray in "$SANDBOX/.parlay/messages.jsonl" \
               "$SANDBOX/.parlay/agents.json" \
               "$SANDBOX/.parlay/settings.json" \
               "$SANDBOX/.parlay/draft.json"; do
    if [ -e "$stray" ]; then echo "  unexpected write outside the state dir: $stray" >&2; return 1; fi
  done
  # And nothing at all under -pai-dir: this run never exercises TTS, so a file
  # there means the server ignored the flag and wrote into the default PAI tree.
  if [ -n "$(find "$SANDBOX/pai" -type f -print -quit 2>/dev/null)" ]; then
    echo "  unexpected write under the -pai-dir:" >&2
    find "$SANDBOX/pai" -type f >&2
    return 1
  fi
  return 0
}
run_check "server persisted only into the state dir it was given" persisted_only_in_state_dir

cat <<'EOF'

  LIMITS of this run — what the PASSes above do not say:

  UNCOVERED  Delivery, and everything behind it. A message that lands on a
             channel is not a message an agent received: nothing here arms a
             listener, so `parlay listen` / `parlay monitor` and the relay they
             enroll through are untested. The relay is a per-runtime-dir
             singleton on the host, so standing one up from a sandbox is the
             cross-talk this example exists to avoid — that is the whole
             reason the agents above read [ghost].

  BOUND      While the server above was up, anything on this machine could
             reach its port and — there is no authentication — read this
             sandbox's history and post as any agent. It is bound to
             127.0.0.1, so nothing off-host could, but that is this script's
             -addr, not a property of the server: it has no authentication, and
             a real instance reaches further. Seeded fixtures, a kernel-picked
             high port and a few seconds bound the damage on this box. On an
             untrusted network, treat that window as real.
EOF

if [ "$fail" = "0" ]; then
  printf '\n\033[32mall checks passed\033[0m — port %s, sandbox %s\n' "$PORT" "$SANDBOX"
else
  printf '\n\033[31msome checks failed\033[0m — see above\n' >&2
  exit 1
fi
