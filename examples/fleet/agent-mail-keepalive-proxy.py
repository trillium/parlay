"""Keep-alive fix proxy for the agent-mail pilot backend (task-3b3oh).

Known interop gap (see agent-mail-pilot.md section "Known interop gap"):
the fork's HTTP stack corrupts responses sent over a REUSED keep-alive
connection (uvicorn ``RuntimeError: Response content longer than
Content-Length``), so Jungle's Go downstream client sees
``connection reset by peer`` on tools/call. A fresh connection per
request always succeeds.

This proxy applies exactly that: it accepts keep-alive connections from
Jungle on :18766 and forwards each request to the backend on :18765 over
a FRESH upstream connection (``Connection: close``), re-framing the
response with a correct Content-Length before returning it. The backend
itself is untouched.

Run (host-local, reversible):
    nohup python3 agent-mail-keepalive-proxy.py \
      > "$MAIL_HOME/keepalive-proxy.log" 2>&1 &
Teardown (reverse):
    kill <proxy pid>   # then re-register Jungle straight at :18765/mcp
Jungle registration (via the proxy):
    mcpjungle register --name agent-mail-pilot \
      --url http://127.0.0.1:18766/mcp \
      --description "Reversible pilot: mcp_agent_mail fork (task-2qo8v)." \
      --registry http://100.74.138.74:8338   # tailnet; loopback :8338 refuses
"""

import http.server
import os
import sys
import urllib.request

LISTEN_HOST = os.environ.get("PROXY_HOST", "127.0.0.1")
LISTEN_PORT = int(os.environ.get("PROXY_PORT", "18766"))
TARGET = os.environ.get("PROXY_TARGET", "http://127.0.0.1:18765")

# Hop-by-hop headers are never forwarded; framing is always recomputed.
SKIP_REQ = {"host", "content-length", "connection", "transfer-encoding",
            "keep-alive", "proxy-connection", "upgrade"}
SKIP_RESP = {"transfer-encoding", "content-length", "connection",
             "keep-alive", "proxy-connection"}


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"  # safe downstream keep-alive: we frame exactly
    server_version = "MailKeepaliveProxy/1.0"

    def _forward(self):
        length = int(self.headers.get("Content-Length", 0) or 0)
        body = self.rfile.read(length) if length else None
        out_headers = {k: v for k, v in self.headers.items()
                       if k.lower() not in SKIP_REQ}
        # Fresh upstream connection per request: the documented keep-alive fix.
        out_headers["Connection"] = "close"
        req = urllib.request.Request(TARGET + self.path, data=body,
                                     headers=out_headers, method=self.command)
        try:
            with urllib.request.urlopen(req, timeout=60) as upstream:
                resp_body = upstream.read()
                self.send_response(upstream.status)
                for k, v in upstream.headers.items():
                    if k.lower() not in SKIP_RESP:
                        self.send_header(k, v)
                self.send_header("Content-Length", str(len(resp_body)))
                self.end_headers()
                self.wfile.write(resp_body)
        except urllib.error.HTTPError as e:
            try:
                err_body = e.read()
            except Exception:
                err_body = b""
            self.send_response(e.code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(err_body)))
            self.end_headers()
            self.wfile.write(err_body)
        except Exception as e:  # backend unreachable etc.
            err_body = ("proxy upstream error: %s" % e).encode()
            self.send_response(502)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(err_body)))
            self.end_headers()
            self.wfile.write(err_body)

    do_GET = _forward
    do_POST = _forward
    do_DELETE = _forward

    def log_message(self, fmt, *args):
        sys.stderr.write("%s - %s\n" % (self.address_string(), fmt % args))
        sys.stderr.flush()


if __name__ == "__main__":
    server = http.server.ThreadingHTTPServer(
        (LISTEN_HOST, LISTEN_PORT), Handler)
    server.daemon_threads = True
    print("keepalive proxy: %s:%d -> %s (fresh upstream conn per request)"
          % (LISTEN_HOST, LISTEN_PORT, TARGET), flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
