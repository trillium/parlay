#!/usr/bin/env bash
# Regression harness for bin/parlay-demo-rehearsal. A rehearsal that cannot fail is
# not a rehearsal, so this runs it against a stub server in four states and asserts
# the verdict: healthy -> exit 0 with every step PASS; no frame-ancestors, wildcard
# frame-ancestors, deck origin missing -> exit 1 naming step 3 with a `fix:` line;
# nothing listening -> exit 1 naming step 1. No real server or state is touched: the
# stub binds a kernel-chosen loopback port and is killed on exit.
# Usage: bin/parlay-demo-rehearsal.test.sh
set -uo pipefail

__pre_sys="/usr/bin:/bin:/usr/sbin:/sbin"
export PATH="${__pre_sys}:${PATH}"
unset __pre_sys BASH_ENV ENV PROMPT_COMMAND PARLAY_DEMO_URL PARLAY_DEMO_DECK_ORIGIN PARLAY_DEMO_DECK_URL 2>/dev/null || true
for __f in ${!BASH_FUNC_*}; do unset "$__f" 2>/dev/null || true; done
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${HERE}/parlay-demo-rehearsal"
PY="$(command -v python3)" || { echo "python3 required" >&2; exit 2; }
"$PY" -c 'pass' >/dev/null 2>&1 || { echo "python3 does not run" >&2; exit 2; }

pass=0; fail=0
ok()  { pass=$((pass + 1)); echo "  ok   - $1"; }
bad() { fail=$((fail + 1)); echo "  FAIL - $1"; [ -n "${2:-}" ] && echo "         $2"; return 0; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-demo-rehearsal-test.XXXXXX")" || exit 2
STUB_PID=""
cleanup() { [ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null; case "$ROOT" in */parlay-demo-rehearsal-test.*) rm -rf "$ROOT" ;; esac; }
trap cleanup EXIT

cat >"$ROOT/stub.py" <<'PYEOF'
import http.server, json, os, sys
CSP = os.environ.get("STUB_CSP", "")
msgs = []
PAGE = ("<html><title>Parlay talk demo</title>" + "x" * 6000 + "</html>").encode()
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def _send(self, code, body, ctype="application/json", extra=None):
        self.send_response(code); self.send_header("Content-Type", ctype)
        for k, v in (extra or {}).items(): self.send_header(k, v)
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def do_GET(self):
        if self.path == "/demo":
            self._send(200, PAGE, "text/html", {"Content-Security-Policy": CSP} if CSP else {})
        elif self.path == "/api/chat/demo/view":
            hops = [{"seq": 1, "stage": "queued", "class": "ok", "source": "demo"}] if msgs else []
            self._send(200, json.dumps({"channel": "talk-demo", "messages": msgs, "hops": hops}).encode())
        else: self._send(404, b"{}")
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0)); body = json.loads(self.rfile.read(n) or b"{}")
        if self.path == "/api/chat/demo/reset": msgs.clear(); self._send(200, b'{"ok":true}')
        elif self.path == "/api/chat/demo/trigger":
            msgs.append({"role": "user", "text": body.get("text", "")})
            self._send(200, b'{"outcome":"queued; waiting for a real agent on channel \\"talk-demo\\""}')
        elif self.path == "/api/chat/reply": msgs.append({"role": "agent", "text": body.get("text", "")}); self._send(200, b'{"ok":true}')
        else: self._send(404, b"{}")
s = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
print(s.server_address[1], flush=True)
s.serve_forever()
PYEOF

start_stub() { # $1 = STUB_CSP
  [ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null
  STUB_CSP="$1" "$PY" "$ROOT/stub.py" >"$ROOT/port" 2>/dev/null & STUB_PID=$!
  for _ in $(seq 1 50); do [ -s "$ROOT/port" ] && break; sleep 0.1; done
  PORT="$(cat "$ROOT/port")"; : >"$ROOT/port"
}
run() { PARLAY_DEMO_URL="http://127.0.0.1:$1" PARLAY_DEMO_DECK_ORIGIN="https://deck.example:4173" PARLAY_DEMO_DECK_URL="http://127.0.0.1:1/" bash "$SCRIPT" 2>&1; }

DECK="https://deck.example:4173"
start_stub "frame-ancestors 'self' $DECK"
out="$(run "$PORT")"; rc=$?
[ "$rc" = 0 ] && ok "healthy stub exits 0" || bad "healthy stub exits 0" "rc=$rc"
echo "$out" | grep -q '^FAIL' && bad "healthy stub has no FAIL line" "$out" || ok "healthy stub has no FAIL line"
echo "$out" | grep -q '^PASS  5c' && ok "fake-agent exchange reaches the live view" || bad "fake-agent exchange reaches the live view" "$out"

start_stub ""
out="$(run "$PORT")"; rc=$?
[ "$rc" = 1 ] && echo "$out" | grep -q 'FAIL  3 iframe headers: no frame-ancestors' && echo "$out" | grep -q 'fix: ' && ok "missing frame-ancestors fails step 3 with a fix" || bad "missing frame-ancestors fails step 3 with a fix" "rc=$rc $out"

start_stub "frame-ancestors 'self' *"
out="$(run "$PORT")"; rc=$?
[ "$rc" = 1 ] && echo "$out" | grep -q 'FAIL  3 iframe headers: frame-ancestors contains a wildcard' && ok "wildcard frame-ancestors fails step 3" || bad "wildcard frame-ancestors fails step 3" "rc=$rc $out"

start_stub "frame-ancestors 'self' https://other.example"
out="$(run "$PORT")"; rc=$?
[ "$rc" = 1 ] && echo "$out" | grep -q 'deck origin https://deck.example:4173 is not allowed' && ok "deck origin missing from frame-ancestors fails step 3" || bad "deck origin missing from frame-ancestors fails step 3" "rc=$rc $out"

kill "$STUB_PID" 2>/dev/null; wait "$STUB_PID" 2>/dev/null; STUB_PID=""
out="$(run "$PORT")"; rc=$?
[ "$rc" = 1 ] && echo "$out" | grep -q 'FAIL  1 server up' && echo "$out" | grep -q 'fix: ' && ok "no server fails step 1 with a fix" || bad "no server fails step 1 with a fix" "rc=$rc $out"

echo "$pass ok, $fail failed"
[ "$fail" = 0 ]
