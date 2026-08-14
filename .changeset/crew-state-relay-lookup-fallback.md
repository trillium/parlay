---
"@parlay/cli": patch
---

Fix `parlay crew-state` intermittently reporting a live, registered agent as `unknown · source: none · agent not enrolled with relay` (robots-me7m).

`isAgentSubscribed()` (in both `packages/cli/src/commands-crew-state.ts` and its Go port `tools/cli/internal/commands/crew_state.go`) collapsed a *failed* `/api/chat/subscribers` lookup — 3s timeout, non-2xx, undecodable body — into "not subscribed". Under relay contention (43 concurrent `parlay listen`/`parlay monitor` processes on the box when this was observed) a single hiccup made crew-state declare a healthy agent unenrolled, even though `parlay agents` listed it and its status file was populated and unchanged. Retrying the identical command seconds later returned the correct state. Because crew-state is the supervision oracle, this is a false-dead: a supervisor polling it can start recovery or teardown against healthy work, and the transient `unknown` also *masks* whatever the agent actually was at that poll (a `blocked`/`needs-decision` landing on that exact poll is swallowed and never seen again).

What changed:

- **A failed lookup is no longer an answer.** The relay check now returns enrolled / not-enrolled / **lookup-failed**, retries a transient failure (3 attempts, 250ms backoff) before giving up, and only reports "not registered" when the relay actually answered.
- **The on-disk status file is always consulted and always wins over `unknown`.** When the relay is unreachable, the durable status file supplies the state (`source: status-degraded`, detail carries a staleness caveat) instead of `unknown`. Even an authoritative "not registered" keeps the last recorded verb rather than discarding it. A supervisor acting on stale-`working` is safe; one acting on false-dead is not.
- **Three conflated conditions are now distinct**, in both the message and the exit code, so a supervisor can tell "no news" from "gone" without string-matching: `0` state determined (including a stale fallback), `3` enrolled but no status recorded, `4` relay does not list the agent, `5` status present but unreadable/unparseable, `6` relay unreachable *and* no status to fall back on — the only case crew-state has no opinion. `source` gained `status-degraded` and `status-unenrolled` alongside `status` / `none`.

Regression tests on both sides cover the relay-unreachable fallback, the transient-failure retry, the not-enrolled-but-status-present case, and each new exit code.
