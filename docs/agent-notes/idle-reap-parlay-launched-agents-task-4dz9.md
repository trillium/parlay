# Idle reaper for Parlay-launched agents (task-4dz9)

Two systems spawn agents in this fleet. Firstmate tracks lifecycle and
auto-closes idle panes at 2h. Parlay-spawned agents (`parlay spawn` /
`bin/parlay-spawn`, and ticket auto-claim via `parlay claim`) got a registry
row and nothing else — once their listener stopped polling, nothing had
authority to close them, and they accumulated forever (root-caused in
task-4dz9's 2026-08-05 investigation; design discussed in GitHub discussion
#232, whose two diagrams are the shape this implements). This closes that gap
with a second, independent reaper scoped to exactly the agents Parlay itself
launched.

## Launch records: extend the registry row, no new store

`AgentInfo` (`packages/server/src/types.ts`) gained two optional fields,
set once by `POST /api/chat/register-agent`
(`packages/server/src/router-messages.ts`):

- `launchedBy` — `"parlay-spawn"` or `"parlay-claim"`, stamped by the two
  Parlay spawn paths (`bin/parlay-spawn`'s register-agent curl call;
  `claimEnroll` in `tools/cli/internal/commands/claim.go`). **Sticky once
  set** — a later plain re-register never un-marks a Parlay-launched agent.
  Absent entirely for a firstmate-spawned or hand-registered agent — that
  absence, not a boolean flag, is what keeps this reaper out of firstmate's
  lane. Only the `/register-agent` handler sets it; the `/reply` auto-register
  path does not, because a reply is not a launch event.
- `startedAt` — ISO8601, stamped **once**, first registration wins. A later
  re-register (a listener re-arming after a restart) never moves it, so it
  stays the true launch time. (Not currently read by the reap decision itself
  — liveness comes from poll freshness, below — but it is the launch record
  discussion #232 asked for, and the natural place to look when auditing how
  long an agent has actually been running.)

This is the discussion's own leaning ("extend the existing registry row with
endpoint + started-at, no new store") and there was no reason found in the
code to deviate from it: `AgentInfo` is already the one place both spawn paths
and the reap sweep can agree on.

## Idle tracking: the SAME liveness signal the relay already reads

The reaper adds no new tracking. `lastPollByChannel`
(`packages/server/src/sse.ts`) is the in-memory freshness map every inbound
`/api/chat/poll` already updates, and the one the existing hourly
channel-prune sweep (`packages/server/src/prune/sweep.ts`,
`packages/server/src/prune/policy.ts`) already reads for its own,
longer-timescale idle rule (`IDLE_PRUNE_MS`, 7 days — phantom/test-channel
cleanup, a different concept at a different timescale from this one).

Threshold: default 2h (`DEFAULT_IDLE_REAP_MS`,
`packages/server/src/prune/idle-reap.ts`), mirroring firstmate's own
staleness auto-close. Configurable via `PARLAY_AGENT_IDLE_TIMEOUT_MS`
(milliseconds — see `examples/env.example`). This deviates from discussion
#232's bare `PARLAY_AGENT_IDLE_TIMEOUT` spelling in favor of the `*_MS` suffix
convention every other timing constant in this module already uses
(`IDLE_PRUNE_MS`, `PERIODIC_SWEEP_MS`, `LISTEN_WINDOW_MS`) — consistency with
neighboring code over matching the discussion's placeholder name. A missing,
non-numeric, or non-positive override falls back to the default rather than
producing a shorter-than-intended window from a typo.

## The reap decision: `shouldIdleReap`

Pure predicate, mirroring `shouldPrune`'s own shape (`policy.ts`) — every
guard fails toward "leave it alone", because a wrongly-reaped agent destroys a
live session while a wrongly-kept one just waits one more sweep:

1. No `launchedBy` → never reaped. This is the firstmate boundary: a
   firstmate-spawned agent carries no launch record, by construction, so it
   is never even a candidate — this reaper does not "own" it and cannot
   double-manage it.
2. No liveness data yet (`lastPollByChannel` has no entry — never polled, or
   the server restarted since) → kept. Same rule the startup channel-prune
   sweep uses for the identical reason: "never seen" must never mean "idle".
3. Idle time < threshold → kept.
4. Idle time ≥ threshold → reaped.

`idleReapSweep()` runs this over every registry entry and calls
`unregisterAgent()` on each reap-eligible id. Wired periodic-only
(`startIdleReapSweeps()`, `packages/server/src/index.ts`), never at startup —
right after a restart `lastPollByChannel` is empty, so every agent would read
as "no liveness data" and be skipped anyway, and the first tick after a
restart would otherwise judge months-old agents on freshness accumulated in
under an hour.

## Teardown: the #234 primitive, not a second path

`idleReapSweep` calls `unregisterAgent` (`packages/server/src/prune/sweep.ts`)
directly — the exact function `POST /api/chat/unregister` calls, i.e. what
`parlay shutdown <id>` invokes server-side
([task-35ww](graceful-agent-shutdown-task-35ww.md)). No second teardown path
was written.

One deliberate difference from the CLI verb: `parlay shutdown` also runs
`monitor.KillLocalListeners`, a host-local `ps`-based kill of the listener
process. The server has no such access — it can only act on the registry. It
doesn't need to: `unregisterAgent`'s `resolvePollWaiters(id, {gone: true})`
immediately resolves the channel's in-flight long-poll, and both the relay's
`pollLoop` (`tools/relay/relay_poll.go`) and the CLI's own monitor loop
already treat that `Gone` signal as terminal — drop the loop, tombstone the
spool, exit. So the remote listener process ends itself within one poll
cycle without the server ever touching its process table.

## Discussion #232's third question: a "parked on a human" exemption

No new signal was added, and none was found to reuse. The server has no
visibility into an agent's local `~/.parlay/agents/<id>/status` file — the
thing that would say "paused" or "needs-decision" — at all.

The reasoning for why this is still safe: an agent genuinely parked waiting on
a human still has to keep its `parlay listen` process running and actually
long-polling in order to ever receive the reply it's waiting for. That poll
activity is exactly what keeps `lastPollByChannel` fresh. So "idle by poll
freshness" and "dead" are the same condition for a Parlay-launched agent by
construction — a real wait never produces a stale poll timestamp, only a
crashed, killed, or abandoned listener does. No dedicated "waiting on human"
status signal exists anywhere the server can see, and inventing one (a new
field an agent must remember to set, that this reaper would then have to
trust) was rejected as unnecessary complexity for a case the existing
liveness signal already covers correctly.

## Tests

`packages/server/src/prune/idle-reap.test.ts`:

- `shouldIdleReap` unit tests for all four guard branches above, including
  both `launchedBy` values (`parlay-spawn`, `parlay-claim`).
- `idleReapThresholdMs()` env parsing: unset/valid/non-numeric/non-positive.
- `idleReapSweep()` integration tests against the real `agents` /
  `lastPollByChannel` maps: a Parlay-launched idle agent is actually removed
  (registry entry gone, freshness entry gone) via the real `unregisterAgent`
  call; a firstmate-spawned agent is untouched however idle; an active
  Parlay-launched agent is kept; a Parlay-launched agent with no liveness data
  yet is kept; a second sweep after a reap is a no-op for that id (proving
  idempotency end to end, not just relying on `unregisterAgent`'s own
  idempotent-404 behavior at the HTTP layer).

## Scope boundary

TS server only (`packages/server`), matching the precedent this module's
sibling (`policy.ts`/`sweep.ts`) and task-35ww already set — `packages/go-server`
was not given parity here.
