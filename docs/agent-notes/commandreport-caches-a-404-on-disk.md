# commandreport caches "server lacks the command registry" on disk

**The rule:** if a `parlay` verb's invocation is missing from `parlay commands` / the chat panel and you're mid-debugging, check for `$PARLAY_STATE_HOME/command-report-unsupported` (default `~/.parlay/command-report-unsupported`) before suspecting the reporting pipeline. Delete it to force a fresh probe.

## Why it exists

`internal/commandreport.Begin` POSTs `/api/chat/command-start` before every reporting verb. Against a server without the command-registry routes (the Bun server; only `packages/go-server` has them), that request is doomed — and it was the trigger in the register-agent 400 investigation (robots-tjx5): a doomed pre-verb request sharing `http.DefaultTransport`'s keep-alive pool with the verb's real requests. The in-process `disabled` flag couldn't help because every CLI invocation is a new process, so the fleet paid one doomed 400ms request per invocation, forever.

So a **real 404 answer** (never a network failure, never a 5xx) is now remembered across processes in `command-report-unsupported`. The marker's content is the server URL it was learned from, and it expires after one hour (`unsupportedCacheTTL`), so:

- a marker for one server never silences reporting to another (sandboxes with their own `PARLAY_SERVER` are unaffected by the captain's marker and vice versa — but only because the URL is compared; keep it that way);
- a server upgrade is picked up within an hour without anyone deleting anything.

## The trap for test/debug sessions

Bring up a dev instance of the go-server, run a verb against it, and the verb reports fine. But if an earlier verb in the same `$PARLAY_STATE_HOME` hit a Bun server **at the same URL** within the last hour (common when a sandbox reuses a port), the marker matches and every reporting verb silently skips its start report — which looks exactly like the registry being broken. Tests are immune only because they set `PARLAY_STATE_HOME` to a temp dir (`testsupport.TempStateHome`); ad-hoc shells are not.

Related: the same change gave commandreport's client its own `http.Transport` with keep-alives disabled, so telemetry and verb traffic can no longer share pooled connections. Do not "simplify" it back onto the default transport.
