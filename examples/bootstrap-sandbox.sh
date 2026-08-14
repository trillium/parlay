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
    -h|--help) sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
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
(
  cd "$REPO/packages/server"
  env HOME="$SANDBOX" \
    PARLAY_DATA_DIR="$SANDBOX/data" \
    PAI_DIR="$SANDBOX/pai" \
    PARLAY_PORT="$PORT" \
    bun src/index.ts
) > "$SANDBOX/server.log" 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 40); do
  curl -fsS -m 1 "$BASE/api/chat/agents" >/dev/null 2>&1 && break
  sleep 0.25
done
curl -fsS -m 2 "$BASE/api/chat/agents" >/dev/null || {
  echo "server did not come up; log follows:" >&2; cat "$SANDBOX/server.log" >&2; exit 1
}
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

say "parlay doctor — self-diagnosis for PARLAY_AGENT_ID=helm"
env -u PARLAY_SERVER HOME="$SANDBOX" PARLAY_STATE_HOME="$SANDBOX/.parlay" \
  PARLAY_AGENT_HOME="$SANDBOX/.parlay/agents" PARLAY_AGENT_ID=helm \
  "$SANDBOX/bin/parlay" doctor || true   # WARNs are expected: no monitor is armed

# ── 5. Assert ────────────────────────────────────────────────────────────────
say "checks"
fail=0
check() { if [ "$2" = "ok" ]; then echo "  PASS  $1"; else echo "  FAIL  $1"; fail=1; fi; }

parlay agents | grep -q '^helm ' && check "registry served the seeded agents" ok || check "registry served the seeded agents" no
parlay history 5 --full | grep -q "hello from bootstrap-sandbox.sh" &&
  check "message round-tripped through the server" ok || check "message round-tripped through the server" no
grep -q "hello from bootstrap-sandbox.sh" "$SANDBOX/data/chat-history.jsonl" &&
  check "message persisted to \$PARLAY_DATA_DIR/chat-history.jsonl" ok ||
  check "message persisted to \$PARLAY_DATA_DIR/chat-history.jsonl" no
parlay remote | grep -q "source: config" &&
  check "server URL resolved from config.json" ok || check "server URL resolved from config.json" no
parlay identity --agent helm | grep -q "PURPOSE" &&
  check "identity.md read back with frontmatter stripped" ok || check "identity.md read back with frontmatter stripped" no
parlay launch | grep -q "reviewer" &&
  check "launch spec discovered for both agents" ok || check "launch spec discovered for both agents" no
grep -q "Data dir *$SANDBOX/data" "$SANDBOX/server.log" &&
  check "server persisted only inside the sandbox" ok || check "server persisted only inside the sandbox" no

if [ "$fail" = "0" ]; then
  printf '\n\033[32mall checks passed\033[0m — port %s, sandbox %s\n' "$PORT" "$SANDBOX"
else
  printf '\n\033[31msome checks failed\033[0m — see above\n' >&2
  exit 1
fi
