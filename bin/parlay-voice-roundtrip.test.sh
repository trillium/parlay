#!/usr/bin/env bash
# Non-voice round trip for the herdr web page: the synthetic equivalent of saying
# a dictated line that ends in "send it". It builds the real Go server and the
# real eval engine (one `parlay` binary), runs them in a throwaway sandbox on
# kernel-chosen loopback ports, and then does exactly what the herdr page does:
#
#   1. open GET /api/chat/events?device=<id> with the page's Origin and assert
#      the Origin is echoed in Access-Control-Allow-Origin (no ACAO = the page
#      is deaf: the input_action reply never reaches it);
#   2. preflight POST /api/chat/eval for that Origin;
#   3. POST /api/chat/eval with platform:"herdr" (what the page always sends)
#      for a plain line, then for the same line ending "send it";
#   4. assert the server-owned fire arrives back ON THE SSE STREAM as a
#      submitNow input_action (engine -> /api/chat/eval-push -> broadcast).
#
# The one thing it cannot do is speak: speech-to-text on the phone/browser is the
# captain's PASS step in runbook runbooks-v84. Everything after the text exists
# is covered here. No live server, ~/.parlay or the host-wide :4343 engine slot
# is touched. Usage: bin/parlay-voice-roundtrip.test.sh
set -uo pipefail

__pre_sys="/usr/bin:/bin:/usr/sbin:/sbin"
export PATH="${__pre_sys}:${PATH}"
unset __pre_sys BASH_ENV ENV PROMPT_COMMAND 2>/dev/null || true
for __f in ${!BASH_FUNC_*}; do unset "$__f" 2>/dev/null || true; done
for __v in $(compgen -e | grep '^PARLAY_' || true); do unset "$__v"; done

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
for tool in go curl python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 2; }
done

pass=0; fail=0
ok()  { pass=$((pass + 1)); echo "  ok   - $1"; }
bad() { fail=$((fail + 1)); echo "  FAIL - $1"; [ -n "${2:-}" ] && echo "         $2"; return 0; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-voice-roundtrip-test.XXXXXX")" || exit 2
PIDS=""
cleanup() {
  for p in $PIDS; do kill "$p" 2>/dev/null; done
  case "$ROOT" in */parlay-voice-roundtrip-test.*) rm -rf "$ROOT" ;; esac
}
trap cleanup EXIT

free_port() { python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }

mkdir -p "$ROOT/home" "$ROOT/state" "$ROOT/pai" "$ROOT/bin"

# Build BEFORE redirecting HOME: GOPATH/GOMODCACHE live under the real HOME, and a
# module cache created inside the sandbox is read-only and defeats the cleanup.
(cd "$REPO/packages/go-server" && go build -o "$ROOT/bin/parlay-server" ./cmd/parlay-server) \
  || { echo "go-server build failed" >&2; exit 2; }
(cd "$REPO/tools/cli" && CGO_ENABLED=0 go build -o "$ROOT/bin/parlay" .) \
  || { echo "cli build failed" >&2; exit 2; }

export HOME="$ROOT/home" PARLAY_STATE_HOME="$ROOT/state" PARLAY_AGENT_HOME="$ROOT/agents" PAI_DIR="$ROOT/pai"
export PARLAY_HUB_URL="http://127.0.0.1:9"   # closed port: nothing replays into a real hub


SP="$(free_port)"; EP="$(free_port)"
BASE="http://127.0.0.1:$SP"
# The herdr page is served from <host>:8787; this is the tailnet origin it really has.
ORIGIN="http://100.74.138.74:8787"

"$ROOT/bin/parlay" eval serve --addr "127.0.0.1:$EP" --push-url "$BASE/api/chat/eval-push" >"$ROOT/engine.log" 2>&1 &
PIDS="$PIDS $!"
PARLAY_EVAL_ENGINE_URL="http://127.0.0.1:$EP" "$ROOT/bin/parlay-server" -addr "127.0.0.1:$SP" \
  -state-dir "$ROOT/state" -pai-dir "$ROOT/pai" -assets-dir "$ROOT/none" >"$ROOT/server.log" 2>&1 &
PIDS="$PIDS $!"

up=0
for _ in $(seq 1 50); do
  if curl -sf -m1 "http://127.0.0.1:$EP/health" >/dev/null && curl -sf -m1 "$BASE/api/chat/version" >/dev/null; then up=1; break; fi
  sleep 0.2
done
[ "$up" = 1 ] || { echo "sandbox did not come up"; cat "$ROOT/engine.log" "$ROOT/server.log"; exit 2; }

# 1. connect, as the page does.
curl -sN -m6 -H "Origin: $ORIGIN" -D "$ROOT/sse.hdr" "$BASE/api/chat/events?device=rt" >"$ROOT/sse.out" 2>/dev/null &
PIDS="$PIDS $!"
sleep 1
grep -qi "^access-control-allow-origin: $ORIGIN" "$ROOT/sse.hdr" \
  && ok "SSE stream echoes the herdr page origin (page can read replies)" \
  || bad "SSE stream does not echo the herdr page origin" "$(cat "$ROOT/sse.hdr")"
grep -q "^event: connected" "$ROOT/sse.out" && ok "SSE stream delivers events" || bad "SSE stream delivered nothing"

# 2. preflight.
code="$(curl -s -m4 -o /dev/null -w '%{http_code}' -X OPTIONS -H "Origin: $ORIGIN" \
  -H 'Access-Control-Request-Method: POST' -H 'Access-Control-Request-Headers: content-type' "$BASE/api/chat/eval")"
[ "$code" = 204 ] && ok "eval preflight for the page origin is 204" || bad "eval preflight returned $code, want 204"

# 3. dictate: a plain line (version 1), then the same line ending in the ender (version 2).
post_eval() { # version text
  local n=${#2}
  curl -s -m5 -H "Origin: $ORIGIN" -H 'content-type: application/json' \
    -d "{\"device\":\"rt\",\"version\":$1,\"platform\":\"herdr\",\"voiceEnabled\":true,\"text\":\"$2\",\"cursor\":{\"anchor\":$n,\"active\":$n}}" \
    "$BASE/api/chat/eval"
}
r1="$(post_eval 1 'hello world')"
echo "$r1" | grep -q '"ok":true' && ok "plain line evaluated (engine reachable through the server)" || bad "plain eval failed" "$r1"
r2="$(post_eval 2 'hello world send it')"
echo "$r2" | grep -q '"timerId":"submit"' && ok "ender arms the 1s submit timer" || bad "ender did not arm the submit timer" "$r2"

# 4. the fire comes back on the stream.
fired=0
for _ in $(seq 1 25); do
  grep -q '"verb":"submitNow"' "$ROOT/sse.out" && { fired=1; break; }
  sleep 0.2
done
[ "$fired" = 1 ] && ok "server-owned submitNow arrives on the SSE stream within 5s" \
  || bad "no submitNow on the SSE stream" "$(cat "$ROOT/sse.out")"
grep -q '"serverOwnedFire":true' "$ROOT/sse.out" && ok "fire is marked serverOwnedFire" || bad "fire not marked serverOwnedFire"

echo "$pass passed, $fail failed"
[ "$fail" = 0 ]
