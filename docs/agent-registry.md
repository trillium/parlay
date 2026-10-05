# Agent registry & presence

**Code:** `packages/go-server/internal/store/registry.go` (the on-disk half) and
`presence.go` (the in-memory half); the HTTP routes are in
`packages/go-server/internal/handlers/registry.go`. Wire shapes:
[`docs/api-contract.md`](api-contract.md) §Agent registry / presence.

Not to be confused with the **live-command registry**
([`docs/live-commands.md`](live-commands.md)), which tracks running `parlay`
CLI invocations and *does* have an age-based reaper. This is the **agent
registry**: who is enrolled as a chat tab at all.

## One file, rewritten whole

`agents.json`, in the server's state directory — `~/.parlay` by default, or
whatever `-state-dir` / `PARLAY_STATE_HOME` names:

```
$PARLAY_STATE_HOME/agents.json
```

It is a full-snapshot JSON array, atomically rewritten on every change. The
registry holds a handful of entries, so unlike `messages.jsonl` there is no
append-log/ring-buffer split — see `store.go`'s layout comment for the rest of
the state directory.

Note there is **no** `~/.claude/PAI/MEMORY/STATE/parlay-agents.json` anymore.
That was the deleted TypeScript server's file; nothing in this repository
reads or writes it, and there is no `PARLAY_DATA_DIR` to redirect for it. The
Go server's registry is the only one.

### `Upsert` is partial, not replace

`POST /api/chat/register-agent` merges per field, because each caller only
knows the fields it has (`id`, `name`, `color`, `nicknames`, `caps`; the stored
record also carries `urls` and `path`). A caller that sends only `id` and
`color` does **not** blank an existing `name`. The one deliberate exception is
`nicknames`: an explicit empty (non-nil) array clears it, since `nil` is the
only "omitted" sentinel in the merge.

## Presence is never on disk

`PresenceTracker` holds the transient half — connected SSE panel clients,
active `GET /poll` counts per channel, per-channel last-seen timestamps — in
memory only. That is deliberate: a connection count that survived a restart
would be lying, since every live connection dies with the process.

`GET /api/chat/subscribers` is the one route that joins both halves: presence
counters plus the full `Registry.List()`, which is why it is guarded
(identifier disclosure).

## Who writes a row, who takes it away

| Direction | Call site | Route |
| --- | --- | --- |
| Enroll | `parlay listen` (`internal/monitor/listen.go`) | `POST /api/chat/register-agent` |
| Enroll | `parlay monitor` (`internal/monitor/monitor.go`) | `POST /api/chat/register-agent` |
| Enroll + announce | `parlay spawn` (`internal/spawn/httpclient.go`), `parlay claim` (`internal/commands/claim.go`) | `register-agent`, then a hello `POST /api/chat/reply` so the tab goes live immediately |
| Remove | `parlay teardown`, `parlay shutdown` (both best-effort the unregister) | `POST /api/chat/unregister` |
| Remove | `parlay heal` | `POST /api/chat/unregister` |
| Remove | `parlay sweep --apply` | via `teardownAgent`, so it inherits teardown's chain |
| Remove | REST alias, for clients outside this repo (nothing in here calls it) | `DELETE /api/chat/agents/{id}` |
| Read | CLI and panel tabs | `GET /api/chat/agents`, the `agents` burst on SSE connect, and the incremental `agent_register` / `agent_unregister` broadcasts |

Registration and removal both broadcast, so a connected panel updates without
polling the list.

## Nothing reaps a row by age

**This is the one thing to get right when reading older material.** The
TypeScript server had an idle reaper: it unregistered Parlay-launched agents
after two hours of no activity, keyed on a `launchedBy` stamp in the registry
row. That server is deleted, and **the Go server has no equivalent**. There is
no age-based prune of `agents.json`, no `PARLAY_AGENT_IDLE_TIMEOUT_MS`, and no
`launchedBy` exemption — because there is no reaper for anyone to be exempt
from. (`parlay spawn` and `parlay claim` still *send* `launchedBy` /
`startedAt` on their `register-agent` bodies; the Go handler's request struct
has no such fields, so they are accepted and dropped.)

What this means in practice:

- A row outlives its agent. Deregistering is **explicit** — `parlay shutdown
  <id>`, `parlay teardown <id>`, `parlay heal`, or `parlay sweep --apply`
  (which only closes agents it can prove closeable). See
  [`docs/monitor.md`](monitor.md) for each verb's contract.
- A row is therefore not evidence of liveness. A registration is a claim, not a
  listener: the panel will happily route work to an id whose process is gone.
  Reach for `parlay stale <id>` / `parlay sweep` rather than reading the
  registry as a fleet census.
- If you are porting something that relied on the old reaper, that behavior has
  to be re-implemented deliberately — there is no compatibility shim.

A row *is* a name and a color, and `AgentInfo` says nothing else about
liveness: no pid, no process table, no timestamps.

## And nothing tombstones a channel either

Unregistering does exactly two things today: it removes the row and it
broadcasts `agent_unregister`. It does **not** mark the channel gone, resolve a
parked poll, or make later polls fail — the Go server has no 410 and no
`{"gone": true}` response anywhere.

Two consequences, both of which surprise people who inherited the TypeScript
server's model:

- A poll loop that was already parked on the channel waits out its 25s timeout
  and comes straight back. The relay's 410-tombstone path
  (`tools/relay/relay_poll.go`) and `parlay monitor`'s "channel was unregistered
  (410)" exit are both unreachable against this server, so a remote agent's
  local spool survives `parlay shutdown` and keeps polling. Killing the
  listener — which `parlay shutdown` does for a *local* agent — is the only
  thing that actually stops it today.
- A retired channel still receives messages. Delivery never consults the agent
  registry (`messaging.go` reads it only to expand nicknames), so sending to an
  id that was never enrolled — or one that has been retired — delivers
  normally.

`docs/monitor.md` documents the same gap from the `parlay shutdown` side, with
the exact route and response shapes.