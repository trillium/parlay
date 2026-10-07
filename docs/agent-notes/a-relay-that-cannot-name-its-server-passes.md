# A relay that cannot name its server passes the cross-instance preflight

`parlay listen`, `parlay monitor` and `parlay claim` are supposed to refuse to
enroll into a relay bound to a *different* chat server before they register
anything (`tools/monitor/parlay-monitor.sh`, the "relay polls ONE server" guard;
`tools/cli/internal/monitor/monitor.go` passes `PARLAY_SERVER=config.ServerURL()`
into the script, so the guard always has the CLI's resolved server to compare).

The comparison needs the other half from the relay: `GET /health` over
`relay.sock` must carry `"server"`. `tools/relay/relay_control.go` emits it and
`relay_control_test.go` pins it, but the guard is deliberately tolerant — an
absent or unparseable answer is *not* a mismatch, because refusing on unknown
would break every relay older than the field. So on a host whose relay predates
it, the guard silently passes:

```
$ curl -s --unix-socket "$TMPDIR/parlay/relay.sock" http://relay/health
{"ok":true}
$ parlay monitor --agent demo          # CLI resolved 127.0.0.1:4242
parlay-monitor: preflight OK — canonical relay is up for 'demo'
parlay-monitor: enrolling 'demo' via /var/folders/…/T/parlay/relay.sock
parlay-monitor: streaming 'demo' from /var/folders/…/T/parlay/demo.chan
```

Note the missing `and polling <url>` suffix: the script appends it only when the
relay reported its upstream, so that suffix *is* the check's result. This
transcript came from a host whose canonical relay is a September build started
with `-server http://macbook:31337`, while the CLI was pointed at a fresh
`127.0.0.1:4242` server — the enroll looked healthy, the spool lived in the
shared runtime dir, and the relay's own upstream was refusing connections. An
agent enrolled this way is registered but deaf, with nothing on screen saying so.

Consequence for a host that already runs a relay (any machine with parlay
installed): rebuilding/reinstalling nothing means the refusal cannot fire, so an
isolated instance must not rely on it. Use `--legacy-poll`, or give the instance
its own `PARLAY_RELAY_RUNTIME` and a relay started with `-server $PARLAY_SERVER`.
The stale-relay half is fixed by running the current relay build
(`tools/relay/build.sh`; `com.parlay.relay` supervises it).
