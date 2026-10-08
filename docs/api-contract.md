# Parlay HTTP API contract

> **Authority.** This document and
> [`api-contract.openapi.yaml`](./api-contract.openapi.yaml) describe the same
> surface. The OpenAPI file is the machine-readable twin and is
> **authoritative when the two disagree**; fix this doc to match it.
>
> **How this doc was written.** Originally (2026-08-01) reconstructed from
> client/CLI call sites while `packages/server/src` was unreadable. On
> 2026-08-30 every route and shape below was re-derived from the **handler
> source** of both servers — `packages/server/src/*` (TS) and
> `packages/go-server/internal/*` (Go) — so the shapes here are read from the
> code that produces them, not inferred from consumers. The TS server was
> **deleted with the Bun→Go cutover** (the code half merged after Go reached
> feature parity on the production surface), so the contract now describes
> what the Go server does, and the "TS:" annotations below are historical
> notes on the retired implementation where a modern caller still needs the
> contrast. Known leftovers of that time are collected in
> [Divergences to fix](#divergences-to-fix) rather than papered over.
>
> Base path for all REST/SSE routes: `/api/chat` (exceptions: `/health`,
> `/api/debug/*`, `/parlay-ui.js`, static assets). `CHAT_BASE` in
> `packages/client/src/config.ts` is the single client-side constant; the CLI
> resolves the server origin via `config.ServerURL()`
> (`tools/cli/internal/config/config.go`): `PARLAY_SERVER` env → persisted
> `~/.parlay/config.json` `"server"` key → `http://localhost:4242`. All
> request/response bodies are JSON unless noted.

One implementation serves this surface:

- **Go server** — `packages/go-server` (`cmd/parlay-server`), the sole
  production server, built against this contract.

Routes marked **(TS only)** referred to routes on the deleted Bun server and
are kept so a caller can tell a retained-forever quirk from a regression.

## Conventions

- **No authentication anywhere.** The surface trusts the network boundary
  (local/tailnet only — do not expose the port publicly). The enforced part of
  the trust model is the cross-origin half: see [Origin guard](#origin-guard).
- **Two error conventions coexist** (verified in handler source):
  - *App-error-on-200*: most write endpoints (`send`, `reply`, `alert`,
    `system`, `register-agent`, `declare-channel`, `draft` PUT, `eval`'s
    validation, tts family) return HTTP **200 with `{"error": "…"}`** on
    validation failure. Callers detect failure by the `error` field, not the
    status.
  - *Status-error*: `unregister` / `DELETE /agents/:id` (400/404),
    `POST /message` (400), `poll` (410), `eval` engine failures (502),
    `eval-push` (400/404), invalid `?caps=` (400), `tts/validate-splits`
    (400/502), `remote-input/submit` (400), `remote-input/status`
    (400/404), and `remote-input/targets` (502 Talon unreachable) return a **non-2xx with `{"error": "…"}`**.
- **Malformed JSON body**: the Go server returns **400
  `{"error": "invalid JSON body"}`** on every JSON route uniformly
  (`decodeJSON`).
- **Wrong method**: the Go server answers **405** with an `Allow` header and a
  plain-text body `"<METHODS> only"` (`methodNotAllowed`) on every route.
- **CORS**: the Go server sends no `Access-Control-Allow-Origin` on unguarded
  routes. Guarded routes reflect the single allowed origin (see Origin guard).

### Origin guard

Unauthenticated does **not** mean unrestricted: the mutating and
identifier-aiming surface — the routes that write state, drive a device, or
hand out an identifier (device uuid, agent id) a hostile page could then aim
at a mutating route — sits behind an origin guard —
`packages/go-server/internal/guard`. **Within that surface a route is guarded
by what its handler does, not by its HTTP method:** `GET
/api/chat/subscribers` and `GET /api/chat/poll` are both guarded, the first
because it discloses identifiers, the second defense-in-depth (it still
returns message content and drives presence bookkeeping) even though polling
no longer registers the channel (task-1t0m). On those routes:

- A request with **no `Origin` header is allowed.** That is every CLI, curl,
  hook and server-to-server caller, and a browser cannot omit `Origin` on a
  cross-site request. Nothing in this document changes for those callers.
- A request **with** an `Origin` must be same-origin, a loopback / `.local` /
  private-LAN origin, or listed in `PARLAY_ALLOWED_ORIGINS` (comma-separated;
  `*` opts out). Otherwise: **403** with no CORS headers at all, and the
  handler is never reached. Preflight from such an origin is refused the same
  way.
- `POST`/`PUT` must carry `Content-Type: application/json`, else **415** —
  this is what forces a preflight instead of letting a CORS *simple request*
  through. Three routes are exempt from this gate and this gate only —
  `POST /api/chat/upload` (multipart by contract), `POST
  /api/chat/plugin/cursorless/rpc` (its handler parses the body with
  `req.json()` regardless of the header, for an out-of-repo Talon caller), and
  `POST /api/chat/tts/validate-splits` (same shape, and its only callers are
  hand-run `curl -d`, whose default content type is
  `application/x-www-form-urlencoded`) — and on those the origin check alone
  applies; they stay guarded. **The origin check runs first**, so a
  disallowed origin sending a simple content type gets 403, not 415; a 415 is
  only ever reachable from an origin that was already allowed.
- Allowed responses carry the **exact** origin in
  `Access-Control-Allow-Origin` plus `Vary: Origin` — never `*`.

The guard also covers whole subtrees (`guardedPrefixes`): `/api/chat/agents/`,
`/api/chat/plugin/`, `/api/debug/` — anything added under those is guarded
before its handler exists.

The read routes (`history`, `agents`, `commands`, `version`, `pages`,
`plugins`, `GET /api/chat/uploads/<name>`) are outside the guard and behave as
documented below. `TestUnguardedRoutes` in
`packages/go-server/internal/guard/route_coverage_test.go` is that list with a
reason per entry, and `TestEveryRegisteredRouteIsGuardedOrExplained` parses
`internal/handlers` for every path on the mux and fails the build on one that
is neither guarded nor listed — so a new route cannot ship outside the boundary
unnoticed. `/api/chat/events` is **guarded**: `packages/go-server` serves
an external-producer ingress on `POST /api/chat/events` (see [SSE
Events](#sse-events) below), so the path is in `internal/guard.GuardedPaths`,
and because that classifier is method-independent the `GET` SSE stream is
guarded with it. No caller loses the stream: the panel is same-origin, and the
tailers, the CLI and curl send no `Origin`. The herdr web page is the one
cross-origin reader: it is served from another origin (for example
`http://<host>:8787`) and builds its server URL as `${protocol}//${hostname}:4242`,
so `GET /api/chat/events` reflects `Access-Control-Allow-Origin` (plus
`Vary: Origin`) **only** for an origin listed verbatim in
`PARLAY_ALLOWED_ORIGINS` (`listedOriginCORSReads` in `internal/guard/guard.go`).
An origin the guard merely accepts (loopback, `.local`, private LAN) but the
operator did not list gets the stream and no ACAO; a disallowed origin gets 403;
a `*` entry is never echoed as a wildcard. With `PARLAY_ALLOWED_ORIGINS` unset, the stream sends no ACAO to any cross-origin page; the herdr web origin must be listed there for "bravely" to submit. Without the ACAO the page would never
receive the `input_action` reply that makes the spoken "bravely" line-ender
submit.

The read surface is not purely read-only, but the boundary above is the whole
of the guard's scope: the deleted TS server's two unguarded reads
(`GET /api/chat/events` storing an attacker-supplied `?device=`, and
`GET /api/chat/agents` answering `Access-Control-Allow-Origin: *`) were its
`identifier-disclosure-remains-on-sse` residue and died with it. The Go server
ties the read routes closed differently: its unguarded routes send no
`Access-Control-Allow-Origin` at all, so a foreign page's read still executes
but its body stays unreadable, and its `/api/chat/events` is guarded outright
(with the same no-ACAO posture preserved on the stream) and accepts `?device=`
without storing it.

---

## Messaging

### `POST /api/chat/send`
Send a user-role message into a channel (panel UI, CLI, agents).

Request body:
```jsonc
{
  "text": "string",              // required unless images present
  "toAgent": "agent-id",         // optional — target channel
  "images": ["url", "..."],      // optional — up to 8 kept, each ≤500 chars; extras/oversize are appended to text as plain URLs
  "from": "display name"         // optional — sender attribution, truncated to 40 chars
}
```
Responses (always 200):
```jsonc
{ "ok": true, "id": "msg-id" }
{ "error": "empty message" }     // no text and no images
{ "error": "bad request" }     // unparseable body
```
Broadcasts `message` over SSE and wakes matching long-pollers.

### `POST /api/chat/reply`
An agent replies on **its own** channel. Identity comes from `agent` in the
body, which the Go server requires.

Request body:
```jsonc
{
  "text": "string",     // required
  "agent": "agent-id",  // channel this posts to (required)
  "name": "string",     // optional display-name upsert
  "color": "#rrggbb",   // optional (accepted but not persisted)
}
```
Responses (always 200):
```jsonc
{ "ok": true, "id": "msg-id", "new_channel": true }  // new_channel present iff the agent was auto-registered by this reply (broadcasts agent_register)
{ "error": "empty reply" | "unknown action kind: X" | "bad request" | "…" }
```

### `POST /api/chat/alert`
Broadcast a message to multiple channels at once.

Request body:
```jsonc
{ "text": "string", "agents": ["agent-id", "..."] }  // "agents" omitted = broadcast to all
```
Response (200): `{ "ok": true, "channels": N, "delivered": M }` — `channels` =
channels the alert was recorded against, `delivered` = live pollers woken.
"All" means every registered agent. An explicit **empty array** delivers to
nobody.

### `POST /api/chat/system`
Post a system line onto the dedicated `"system"` channel
(`type: "system_update"` — the panel renders a muted line and skips TTS).

Request body:
```jsonc
{ "text": "string", "source": "string?", "meta": { }? }
```
`text` is truncated to 500 runes. Response (200):
`{ "ok": true, "id": "msg-id" }` or `{ "error": "text required" }`.

### `POST /api/chat/message`
Lower-level persist-and-broadcast: stores the message and broadcasts the
resulting `message` SSE event. The out-of-process seam for producers that
cannot live in the server process (`parlay supervise` digests, the PAI hook
tailer via the go-server ingress).

Request body:
```jsonc
{
  "channel": "agent-id",        // required
  "role":    "agent",           // optional, defaults "agent"
  "text":    "string",          // required
  "type":    "system_update",   // optional ChatMessage.type passthrough
  "source":  "SessionStart",    // optional
  "meta":    { "session_id": "s-1" }  // optional
}
```
Responses: 200 `{ "ok": true, "id": "…" }`; **400**
`{"error": "channel and text are required"}` (status-error convention, unlike
`send`/`reply`).

### `GET /api/chat/history?limit=N`
Recent chat history, oldest first. `limit` bounds how many of the retained
messages come back (the server retains 5,000 — see
[`events-history.md`](events-history.md)); an absent, non-numeric or
non-positive `limit` returns the whole retained window rather than a
smaller default. There is no channel scoping: a `channel` query parameter is
ignored.

Response: `ChatMessage[]`
```ts
interface ChatMessage {
  id: string
  role: "user" | "agent"
  ts: string          // ISO 8601
  text: string
  channel?: string    // agent id; absent for global/user messages
  type?: "alert" | "action_request" | "system_update"
  action?: { kind: "navigate"|"switch_tab", url?: string, channel?: string, label: string }  // iff type === "action_request"
  source?: string     // system_update: emitting hook/system label
  meta?: Record<string, unknown>
  images?: string[]
  from?: string       // user-role sender attribution; absent = the captain
  received?: boolean  // user messages: false=queued, true=agent polled it (runtime-only, stripped from disk)
}
```

### `GET /api/chat/input-events?inputId=<id>&afterSeq=<seq>&limit=N`
The input-seam ledger: what happened to operator input between an intake
surface accepting it and a listener being handed it. See
[`input-seam.md`](input-seam.md) for the vocabulary and the failure classes it
tells apart.

One record per hop, append-only, keyed by the input's id — the stored message
id, or a ledger-local `in-…` id for input that never became a message (a
request an intake surface refused before storing anything, or a composer
snapshot the eval engine dropped as superseded). `inputId` narrows the answer
to one input's hops (what a replay renders); an unknown id is an **empty
list**, not an error and not a created record.

`afterSeq` is the cursor a live tail reads forward with: only hops whose `seq`
is strictly greater are returned, oldest first. A cursor older than the
retained window is **not** an error — the oldest retained hops come back, and
because `seq` is dense their first value names the span that was evicted, which
is the only way a reader can tell "nothing new" from "I fell off the end". An
unreadable `afterSeq` is read as `0`, the widest and least-lossy answer.

`limit` is where the one asymmetry lives: without a cursor it returns at most
the **newest** N retained events (unchanged), but with `afterSeq` it returns at
most the **oldest** N of the matching set, so a reader paging forward never has
a page silently omitted out from under it.

Response: `{ events: InputEvent[], stats: InputEventStats, listeners: InputListener[], pollHoldMs: number }`
```ts
interface InputEvent {
  seq: number          // monotonic within the retained ledger; the replay ordering key
  ts: string           // ISO 8601
  inputId: string
  stage: "received" | "interpreted" | "routed" | "queued" | "delivered" | "held" | "superseded"
  class: "ok" | "recogniser_error" | "low_confidence" | "confidence_unknown"
       | "no_match" | "refused" | "unpicked" | "superseded" | "held"
  source?: string      // the surface or mechanism that produced this hop: send, alert, poll-wake, poll-backlog, remote-input, eval
  channel?: string     // destination agent/channel, once one was chosen
  confidence?: number  // reported recognition confidence in [0,1]; absent = not reported
  threshold?: number   // the threshold that measurement was compared against, as it was in force at that moment (absent = no threshold configured, or none reported)
  reason?: string      // short token; REQUIRED for every class but "ok"
  detail?: string      // bounded non-content context
}
interface InputEventStats {
  retained: number; written: number; dropped: number; rejected: number; queue: number
  newestSeq: number       // highest seq assigned (0 = nothing written); a cursor reader's anchor
  minConfidence?: number  // the hold threshold actually in force; absent = disabled
}
interface InputListener {
  channel: string
  lastPollTs?: string  // ISO 8601; when anything last ASKED this channel for messages
  activePollers: number // long-polls parked on it right now
  lastPollCursored?: boolean // did that last ask carry a backlog cursor (`after=`)?
}
```

`listeners` and `pollHoldMs` are the half of the seam the ledger cannot
carry, and they are the reason a waiting input is no longer a dead end: a
`queued` hop and a `queued` hop nobody will ever collect are the same row, so
`listeners` reports the server's own poll activity per channel — when anything
last asked it for messages, and how many long-polls are parked on it right now.
`pollHoldMs` is how long this server holds a parked poll (25s), which is the
window a listener that is attached cannot be quieter than; a reader uses it to
tell "attached and took nothing" from "nothing is there".

Two shapes here are load-bearing:

- **`listeners` is never omitted.** `[]` means *nothing has polled any channel
  since the server started*; an **absent** field means an older server that
  does not report listener activity at all. A reader that read those as the
  same thing would turn an old server into a silently deaf fleet.
- **A channel that was never polled is absent from the array**, not present
  with an empty timestamp, and a channel stays listed after its poller leaves
  (that is the whole point — `activePollers` goes to 0 while `lastPollTs`
  remains). `limit`, `inputId` and `afterSeq` narrow the ledger only: who is
  listening is a fact about the server right now, not about the window a
  reader asked for.
- **`lastPollCursored` is a three-state field, not a boolean.** A server that
  reports it always sets it, so `true` and `false` are real facts about the
  last poll on that channel, while **absent** means the server does not report
  it at all. It must not be read as `false`: that would brand every listener on
  an older server as unable to replay anything. The fact exists because
  `handlePoll` consults the retained store **only** for a request that carried
  `after=`, so a cursorless poll can only ever see a message published while it
  waited — which is what turns "a listener is attached and has not taken it"
  into a named mechanism instead of a shrug.

The listener facts come from the same in-memory presence tracker
`/api/chat/subscribers` counts (`poll.channels[].count`), read here per
channel with the timestamp that surface does not carry. Recording them is one
map write on the poll path — no I/O, no error — and reading them never touches
the ledger's disk (`TestPollDeliveryIsNotBlockedByInputEventsReaders`).

Three invariants a reader can rely on, because each is enforced rather than
intended:

- **No message text, ever.** The ledger holds ids, stages, classes, short
  reason tokens and numbers. The full transcript lives in `messages.jsonl`
  under its own retention rule; this is not a second copy of it.
- **A non-`ok` hop always carries a `reason`.** A failure with no recorded
  reason is a defect in the tooling, so the ledger refuses to store one and
  counts it in `stats.rejected` instead.
- **`stats` travels with `events`.** An empty list and a ledger that is
  silently shedding records must never look the same. `stats.minConfidence`
  is the hold threshold in force, so a hold is never inferred from a row.

Unguarded: it returns only message ids, channel names (which
`/api/chat/history` already returns unguarded) and per-channel poll
counts/timestamps (which name a channel, not a caller), and it writes nothing.
Recording is asynchronous and never sits in the delivery path — a wedged
ledger sink cannot slow or fail a send or a poll (pinned by
`TestDeliveryIsNotSlowedOrFailedByAWedgedLedger`), and readers of the listener
half cannot stall a woken poll either.

### `GET /api/chat/poll?after=<lastId>&channel=<agentId>`
Agent long-poll. Blocks until a message for the channel arrives or the
server-side timeout elapses (25s), then returns exactly one
of:

```jsonc
{ "timeout": true }
{ "id": "…", "role": "user", "text": "…", "from": "…?", "cursorReset": true?, "skipped": N? }
```

- **410 Gone** `{ "error": "…", "gone": true }` when the channel is
  tombstoned (unregistered via `agent-down`) — a dead agent must not re-create
  itself by polling.
- Polling an **unknown, non-tombstoned** channel is genuinely read-only
  (task-1t0m): it neither creates nor resurrects a registry row.
  Registration only happens via the explicit, guarded `POST
  /api/chat/register-agent` — every real poll consumer (`parlay listen`,
  `parlay monitor`, the relay) calls it before polling.
- Delivering a queued user message marks it received and broadcasts
  `message_received` (payload `{ "id" }`).
- A poll with no `after` waits for the *next* message only (no replay).

`cursorReset`/`skipped` appear only when `after` names a message the server
cannot resolve among the channel's retained messages — a truncated or rotated
store, a cursor from a previous server run, or a cursor belonging to a
different channel. Rather than silently delivering nothing, the server resumes
from the newest `min(50, retained)` messages on that channel
(`DefaultReplayMax`, mirroring the relay's `PARLAY_REPLAY_MAX`) and says so:
`cursorReset: true`, with `skipped` counting the older retained messages left
outside that window. The reset frame carries the oldest message of that
window, so the caller's next `after` resolves normally. A resolvable cursor
never sets either field.

### Localhost link rewriting (`PARLAY_PUBLIC_HOST`)
`ChatMessage.text` may contain `http://localhost:<port>` / `http://127.0.0.1:<port>`
links (server URLs, panel links, agent endpoints) that are dead once the
captain reads Parlay off-home. Setting `PARLAY_PUBLIC_HOST` rewrites just the
host of those links, at serve time only — history (`GET /api/chat/history`),
poll (`GET /api/chat/poll`), and the SSE `history`/`message` events all
rewrite through the same helper
(`packages/go-server/internal/linkrewrite`). The stored/retained message
text itself is never mutated — a client reading the durable log directly
still sees the original `localhost` link.

- Unset (default) ⇒ no rewrite, byte-identical to legacy behavior.
- `"auto"` ⇒ resolved once per process from `tailscale status --json`'s
  `Self.DNSName` (short node name, e.g. `macbook`); fails open to no-rewrite
  if `tailscale` is unavailable or errors.
- Any other value ⇒ used as the host literally (bare hostname, FQDN, or IP);
  only the host swaps, port/path/query/hash are preserved.
- Fail-open is absolute: any resolution or rewrite error returns the
  original text unchanged.

---

## Agent registry / presence

### `POST /api/chat/register-agent`
Upsert an agent's registry entry. Idempotent — safe to call on every restart.
Registering clears any tombstone for the id.

Request body (all optional except `id`):
```jsonc
{
  "id": "agent-id",
  "name": "Display Name",
  "color": "#rrggbb",
  "nicknames": ["nick1"],   // explicit [] clears; omitted = keep existing
  "urls": ["…"], "path": ["…"],
  "caps": { }               // free-form agent metadata (parlay listen --caps); unrelated to SSE ?caps=
}
```
Response (200): `{ "ok": true, "nicknames"? }`. Errors: `{ "error": "id required" | "bad request" }`.
Broadcasts `agent_register` with the stored `AgentInfo`.

### `POST /api/chat/unregister`
Deregister an agent's channel. **Status-error convention**: **400**
`{"error": "id required"}`, **404** `{"error": "…"}` on unknown id.

Request: `{ "id": "agent-id" }`. Success (200): `{ "ok": true, "id": "…" }`.
Broadcasts `agent_unregister`.

### `DELETE /api/chat/agents/:id`
REST alias of `unregister` (same handler path, id from the URL). Same
status-error convention and response shapes.

### `GET /api/chat/agents`
List all registered agents. Response: `AgentInfo[]`
```ts
interface AgentInfo {
  id: string
  name: string
  color: string
  nicknames?: string[]  // first entry is the primary display alias
  urls?: string[]       // pulse pages this agent owns
  path?: string[]       // filesystem paths this agent is responsible for
}
```

### `GET /api/chat/subscribers`
Connection/presence snapshot. Guarded (identifier disclosure).

Response:
```jsonc
{
  "parlay":     { "clients": 2 },                       // connected SSE clients
  "poll":       { "count": 1, "channels": [ { "channel": "id|null", /* + AgentInfo fields when registered */ } ] },
  "registered": { "count": 3, "agents": [ /* AgentInfo */ ] },
  "presence":   [ { "channel": "id", "lastSeen": "iso|null" } ],
  "capability_suppressed": { "navigate": 3 },           // gated event → deliveries suppressed
  "capability_declarations": [ { "surface": { "kind": "panel", "instance"?: "…" }, "accepts": ["…"], "content": ["…"], "interactions": ["…"], "connectedAt": "iso", "device"?: "uuid" } ],  // one entry per declared SSE connection (device-identified or not), all three axes
}
```

### `POST /api/chat/declare-channel`
Bind a session id to a channel (used by hooks to attribute system lines).

Request: `{ "session_id": "s-1", "channel": "agent-id" }`. Response (200):
`{ "ok": true, "session_id": "…", "channel": "…" }` or
`{ "error": "session_id and channel required" }`. Declarations are sticky per
session and the server echoes the *effective* channel, which can differ from
the request.

---

## Panel control (server → connected browsers)

These routes exist so agents/CLI can drive connected panels; each broadcasts
an SSE event and reports how many clients it reached. All guarded.

### `POST /api/chat/clear`
Clear chat history — all of it, or one channel's.

Request: `{ "channel": "agent-id"? }` (empty/absent body = clear everything).
Response (200): `{ "ok": true, "removed": N, "remaining": M }`. Broadcasts
`reload` so every panel refetches.

### `POST /api/chat/reload`
Force connected panels to reload. Request: `{ "device": "uuid"? }` — scope to
one device or omit for all. Response (200):
`{ "ok": true, "clients": N, "device"?: "uuid" }`. Broadcasts `reload`.

### `POST /api/chat/navigate`
Navigate connected panels (Parlay-as-shell workspace navigation).

Request: `{ "url": "string", "open_drawer": true?, "device": "uuid"? }`.
Response (200): `{ "ok": true, "clients": N, "url", "open_drawer": bool,
"device"?: "…" }`. Error:
`{ "error": "url required" }`. Broadcasts `navigate`
`{ "url", "openDrawer" }`.

### `POST /api/chat/device-cmd`
Drive a client device (reload TTS, switch channel, toggle hands-free, …).

Request body:
```jsonc
{
  "cmd": "reload" | "reset-tts" | "ping" | "switch-channel" | "list-channels" | "set-hands-free",
  "args": { "channel": "agent-id" },       // switch-channel
  // or "args": { "enabled": "true"|"false" },  // set-hands-free; omit to toggle
  "device": "uuid"?                        // scope to one device
}
```
Response (200): `{ "ok": true, "cmd": "…", "sent": N }` or
`{ "error": "cmd required" }`. Broadcasts `device_cmd` `{ "cmd", "args"? }`.

---

## Drafts

### `GET /api/chat/draft`
Response: `{ "text": "string" }`. The server returns the whole stored draft:
`{ "text", "clientId"?, "updatedAt"? }` — extra fields are harmless to the
one consumer, which reads `text`.

### `PUT /api/chat/draft`
Save (or clear, with `text: ""`) the shared input draft.

Request: `{ "text": "string", "clientId": "uuid"? }` — `clientId` is a
per-page-load id the client uses to ignore its own `draft` SSE echo.
Response (200): echoes the saved draft object. Broadcasts `draft`
`{ "text", "clientId"? }`.

---

## Uploads

### `POST /api/chat/upload`
Image upload. `multipart/form-data` with a single `file` field (the one
JSON-content-type exemption class in the guard). Images only, 10MB max.

Response (200):
```jsonc
{ "ok": true, "url": "/api/chat/uploads/<sha1-12>.<ext>", "bytes": 12345 }
```
Failures: returns a bare `{ "ok": false }` with no error field (its callers
only check `ok`/`url`). The server sniffs the actual bytes
(`http.DetectContentType`) and ignores the claimed type.

### `GET /api/chat/uploads/<name>`
Serve an uploaded image inline. Unguarded read. No name regex (store lookup
instead); Content-Type is sniffed from the file bytes, not the extension; 404
on unknown.

On disk: `<state-dir>/uploads/<name>` (`$PARLAY_STATE_HOME`, default
`~/.parlay`) — agents may read that path directly. `<name>` is server-
generated (random + a sanitized image extension), never the client's filename.

---

## Settings

### `GET /api/chat/parlay/settings` · `PUT /api/chat/parlay/settings`
Load / whole-document-replace the persisted panel settings (never a patch: an
omitted field is stored as its zero value).

```ts
interface ParlaySettings {
  panelSide: "left" | "right"
  triggerSide: "left" | "right"
  enabledProjects: "all" | string[]
  voiceEnabled: boolean
  voiceSubmitPhrases: string[]
  voiceClearPhrases: string[]
  voiceStopPhrase: string
  commandPhrases: Record<string, string[]>
  hybridVoice: boolean
  localOnlyVoice: boolean
  textScale: number // PERCENT; 100 = default. The client divides by 100.
  voiceSettleMs: number
  noKeyboardMode: boolean
}
```
GET with no `settings.json` on disk returns `DefaultSettings()`
(`packages/go-server/internal/store/settings.go`) — the document a fresh
install sees, since nothing writes the file until a client PUTs one. The client
spreads that response over its own defaults, so these values win on first run;
`textScale` in particular is a percent and must be `100`, not `1`.

PUT response (200): echoes the stored settings object bare. A legacy
`voiceClearPhrase: string` (singular) on disk is migrated to
`voiceClearPhrases: string[]` at load time.

---

## Voice / command eval (server-owned input evaluation)

### `POST /api/chat/eval`
The client performs NO local evaluation of typed/dictated text — every buffer
change is POSTed here; the server relays to the compiled eval engine
(`tools/cli/internal/evalengine`, run as `parlay eval serve`; `PARLAY_EVAL_ENGINE_URL`, default
`http://127.0.0.1:4343`) and broadcasts the computed actions to the owning
device as the `input_action` SSE event (which is the source of truth for
applying them — the synchronous response is informational).

Request body:
```jsonc
{
  "streamId": "eval-<device>-main",   // defaults to that pattern when omitted
  "version": 42,
  "text": "string",
  "cursor": { "anchor": 0, "active": 0 },
  "reason": "input" | "resync" | "…", // defaults "input"
  "voiceEnabled": true,
  "device": "device-uuid",            // REQUIRED — 200 {"error":"device required"} without it
  "tabs": [{ "id": "…", "name": "…", "nicknames": ["…"] }]
}
```
Responses:
```jsonc
// 200
{ "ok": true, "sseClients": 1, "v": 1, "streamId": "…", "seq": 7,
  "baseVersion": 42, "actions": [ … ], "engineEvalNs": 12345,
  "timing": { "engineEvalNs": 12345, "relayMs": 3 } }
// 502 — engine unreachable / bad engine response
{ "error": "engine unreachable: …" }
```

### `POST /api/chat/eval-push`
Down-channel for **server-owned submit fires**: the eval engine calls this
when its per-stream timer elapses; the server routes the fire to the device
that owns the stream and broadcasts `input_action` with
`timing.serverOwnedFire: true`.

Request: `{ "streamId": "…", "seq"?: N, "baseVersion"?: N, "v"?: N, "action"?: {…} }`.
Responses: 200 `{ "ok": true, "sseClients": N }`; **400** `streamId` missing;
**404** `{ "error": "unknown stream" }` when the stream→device mapping is
gone (engine restart, eviction) — the client re-registers on its next
keystroke. The Go server bounds the mapping at 4096 streams
(oldest-insertion eviction).

---

## TTS

### `POST /api/chat/tts`
Synthesize speech via the local speak daemon.

Request: `{ "text": "string (≤2000)", "voice"?: "…", "speed"?: 1.0 }`.
Success: binary **`audio/wav`** body. Errors: JSON `{ "error": "…" }` typed
`application/json`.

### `POST /api/chat/tts-correction`
Persist a pronunciation substitution. Request:
`{ "from": "string (≤100)", "to": "string (≤200)", "sentence"?: "…" }`.
Response (200): `{ "ok": true, "substitutions": N }` (total stored) or
`{ "error": "from and to required" | "from/to too long" | … }`.

### `POST /api/chat/tts-report`
Report a mispronounced sentence (🚩 button). Request:
`{ "sentence": "string (≤500)", "voice"?: "…", "clipMeta"?: { "source": "panel", "msgId": "string|null" } }`.
Response (200): `{ "ok": true }` or `{ "error": "sentence required" | … }`.
Appends to `tts-pronunciation-reports.jsonl` under the PAI dir.

### `POST /api/chat/tts-event`
Fan a TTS lifecycle event (readiness dots, playback state) out to every
listener. The body is free-form; the server stamps `ts` if absent and
broadcasts it as the `tts_event` SSE event. Response (200): `{ "ok": true }`.

### `POST /api/chat/tts/validate-splits`
Validate sentence-split quality for a block of text (LLM-assisted; JSON
content-type exempt for `curl -d` use). Request:
`{ "text": "string", "model"?: "…" }`. Responses: 200
`{ "blocks": [...], "evaluation": { "overall_score": N, "verdict": "…",
"issues": [...], "suggestion": "…" }, "model": "…", "ms": N }`; **400**
`text` missing; **502** when the evaluating model is unreachable. The current
implementation returns a placeholder evaluation (`verdict: "unknown"`,
`suggestion: "Ollama integration pending"`).

---

## Remote input

The Mac-side injection control plane: accepted text → real keystrokes via
Talon. Full contract (wire shapes, focus-first semantics, Talon adapter,
manual proof): [`docs/remote-input.md`](./remote-input.md) (which owns it —
summary here).

### `POST /api/chat/remote-input/submit`
Enqueue accepted text for injection — or, with `"mode": "bead"`,
capture it as a bead (no Talon path, no target needed; `bead_created` /
`bead_failed` outcomes, `store` names the capture queue, default
`inbox`). Request:
`{ "device": "string (required)", "text": "string (required)",
"app"?: "…", "windowTitle"?: "…", "trigger"?: "…",
"mode"?: "inject"|"bead", "store"?: "…",
"allowUnfocused"?: true, "dryRun"?: true, "confidence"?: 0.0..1.0 }`
(`?dryRun=1` / `?allowUnfocused=1` / `?mode=bead` / `?store=…` force the same modes without touching
the body; the body wins when both are set; dry-run runs the real focus + verification and reports
`wouldInsert`, typing nothing). A live inject submit with no `app` and no
`windowTitle` is refused (**400** naming `allowUnfocused`) unless the
caller deliberately sets `allowUnfocused`, which surfaces on the outcome
as `focus: "unfocused_allowed"` (target names come from `GET …/targets`
below — Talon `ui.apps()` names, not OS process names). Bead text is capped at 2000 chars (rejected, never truncated). Response:
**202** `{ "id": "ri-N", "status": "queued" }` (queued, not
done — the terminal outcome arrives via status poll or the
`remote_input_result` SSE event). Errors: **400** `device`/`text` missing;
**405** non-POST. All three routes are in the guard's `GuardedPaths`.

`confidence` is optional and additive: the recognition confidence the
submitting surface reported for `text`. Omitting it means *not reported*,
never *confident*. When the server has a hold threshold in force
(`PARLAY_INPUT_MIN_CONFIDENCE`, in [0,1]), a submission whose reported
confidence is below it is **held** rather than routed: **202**
`{ "id": "ri-N", "status": "held" }`, nothing is typed, the text is
preserved, and the terminal outcome carries `status: "held"` with an `error`
naming the threshold. An input with **no** reported confidence is never held —
a threshold that refused an absent value would refuse every surface that
cannot report one. Every submit, refusal and outcome is recorded in
[`input-seam.md`](input-seam.md)'s ledger (source `remote-input`).

### `GET /api/chat/remote-input/targets`
List Talon's applications in Talon's own `ui.apps()` ordering (200) or
**502** Talon unreachable. Per target: exact Talon `name` (round-trips
through focus verification), `focused`, relevant `windowTitle`,
`windowCount`, `hasWindows` (windowless apps stay listed). Read-only —
`?dryRun=1` is an accepted no-op echo. **405** non-GET.

### `GET /api/chat/remote-input/status?id=ri-N`
Poll one submission's latest `Outcome` (200) or **404** unknown id
(evicted or never submitted). Terminal `status`: `injected` |
`focus_failed` | `inject_failed` | `dry_run_passed` (dry-run success:
real focus + verification, nothing typed, `wouldInsert` carries the exact
bytes) | `bead_created` (capture success: `beadId`/`beadStore`/`beadWrapper`/`capturedText`) |
`bead_failed` (typed `error`, text preserved, never an id) | `held` (a reported
confidence fell below the configured threshold: nothing typed, text preserved);
transient: `queued` | `injecting`.
Parlay clears shared input state only on `injected` (never on `dry_run_passed`); on `focus_failed` it
preserves the text and strips `trigger`. **400** `id` missing; **405**
non-GET.

---

## Pages, plugins, version, UI bundle

### `GET /api/chat/pages`
List servable pages from `~/pulse-pages/` (every directory holding an
`index.html`, with its `<title>` for fuzzy search). 30s server-side cache.
Response: `{ "pages": [ { "tag": "dirname", "title": "…" } ] }`. Non-GET:
**405** plain text. A server-side watcher broadcasts `pages_patch`
`{ "added": [PageEntry], "removed": ["tag"] }` on changes.

### `GET /api/chat/plugins`
Installed plugin manifests, load-ordered (speak first — it wires the global
speech hooks). Response: a bare JSON array:
```jsonc
[ { "id": "speak", "version": "1.0.0", "minPanel": "3.7.0",
    "description": "…", "defaultEnabled": true },
  { "id": "cursorless", … } ]
```
The panel injects `/annotate/plugins/<id>.js?v=<version>` for each id matching
`^[a-z0-9-]+$`.

### `POST /api/chat/plugin/cursorless/rpc`
Talon-side entry of the Cursorless RPC bridge: relays an editor op to the
panel over SSE (`cursorless_rpc` `{ "rpcId", "op", "args" }`) and blocks until
the panel responds or 2.5s elapses. JSON-content-type exempt (out-of-repo
Python caller); still origin-guarded.

Request: `{ "op": "string", "args"?: any, "device"?: "uuid" }`. Responses
(always 200):
```jsonc
{ "ok": true, "result": … }
{ "ok": false, "error": "op required" | "panel did not respond (2.5s)" | "bad request" }
```
The server ignores a `device` field and broadcasts to all clients.

### `POST /api/chat/plugin/cursorless/response`
Panel-side reply leg. Request: `{ "rpcId": "…", "result": any }`. Response
(200): `{ "ok": true }` if a waiter was resolved, `{ "ok": false }` if the
rpcId was unknown/expired.

### `GET /api/chat/version`
Bundle version, polled on every SSE `connected` so a stale PWA tab
self-upgrades. Response: `{ "version": "string" }` (`"unknown"` = no-op).

---

## Live commands

The live-command registry: every running CLI verb reports itself so panels can
show what the fleet is doing. Full contract:
[`docs/live-commands.md`](./live-commands.md) (which owns it — summary here).
By design the registry stores **no free-form text**: verb, agent id, pid, flag
*names*, outcome token — never argv values, paths, or error strings.

The three report routes require POST **and** `Content-Type: application/json`
— anything else is **415** (`requireCommandReport`) — and all three are in the
origin guard's `GuardedPaths` (see [Origin guard](#origin-guard)), so a
cross-origin POST is **403** before the handler runs. `GET /api/chat/commands`
is the read half and is deliberately **unguarded**, on the `/api/chat/agents`
precedent.

### `GET /api/chat/commands`
```jsonc
{ "ok": true, "now": "iso", "running": N, "staleAfterMs": N,
  "commands": [ {
    "id": "…", "verb": "…", "agent"?: "…", "flags"?: ["--x"], "pid"?: 123,
    "state": "running"|"exited"|"failed"|"dropped",
    "startedAt": "iso", "updatedAt": "iso", "endedAt"?: "iso",
    "exitCode"?: 0, "outcome"?: "…", "durationMs": N } ] }
```
`"dropped"` is wire-only — computed at read time for a command whose
heartbeats stopped without a `command-end`.

### `POST /api/chat/command-start`
Request: `{ "id", "verb", "agent"?, "flags"?: ["…"], "pid"? }` → 200
`{ "ok": true, "id", "state": "running" }`.

### `POST /api/chat/command-heartbeat`
Request: `{ "id" }` → 200 `{ "ok": true, … }`; an unknown id answers
`{ "ok": false, "unknown": true }` (200 — the CLI stops heartbeating).

### `POST /api/chat/command-end`
Request: `{ "id", "state": "exited"|"failed", "exitCode"?, "outcome"? }` →
200 `{ "ok": true, "id", "state" }`.

State changes broadcast the `command_update` SSE event; the connect burst
includes a full `commands` snapshot.

---

## Command log and the off switch

The **command log** — one row per *evaluated string* (not per process, which is
the live-command registry above) — and the **off switch** that turns a
connection or an action off from wherever those rows are read. Full contract:
[`docs/action-log.md`](./action-log.md) (which owns it — summary here). Like the
registry, the log stores **no evaluated text**: identifiers, verb names, an
outcome token and timings only.

Both routes are in the origin guard's `GuardedPaths`. `/api/chat/off-switch` is
mutating on POST, which by the method-independent rule puts the GET on the same
path inside the boundary too; `/api/chat/action-log` is guarded because it hands
out device and command ids, and it is in `noGuardedCORSReads` so guarding it does
not newly reflect an `Access-Control-Allow-Origin` on a read that has never sent
one.

### `GET /api/chat/action-log`
Query (all optional): `source`, `inputAction`, `outputAction`, `outcome`,
`reason`, `device`, `since`, `until`, `limit`. `since`/`until` accept an RFC3339
timestamp **or** a duration (`15m`, `2h`, `-15m`) measured back from now.
```jsonc
{ "ok": true, "now": "iso", "total": N, "limit": N,
  "records": [ {
    "id": "act-N", "at": "iso", "source": "test-site"|"panel",
    "device"?: "…", "streamId"?: "…", "inputAction"?: "…",
    "outputActions": ["…"],
    "outcome": "delivered"|"queued"|"dropped"|"refused",
    "reason"?: "…", "relayMs"?: N, "engineEvalNs"?: N } ],
  "facets": { "sources": [], "inputActions": [], "outputActions": [],
              "outcomes": [], "reasons": [], "devices": [] },
  "targets": [ { "kind", "id", "by"?, "surface"?, "at" } ],
  "outcomeVocabulary": ["delivered","queued","dropped","refused"],
  "filterVocabulary": { "fields": [], "times": [] } }
```
The three companion fields are what make one fetch enough to render a working
filter bar: `facets` is every distinct value actually present, `targets` is what
is currently off, and `outcomeVocabulary` is the closed four including values
with no rows yet.

### `GET /api/chat/off-switch`
```jsonc
{ "ok": true, "now": "iso", "targets": [ …as in `targets` above… ],
  "kinds": ["connection","action"], "surfaces": ["website","cli","api"],
  "connections": N, "actions": N }
```

### `POST /api/chat/off-switch`
Request: `{ "kind": "connection"|"action", "id", "off": true|false,
"by"?, "surface": "website"|"cli"|"api" }` → 200
`{ "ok": true, "kind", "id", "off", "changed", "entry"?, "targets": [ … ] }`.
An unknown `kind` or an empty `id` is **400** and mutates nothing; a missing
`off` is **400** rather than a silent "turn it on".

`off:true` on a **connection** refuses that device's `/api/chat/eval` before the
relay reaches the engine, and refuses its pending server-owned fires on
`/api/chat/eval-push`; `off:true` on an **action** suppresses that command's
emitted actions after evaluation and refuses its already-armed submit timer.
Both are recorded in the log as `refused` with reason `off-connection` /
`off-action`. State is in-memory by design (the same reasoning as
`PresenceTracker` and the command registry, which have no on-disk form either).

---

## Debug / diagnostics

### `POST /api/chat/debug-log`
Batched client console errors/warnings + instrumented traces from the panel,
appended to a log file so a phone (no devtools) can be diagnosed by tailing
it. The Go server does not serve it — its route 404s, and the client treats
the 404 as "permanent no-op for the session" (confirmed, working degradation).
Guarded (origin + JSON
content-type). Disabled entirely with `PARLAY_DEBUG_LOG=0`; log path
overridable via `PARLAY_DEBUG_LOG_PATH`. Request
`{ "device", "ua", "url", "entries": [ { "ts", "level": "error"|"warn"|"trace", "source", "message", "detail"? } ] }`;
responses 204 (disabled/empty/success), 400 (invalid JSON), 500 (persist
failure to `$PARLAY_STATE_HOME/debug.log`). Fields truncated at 4000 chars,
50 entries per batch.

The `POST`/`GET /api/debug/input-timing` routes are legacy and **unserved** —
they existed only on the deleted TS server and there is no Go counterpart:
a request to them 404s.

### `GET /health`
Liveness + store sanity, outside `/api/chat`. Response:
`{ "ok": true, "messages": N, "agents": N }`. Non-GET: 405 plain text.

### Static assets
The server serves the built panel bundle standalone (no Pulse front door):
`/` (SPA fallback to `index.html`), `/annotate/<path>` (the Pulse symlink
convention, mapped onto the bundle root), and `/fleet/` (the
`packages/webview` fleet dashboard), from `PARLAY_ASSETS_DIR` (`-assets-dir`;
default: the first `packages/client/dist` found by walking up from the
executable's directory and then from the working directory, else a bare `dist`).
Dispatched
after all `/api/*` routes so it can never shadow them — and an unrouted
`/api/*` path stays a real 404, never the SPA fallback (the CLI's
`commandreport` caches that 404 to detect unsupported verbs). Source:
`internal/static`.

---

## SSE Events

### `GET /api/chat/events?device=<uuid>&after=<lastMsgId>&url=<pageUrl>&caps=<declaration>`
One persistent `EventSource` per tab. On any error the client closes and
reconnects with exponential backoff (1s → doubling, capped 30s).

Query params:
- `device` — client-generated localStorage uuid; enables device-scoped
  delivery (`navigate`/`reload`/`device_cmd`/`input_action` with a `device`
  target, cursorless RPC). The server accepts it without storing.
- `after` — last-seen message id. When resolvable, `history` in the connect
  burst is the delta after that id. When absent, history is windowed
  per-channel: the newest 50 per channel (`PER_CHANNEL`), except the channel
  owning the page named by `url` gets 200 (`OWNER_LIMIT`), merged and sorted
  by timestamp. An unresolvable `after` (evicted or never-existed id) also
  degrades to that windowed replay, and the `history` event is identical in
  shape either way — a client cannot tell delta from replay, so dedup by
  message id regardless.
- `caps` — url-encoded
  JSON interface-capability declaration, contract owned by
  [`docs/interface-capabilities.md`](./interface-capabilities.md) and the
  normative engine `tools/cli/internal/capability` (Go-server mirror:
  `packages/go-server/internal/capability`, sync-tested byte-identical
  against the engine). A declared connection only receives
  the presentation-command events (`navigate`, `reload`, `device_cmd`,
  `input_action`, `draft`) it lists under `accepts`; all other events are
  ungated, and a declaration can only *subtract* deliveries. No `caps` at all
  = legacy client, byte-identical full delivery. An **invalid** declaration
  is refused with **400 `{"error"}`** rather than falling back to legacy —
  fail-open would widen delivery against declared intent. Validation caps:
  8KB declaration, schema major must be 1, ≤64 accepts names
  (`^[a-z][a-z0-9_]{0,63}$`), ≤32 content/interaction tokens. Unrelated to
  `register-agent`'s free-form `caps` field, which is INPUT-direction agent
  metadata.

Connect burst, in order: `connected`, `history`, `agents`,
`agent_presence`, `presence_map`, then `commands` (live-command snapshot).
Keepalive comment frame every 5s (`: keep-alive`), plus
`X-Accel-Buffering: no` so buffering proxies cannot hold the keepalives.
The 5s cadence sits under the tightest idle budget observed on the client
path (the phone's down stream cycled open/drop every ~11s with the old 25s
interval, which could never fire before the reap) with >2x margin, and
likewise beats minute-scale NAT/DERP idle timeouts — an idle stream must
see traffic well before any of those fire.

Reconnect/resume contract: on any error the client closes and reconnects
with exponential backoff (1s → doubling, capped 30s), passing `after=`
with the last `message` id it holds so `history` in the connect burst is
the delta, not a full replay. `message` frames are the resume cursor
(`input_action` envelopes carry no id); dedup by message id regardless,
since the delta can overlap what the client already holds. The
`parlay-input` owned SSE (`packages/input/src/parlay-input/sse.ts`)
implements the same contract for hosts without a shared subscription.

| Event | Payload | Notes |
|---|---|---|
| `connected` | `{ "capabilities"?: { "schema", "recognized": [], "unknown": [] } }` | Resets client backoff; triggers the `/version` self-upgrade check. `capabilities` echoes the `?caps=` negotiation (which accepts names this server gates on vs. never heard of). |
| `history` | `ChatMessage[]` | Full, windowed, or delta history depending on `after`/`url`. |
| `agents` | `AgentInfo[]` | Full registry snapshot. |
| `agent_register` | `AgentInfo` | Single-agent upsert (explicit `register-agent`, auto-register on reply). |
| `agent_unregister` | `{ "id": "string" }` | Agent removed (unregister/DELETE/sweep). |
| `presence_map` | `Record<string, string>` (channel → status) | Vocabulary: `"online"`. |
| `message` | `ChatMessage` | The core new-message event. Deduped client-side by id. |
| `message_received` | `{ "id" }` | Delivery ack: a queued user message was polled → ◌→✓ pip. |
| `presence` | `{ "status": "string" }` | Thinking-dots indicator (`"thinking"`/`"idle"`). |
| `draft` | `{ "text", "clientId"? }` | Cross-device draft sync; self-echoes ignored via `clientId`. |
| `agent_presence` | `{ "active": boolean }` | ≥1 long-poll waiter connected — "agent away" banner. |
| `tool_event` | *(opaque producer payload)* | Tool-activity line; fed through the ingress (below) by the tool tailer. |
| `tts_event` | `{ "id", "role": "tts_event", "type", "device", …, "ts" }` | TTS lifecycle fan-out from `POST /tts-event`. |
| `remote_input_result` | `Outcome` (`{ "id", "device", "status", "focus"?, "injectAttempted", "preserveText"?, "stripTrigger"?, "error"?, "dryRun"?, "wouldInsert"?, "allowUnfocused"?, "mode"?, "beadId"?, "beadStore"?, "beadWrapper"?, "capturedText"? }`) | Terminal remote-input outcomes (`injected`/`focus_failed`/`inject_failed`/`dry_run_passed`/`bead_created`/`bead_failed`/`held`), device-scoped. See [`docs/remote-input.md`](./remote-input.md). |
| `lavish_session` | `{ "key", "file", "proxyUrl", "status" }` | Embedded-workspace card upsert. **Producer routes not wired** — see below. |
| `reload` | *(none)* | `location.reload()`. |
| `navigate` | `{ "url", "openDrawer" }` | Workspace navigation. Gated by capability declarations. |
| `input_action` | `ActionEnvelope` (below) | Eval engine's computed actions, device-scoped. Gated. |
| `device_cmd` | `{ "cmd", "args"? }` | See `POST /device-cmd`. Gated. |
| `pages_patch` | `{ "added"?: [PageEntry], "removed"?: ["tag"] }` | Page-nav picker updates. |
| `cursorless_rpc` | `{ "rpcId", "op", "args" }` | Cursorless bridge, server → panel leg. |
| `commands` | live-command snapshot (see `GET /commands`) | Connect burst only. |
| `command_update` | one command record | On every registry state change. |

Plugins may subscribe to additional event names via the client's
`onSse(event, handler)` shim; the table covers every name with a first-party
producer or subscriber. `commands`/`command_update` are owned by
[`docs/live-commands.md`](./live-commands.md).

### `POST /api/chat/events`
The external-producer ingress into the SSE hub, for a producer that cannot
live inside the server process. Its `GET` sibling above is the stream itself —
the path serves both.

Callers: the hook/tool tailer (Go), pushing `tool_event` against
`PARLAY_HUB_URL` (default `http://127.0.0.1:4242`, 5s timeout, per-route
ordered delivery chains that shed at 256 queued posts).

Request body:
```jsonc
{
  "event": "tool_event",   // required; must be in the ingress allowlist
  "data":  { }             // optional; forwarded to the wire byte-identical
}
```
Responses: 200 `{"ok": true, "event": "<echoed name>"}` (absent `data`
broadcasts `{}`); **400** `{"error": "event is required"}` /
`{"error": "event not accepted from an external producer: X"}`; **405**
(`Allow: GET, POST`) on other methods.

**The allowlist is one name per real producer** — `tool_event` alone today.
Since PR #164 it is no longer hand-written: at init it is derived from the
enrolled source contracts (`contracts/sources/`, embedded via
`internal/sourcecontracts`) as the union of `emits` across contracts with the
observability trust posture — widening it means landing a reviewed contract,
not editing a map. The refusal rosters below stay hard-coded in the handler;
no enrollment can admit one of those names.
Anything else is refused, including every name the server produces from its
own persisted state (`message`, `history`, `agents`, `agent_register`,
`message_received`, `presence_map`, `commands`, `command_update`), every
panel-aiming name with no producer in the repo (`navigate`, `reload`,
`device_cmd`, `input_action`, `draft`), and any unknown name. `system_update`
is refused too: it is a `ChatMessage.type` carried on `message`, not an event
name — a producer wanting one posts to
[`POST /api/chat/message`](#post-apichatmessage) with
`type: "system_update"`, which persists first and broadcasts as a
consequence. Rationale for each refusal is in
`packages/go-server/internal/handlers/events_ingress.go`'s doc comment, which
owns this contract.

This route is in the guard's `GuardedPaths`; see § Origin guard above for
what that means for the `GET` stream on the same path.

#### `input_action` envelope shape
```ts
interface ActionEnvelope {
  v: number
  streamId: string        // echoes the streamId from the triggering /eval POST
  seq: number
  baseVersion: number
  actions: Action[]
  timing?: { engineEvalNs?: number; relayMs?: number; serverOwnedFire?: boolean }
}
interface Action {
  verb: string
  args?: {
    start?: number; end?: number; text?: string; triggerText?: string
    tail?: boolean; requireTail?: string; timerId?: string; fireInMs?: number
    id?: string; kind?: 'info' | 'warn'; channel?: string; url?: string; reason?: string
    prompt?: string; channels?: PickerChannel[]; senders?: PickerSender[]
  }
}
```
Full verb semantics are out of scope for this doc — see
`docs/COMMAND_DESIGN_CONTRACT.md` and `docs/CHANNEL_PICKER_CONTRACT.md`.

### Gas City bus dual-write / consume (flags — not endpoints)
Behind default-off flags, the server can mirror its observability events
onto a Gas City event bus and consume bus events back into the hub:
`-bus-emit`/`PARLAY_BUS_EMIT` dual-writes exactly `message`,
`message_received`, `agent_register`, `command_update`, `tool_event`;
`-bus-consume`/`PARLAY_BUS_CONSUME` streams `parlay.*` bus events (minus this
server's own emissions) into the SSE hub with a persisted after-seq cursor.
Both flags off (the default) is byte-identical to a build without the bus.
No HTTP surface changes either way.

---

## Endpoints referenced but out of scope / not live

- **The relay control API** (`http://relay/register` etc. over a Unix socket
  at `$TMPDIR/parlay/relay.sock`) is a *local* control-plane protocol between
  the CLI and `tools/relay/parlay-relay` — not part of `/api/chat/*`.
- **`parlay status`** is pure local file I/O — no HTTP call at all.
- **`POST /api/events/bead-status`** is proposed, not built
  (`docs/CLI_VERBS_AND_EVENTS.md` §2.6).
- **`POST /api/lavish/claim` and `GET /lavish-proxy/...`** — the handlers
  lived in the deleted `packages/server/src/lavish.ts` and were never wired
  into the router, so the routes 404'd there and 404 here too. The
  `lavish_session` SSE event they would have fed has a client-side subscriber
  but no live producer. Noted so nobody "rediscovers" them as live routes.

---

## Divergences to fix (historical)

The TS↔Go divergence table lived here while both servers were live and a
portable caller had to tolerate both sides. The TS server was deleted with
the Bun→Go cutover (it diverged on: `connected` clientId, `presence_map`
vocabulary, poll timeout/shape/reset, `message_received` payload,
`register-agent` echo, `alert` no-target scope, `reply` minimum body, wrong
method and malformed-JSON handling, `subscribers`/`draft`/`settings`
response shapes, upload validation, `navigate` response field,
`declare-channel` echo, `/system` truncation rule, `tts`/`tts-event`/
`tts/validate-splits` behavior, cursorless `device` scoping, and the
`/api/chat/events` guard posture), so those rows and the "one of two
servers" framing no longer apply: every route in this doc now describes the
one implementation that remains. The two rows that were genuinely
partitioned surfaces — Go-only routes and TS-only routes (#29/#30) — are now
simply the route list above: the Go-only ones are served, the TS-only ones
(`/parlay-ui.js`, `/api/debug/input-timing`, `/api/chat/debug-log`) are gone
with the server.

---

## Open Gaps

1. **The server's TTS synthesis path is lightly exercised** — its speak
   daemon socket protocol (`tts_engine.go`) has not been verified against a
   live daemon end to end in this pass.
2. **`POST /api/chat/device-cmd` has no first-party POST call site in this
   repo** — the request shape is read from the server's handler (so it is
   accurate), but the producing callers are out-of-repo `curl`/agents.
3. **CLI `types.ts` archaeology**: the retired `packages/cli` typed
   `ChatMessage.type` as only `"alert"`. The server truth is
   `"alert" | "action_request" | "system_update"` (this doc and the OpenAPI
   file are correct; any surviving copy of the old type is stale).
4. **Auth/exposure.** No endpoint performs authentication, on either server,
   and that is deliberate — the surface trusts the network boundary
   (local/tailnet only). The enforced part is the cross-origin half (§ Origin
   guard). A caller that reaches the port with no `Origin` header is trusted
   everywhere.
