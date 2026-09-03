# `parlay shutdown` — explicit graceful agent retirement (task-35ww)

Before this verb existed, an agent leaving had no clean way to say so: its
listener process, spool, and registry row were left to time out, get pruned
by the hourly sweep, or 410-tombstone themselves the next time something
happened to poll them ([task-0n80i](relay-resume-tombstones-retired-spools-task-0n80i.md)).
`parlay shutdown <id>` does the same teardown on demand, in one call:

1. `monitor.KillLocalListeners(id)` ends any live `parlay listen`/`monitor`
   process for `id` on THIS host — same detect/SIGTERM/2s-grace/SIGKILL
   sequence the singleton guard (robots-fgyz) already uses when taking over a
   channel. Deliberately does **not** honor `PARLAY_LISTEN_NO_SINGLETON` —
   that env var is scoped to `parlay listen`'s own arming-time opt-out; an
   explicit shutdown request should always act.
2. `POST /api/chat/unregister` deregisters `id` server-side. `unregisterAgent`
   (`packages/server/src/prune/sweep.ts`) tombstones the channel and reports
   an `undelivered` count — user messages queued for the channel that were
   never polled/received. This count is **reported, not flushed**: there is
   no other listener to hand them to, and discarding chat history is a
   separate, unrequested destructive action.
3. `unregisterAgent` also calls `resolvePollWaiters` (`packages/server/src/sse.ts`)
   to immediately resolve any long-poll already parked on that channel with
   `{gone: true}`, instead of leaving it to sit out its own up-to-30s timeout
   and only find out on its *next* request's 410. The relay's `pollLoop`
   (`tools/relay/relay_poll.go`) treats an in-body `Gone` exactly like a fresh
   request's terminal 410 (`errChannelGone`, task-0n80i): drop the loop and
   tombstone the local spool, so a relay restart never resurrects it.

No separate relay-control-socket call was added for this verb — the existing
poll-loop/tombstone machinery, plus the new immediate `Gone` signal, already
gets the relay there within one cycle. (The control socket has exactly one
existing caller in this codebase, `parlay-monitor.sh` via bash+curl; a second,
Go-native caller wasn't justified for this.)

Idempotent by design: a 404 from step 2 means the agent was already retired —
this verb's success case ("already retired"), not an error. Unlike
`parlay agent-down` (fails loud on an unknown id), `shutdown` is meant to be
the one thing a departing agent or a supervisor sweeping a dead one can
always call without checking state first.

Scope boundary: this only touches the TS server (`packages/server`, the live
production server per this file's own header) and `tools/relay`/`tools/cli`,
matching the precedent of PR #225 (relay-only) and PR #226 (TS-server-only) —
`packages/go-server` was not given parity here.
