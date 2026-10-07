# Chat history, and the streams that feed it

**Code:** `packages/go-server/internal/store/` — `store.go` (the state-dir
layout), `messages.go` (chat history), `channels.go` (session → channel).
Served by `internal/handlers/{poll,events,events_ingress,panel}.go`. The wire
shapes are in [`api-contract.md`](api-contract.md).

There is exactly **one** server implementation (`packages/go-server`), so
there is exactly one history file. If you are reading a reference to
`chat-history.jsonl`, `$PARLAY_DATA_DIR`, `~/exchange`, or a 5 MB rotation,
it is describing the TypeScript server that was deleted with the Bun→Go
cutover; nothing reads or writes those.

## The file

`messages.jsonl`, in the server's state dir — `$PARLAY_STATE_HOME`, default
`~/.parlay`, overridable per-process with `-state-dir`. One `ChatMessage` per
line, append-only. The shape is the one documented under `GET
/api/chat/history` in [`api-contract.md`](api-contract.md), and is defined
once, in Go, by `store.ChatMessage`.

Three properties worth knowing before you go looking for data in it:

- **Ids are assigned by the server** as `m` + a base-36 sequence counter
  (`nextID`), unless a caller supplies its own. They are monotonic and
  sortable, not dense — the counter is deliberately *not* reset by
  `POST /api/chat/clear`, because clients hold these ids as long-poll
  cursors and a reused id resolves against a different message.
- **The file is the durable record; the in-memory ring is a working set.**
  `DefaultMaxMessages` (5,000) caps what the process retains, so `GET
  /api/chat/history` and an SSE reconnect can never return more than the
  last 5,000 messages even if the file holds more.
- **Growth is bounded by compaction, not rotation.** Once the file passes
  `DefaultMaxHistoryBytes` (32 MiB), the next append rewrites it down to
  exactly what the ring still holds, through an atomic replace. A corrupt
  line is skipped at load rather than failing startup.

A cleared channel is removed by the same rewrite (`RemoveByChannel`).

## What is *not* in the file

Two things that look like history but are not:

- `received` (a user message marked as polled by an agent) is runtime-only
  and never written to disk.
- `PARLAY_PUBLIC_HOST` link rewriting happens **at serve time** only, in
  `internal/linkrewrite`. Reading `messages.jsonl` directly always shows the
  original `http://localhost:<port>` links.

## Who reads it

| Reader | What it gets |
|---|---|
| `GET /api/chat/history?limit=N` (`parlay history [N]`) | The newest `N` retained messages, oldest first. An absent, non-numeric or non-positive `limit` returns the **whole retained window** — up to 5,000 messages, not a 200-message default. |
| SSE connect burst, `history` event (`GET /api/chat/events?after=<id>`) | With a resolvable `after`: the delta after that id. Without one, or if the id is outside the retained window: the full retained window. |
| `GET /api/chat/poll?after=<id>` | One message per call. An unresolvable cursor replays the newest `min(50, retained)` on that channel and says so (`cursorReset`, `skipped`) rather than silently delivering nothing. |

There is no channel scoping on `GET /api/chat/history` — a `channel` query
parameter is ignored. (`packages/webview` sends one and then filters the
result client-side.) Clearing is `POST /api/chat/clear`, all of it or one
channel's.

## The other JSONL streams — none of which this server reads

Hook firings (`$PAI_DIR/MEMORY/OBSERVABILITY/hook-firings.jsonl`) and tool
activity (`tool-activity.jsonl`) are written elsewhere, by Claude Code hooks,
synchronously and locally. The server does not tail them, and has no
tool-activity tailer of its own (`channels.go` says so at the point where the
deleted TS server had one as a fallback).

The tailers that read those files live in the TS/Pulse home, outside this
repository, and reach the panel over HTTP to `PARLAY_HUB_URL` (default
`http://127.0.0.1:4242`):

- **Hook firings → `POST /api/chat/message`.** Persisted first, then
  broadcast as a consequence, arriving in history as a `system_update` with
  its `source` label. This is the route that makes hook activity durable.
- **Tool activity → `POST /api/chat/events`** as a `tool_event` frame, which
  is *not* persisted. That route's event-name allowlist is derived from the
  enrolled source contracts in `contracts/sources/*.json`
  ([`source-contracts.md`](source-contracts.md)); `tool_event` is the only
  name enrolled today. See the rule in [`AGENTS.md`](../AGENTS.md): **one name
  per real producer**, never widen it to the documented-but-unproduced names.

Routing a hook firing to the right agent's tab is a separate, explicit step:
processing signals carry a Claude Code `session_id`, never a Parlay channel,
so the session is bound with `POST /api/chat/declare-channel`
(`channels.json`). Declarations are **sticky — first one wins**, because a
later declaration of a different channel means an agent is watching another
channel, not that it became that agent.

Finally, do not confuse these with the fleet's `~/data/<store>/events.jsonl`
streams (`robots`, `inbox`), tailed by `parlay robots-tail` / `parlay
inbox-tail` to fire `mechanic-dispatch`. Those never touch chat history, and
those tailers *are* in this repo (`tools/cli/internal/robotswatch`).

This is load-bearing in one direction only: hooks pay no network latency for
their own writes, and the tail is the only thing that touches the network.
