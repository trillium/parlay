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
> code that produces them, not inferred from consumers. The contract describes
> what the servers actually DO, warts included; known TS↔Go mismatches are
> collected in [Divergences to fix](#divergences-to-fix) rather than papered
> over.
>
> Base path for all REST/SSE routes: `/api/chat` (exceptions: `/health`,
> `/api/debug/*`, `/parlay-ui.js`, static assets). `CHAT_BASE` in
> `packages/client/src/config.ts` is the single client-side constant; the CLI
> resolves the server origin via `config.ServerURL()`
> (`tools/cli/internal/config/config.go`): `PARLAY_SERVER` env → persisted
> `~/.parlay/config.json` `"server"` key → `http://localhost:4242`. All
> request/response bodies are JSON unless noted.

Two implementations serve this surface:

- **TS server** — `packages/server`, a standalone Bun `serve()` app. The
  production instance.
- **Go server** — `packages/go-server` (`cmd/parlay-server`), the rewrite
  built against this contract.

Routes marked **(TS only)** or **(Go only)** exist on one implementation.
Everything else exists on both.

## Conventions

- **No authentication anywhere.** The surface trusts the network boundary
  (local/tailnet only — do not expose the port publicly). The enforced part of
  the trust model is the cross-origin half: see [Origin guard](#origin-guard-both-servers).
- **Two error conventions coexist** (verified in handler source, both servers
  deliberately match):
  - *App-error-on-200*: most write endpoints (`send`, `reply`, `alert`,
    `system`, `register-agent`, `declare-channel`, `draft` PUT, `eval`'s
    validation, tts family) return HTTP **200 with `{"error": "…"}`** on
    validation failure. Callers detect failure by the `error` field, not the
    status.
  - *Status-error*: `unregister` / `DELETE /agents/:id` (400/404),
    `POST /message` (400), `poll` (410), `eval` engine failures (502),
    `eval-push` (400/404), invalid `?caps=` (400), `tts/validate-splits`
    (400/502) return a **non-2xx with `{"error": "…"}`**.
- **Malformed JSON body**: the Go server returns **400
  `{"error": "invalid JSON body"}`** on every JSON route uniformly
  (`decodeJSON`). The TS server mostly returns 200 `{"error": "bad request"}`
  (each handler's own catch).
- **Wrong method**: the Go server answers **405** with an `Allow` header and a
  plain-text body `"<METHODS> only"` (`methodNotAllowed`) on every route. The
  TS server generally falls through to its **404** handler; its only explicit
  405 is `GET`-only `/api/chat/pages` (JSON `{"error": "GET only"}`).
- **CORS**: unguarded read routes carry `Access-Control-Allow-Origin: *` on
  the TS server; the Go server sends no ACAO on unguarded routes. Guarded
  routes reflect the single allowed origin (see Origin guard).

### Origin guard (both servers)

Unauthenticated does **not** mean unrestricted: the mutating and
identifier-aiming surface — the routes that write state, drive a device, or
hand out an identifier (device uuid, agent id) a hostile page could then aim
at a mutating route — sits behind an origin guard —
`packages/server/src/guard/` (route set in `guard/paths.ts`) and
`packages/go-server/internal/guard`. **Within that surface a route is guarded
by what its handler does, not by its HTTP method:** `GET
/api/chat/subscribers` and `GET /api/chat/poll` are both guarded, the first
because it discloses identifiers, the second because polling registers the
channel. On those routes:

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

Both sides also guard whole subtrees (`GUARDED_PREFIXES` /
`guardedPrefixes`): `/api/chat/agents/`, `/api/chat/plugin/`, `/api/debug/` —
anything added under those is guarded before its handler exists.

The read routes (`history`, `agents`, `version`,
`GET /api/chat/uploads/<name>`) are outside the guard and behave as documented
below. `/api/chat/events` is **guarded on the Go server and not on the TS
one** — the two servers deliberately differ here. `packages/go-server` serves
an external-producer ingress on `POST /api/chat/events` (see [SSE
Events](#sse-events) below), so the path is in `internal/guard.GuardedPaths`,
and because that classifier is method-independent the `GET` SSE stream is
guarded with it. No caller loses the stream: the panel is same-origin, and the
tailers, the CLI and curl send no `Origin`. Guarding a read path is **not** a
one-way tightening, though — it also reflects an `Access-Control-Allow-Origin`
back to every origin the guard *allows* (any loopback, `.local` or private-LAN
page), which on a stream that has never sent CORS headers would be new read
access. `noGuardedCORSReads` in `internal/guard/guard.go` suppresses it: `GET
/api/chat/events` answers with `Vary: Origin` and **no** CORS headers, so a
disallowed origin gets 403 and an allowed one gets a stream it still cannot
read cross-origin.

That surface is not purely read-only, and the boundary above is narrower than
"everything that writes or discloses". Two TS routes are **known, accepted,
deliberately unguarded residue** — accepted meaning somebody looked and
decided, not that nothing is exposed:

- `GET /api/chat/events` **on the TS server only** writes `sseClients` from an
  attacker-supplied `?device=` (`router-events.ts`), and the `tts_event` frames
  it streams carry that device uuid to every connected client
  (`router-tts-events.ts` broadcasts `{ …, device, ...body }` with no
  filtering), so a cross-origin `EventSource` can read it.
- `GET /api/chat/agents` (`router-messages.ts`) returns every registered agent
  id under `Access-Control-Allow-Origin: *` — the same class of disclosure
  `GET /api/chat/subscribers` was guarded for.

Both are tracked separately as `identifier-disclosure-remains-on-sse`; they
were ruled out of the guard's scope, not overlooked. What keeps the residue
from chaining is that every route that *aims* anything (`eval`, `draft`,
`device-cmd`, `navigate`, `reload`, `poll`, `upload`, `subscribers`) is
guarded. The Go server does not expose it the same way: its unguarded routes
send no `Access-Control-Allow-Origin` at all, so a foreign page's read still
executes but its body stays unreadable, and its `/api/chat/events` is guarded
outright (with the same no-ACAO posture preserved on the stream) and accepts
`?device=` without storing it.

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
{ "error": "bad request" }       // TS: unparseable body
```
Broadcasts `message` (and `presence {status:"thinking"}` on TS) over SSE and
wakes matching long-pollers.

### `POST /api/chat/reply`
An agent replies on **its own** channel. Identity comes from `agent` in the
body; on the TS server, when `agent` is absent the handler falls back to the
caller's environment/session context (`agent-context.ts`'s
`loadAgentContext`). The Go server requires `agent` in the body.

Request body:
```jsonc
{
  "text": "string",     // required on Go; TS accepts empty text when action or images present
  "agent": "agent-id",  // channel this posts to (see fallback above)
  "name": "string",     // optional display-name upsert
  "color": "#rrggbb",   // optional (TS persists on auto-register; Go accepts but does not persist)
  "action": { "kind": "navigate"|"switch_tab", "url"?: "…", "channel"?: "…", "label": "…" },  // TS only; unknown kind → {error}
  "images": ["url"]     // TS only
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
**Broadcast-to-all differs by server:** TS's "all" is the global (no-channel)
history **plus** every registered agent; Go's is every registered agent only.
On both, an explicit **empty array** delivers to nobody.

### `POST /api/chat/system`
Post a system line onto the dedicated `"system"` channel
(`type: "system_update"` — the panel renders a muted line and skips TTS).

Request body:
```jsonc
{ "text": "string", "source": "string?", "meta": { }? }
```
`text` is truncated to 500 characters (TS truncates UTF-16 code units, Go
truncates runes — differs only on astral-plane text). Response (200):
`{ "ok": true, "id": "msg-id" }` or `{ "error": "text required" }`.

### `POST /api/chat/message` (Go only)
Lower-level persist-and-broadcast: stores the message and broadcasts the
resulting `message` SSE event. The out-of-process seam for producers that
cannot live in the server process (`parlay supervise` digests, the PAI hook
tailer via `packages/server/src/hub-ingress.ts`'s `postHubMessage`). **The TS
server has no such route — it 404s.**

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
Recent chat history, oldest first. `limit` defaults to 200; non-numeric or
non-positive values fall back to the default.

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

### `GET /api/chat/poll?after=<lastId>&channel=<agentId>`
Agent long-poll. Blocks until a message for the channel arrives or the
server-side timeout elapses (**TS: 30s, Go: 25s**), then returns exactly one
of:

```jsonc
{ "timeout": true }
// or (TS) — the full ChatMessage
{ "id": "…", "role": "user", "ts": "…", "text": "…", ... }
// or (Go) — a subset
{ "id": "…", "role": "user", "text": "…", "from": "…?", "cursorReset": true?, "skipped": N? }
```

- **410 Gone** `{ "error": "…", "gone": true }` (TS) when the channel is
  tombstoned (unregistered via `agent-down`) — a dead agent must not re-create
  itself by polling. Go answers tombstones the same way at the store level.
- On the TS server, polling an **unknown, non-tombstoned** channel
  auto-registers it (name = id, color `#6b7280`), persists it, and broadcasts
  `agent_register`.
- Delivering a queued user message marks it received and broadcasts
  `message_received` (payload `{ "id", "channel"? }` on TS; `{ "id" }` on Go).
- A poll with no `after` waits for the *next* message only (no replay).

`cursorReset`/`skipped` are **Go-server only**; the TS server never emits
them. They appear only when `after` names a message the server cannot resolve
among the channel's retained messages — a truncated or rotated store, a
cursor from a previous server run, or a cursor belonging to a different
channel. Rather than silently delivering nothing, the server resumes from the
newest `min(50, retained)` messages on that channel (`DefaultReplayMax`,
mirroring the relay's `PARLAY_REPLAY_MAX`) and says so: `cursorReset: true`,
with `skipped` counting the older retained messages left outside that window.
The reset frame carries the oldest message of that window, so the caller's
next `after` resolves normally. A resolvable cursor never sets either field.

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
Response (200): **TS** echoes the stored entry — `{ "ok": true, "id", "name",
"color", "nicknames"?, "urls"?, "path"? }`. **Go** returns only
`{ "ok": true, "nicknames"? }`. Errors: `{ "error": "id required" | "bad request" }`.
Broadcasts `agent_register` with the stored `AgentInfo`.

### `POST /api/chat/unregister`
Deregister an agent's channel. **Status-error convention**: **400**
`{"error": "id required"}`, **404** `{"error": "…"}` on unknown id.

Request: `{ "id": "agent-id" }`. Success (200): `{ "ok": true, "id": "…" }` on
both servers. Broadcasts `agent_unregister`.

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
Connection/presence/memory snapshot. Guarded (identifier disclosure).

TS response (Go serves a subset — see [Divergences](#divergences-to-fix)):
```jsonc
{
  "parlay":     { "clients": 2 },                       // connected SSE clients
  "poll":       { "count": 1, "channels": [ { "channel": "id|null", /* + AgentInfo fields when registered */ } ] },
  "registered": { "count": 3, "agents": [ /* AgentInfo */ ] },
  "presence":   [ { "channel": "id", /* + AgentInfo fields */, "listening": true, "lastSeen": "iso|null", "status": "listening"|"idle"|"offline" } ],
  "presence_broadcasts": 12,                            // TS only
  "capability_suppressed": { "navigate": 3 },           // TS only — gated event → deliveries suppressed
  "capability_declarations": [ { "surface": { "kind": "panel", "instance"?: "…" }, "accepts": ["…"], "content": ["…"], "interactions": ["…"], "connectedAt": "iso", "device"?: "uuid" } ],  // TS only — one entry per declared SSE connection (device-identified or not), all three axes
  "devices": [ { "device": "uuid", "ua": "…", "connectedAt": "iso", "surface"?: {...}, "accepts"?: ["…"] } ],  // TS only
  "memory":  { "rssMB": 0, "heapUsedMB": 0, "externalMB": 0, "arrayBuffersMB": 0 },  // TS only
  "history": { "count": 0, "approxBytes": 0, "approxKB": 0, "ssePerConnectKB": 0 }   // TS only
}
```
A channel is `"listening"` if it long-polled within the last 35s
(`LISTEN_WINDOW_MS`), `"idle"` if it has polled before but not recently,
`"offline"` if registered but never polled.

### `POST /api/chat/declare-channel`
Bind a session id to a channel (used by hooks to attribute system lines).

Request: `{ "session_id": "s-1", "channel": "agent-id" }`. Response (200):
`{ "ok": true, "session_id": "…", "channel": "…" }` or
`{ "error": "session_id and channel required" }`. The TS server echoes the
*requested* channel; the Go server's declarations are sticky per session and
it echoes the *effective* channel, which can differ from the request.

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
Response (200): TS `{ "ok": true, "clients": N, "url", "openDrawer": bool,
"device"?: "…" }`; Go names the response field `open_drawer`. Error:
`{ "error": "url required" }`. Broadcasts `navigate`
`{ "url", "openDrawer" }` (both servers use `openDrawer` on the wire).

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
Response: TS `{ "text": "string" }`. Go returns the whole stored draft:
`{ "text", "clientId"?, "updatedAt"? }` — extra fields are harmless to the
one consumer, which reads `text`.

### `PUT /api/chat/draft`
Save (or clear, with `text: ""`) the shared input draft.

Request: `{ "text": "string", "clientId": "uuid"? }` — `clientId` is a
per-page-load id the client uses to ignore its own `draft` SSE echo.
Response (200): TS `{ "ok": true }` (or `{ "error": "bad request" }`); Go
echoes the saved draft object instead. Broadcasts `draft`
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
Failures: TS returns 200 `{ "error": "file field required" | "too large (10MB max)"
| "images only (png/jpg/gif/webp/svg)" | "bad request" }`; Go returns a bare
`{ "ok": false }` with no error field (its callers only check `ok`/`url`).
TS accepts by MIME type or filename extension; Go sniffs the actual bytes
(`http.DetectContentType`) and ignores the claimed type.

### `GET /api/chat/uploads/<name>`
Serve an uploaded image inline. Unguarded read.

- TS: `<name>` must match `^[a-z0-9]+\.(png|jpg|gif|webp|svg)$` else **400**
  (plain text `bad name`); **404** plain text `not found`; success carries
  `Content-Disposition: inline` and
  `Cache-Control: public, max-age=31536000, immutable` (content-addressed).
- Go: no name regex (store lookup instead); Content-Type is sniffed from the
  file bytes, not the extension; 404 on unknown.

On disk: `~/exchange/parlay-uploads/<name>` (TS canonical mapping — agents may
read that path directly).

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
  textScale: number        // clamped 85–160 on PUT (TS)
  voiceSettleMs: number    // clamped 0–3000 on PUT (TS)
  noKeyboardMode: boolean  // Go server + client only — ABSENT from the TS server's interface (a TS PUT drops it); see Divergences
}
```
PUT response (200): TS `{ "ok": true, "settings": { …stored } }` (or
`{ "error": "…" }`); Go echoes the stored settings object bare, with no
`ok` wrapper. A legacy `voiceClearPhrase: string` (singular) on disk is
migrated to `voiceClearPhrases: string[]` at load time on both servers.

---

## Voice / command eval (server-owned input evaluation)

### `POST /api/chat/eval`
The client performs NO local evaluation of typed/dictated text — every buffer
change is POSTed here; the server relays to the compiled eval engine
(`packages/eval-engine`, `PARLAY_EVAL_ENGINE_URL`, default
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
Success: binary **`audio/wav`** body. Errors: JSON `{ "error": "…" }` — on
the TS server the error body is (wart) still typed `audio/wav`, and the panel
sniffs the RIFF magic to tell audio from error; the Go server types errors
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
broadcasts it as the `tts_event` SSE event. **TS additionally resolves every
pending long-poll waiter with the event** (agents see TTS state); the Go
server broadcasts to SSE only. Response (200): `{ "ok": true }`.

### `POST /api/chat/tts/validate-splits`
Validate sentence-split quality for a block of text (LLM-assisted; JSON
content-type exempt for `curl -d` use). Request:
`{ "text": "string", "model"?: "…" }`. Responses: 200
`{ "blocks": [...], "evaluation": { "overall_score": N, "verdict": "…",
"issues": [...], "suggestion": "…" }, "model": "…", "ms": N }`; **400**
`text` missing; **502** when the evaluating model is unreachable (TS). The Go
implementation currently returns a placeholder evaluation
(`verdict: "unknown"`, `suggestion: "Ollama integration pending"`).

---

## Pages, plugins, version, UI bundle

### `GET /api/chat/pages`
List servable pages from `~/pulse-pages/` (every directory holding an
`index.html`, with its `<title>` for fuzzy search). 30s server-side cache.
Response: `{ "pages": [ { "tag": "dirname", "title": "…" } ] }`. Non-GET:
**405** (TS: JSON `{ "error": "GET only" }`; Go: plain text). A server-side
watcher broadcasts `pages_patch` `{ "added": [PageEntry], "removed": ["tag"] }`
on changes.

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
{ "ok": false, "error": "no client for device <uuid>" }   // TS only — device-scoped delivery; Go ignores "device" and broadcasts to all
```

### `POST /api/chat/plugin/cursorless/response`
Panel-side reply leg. Request: `{ "rpcId": "…", "result": any }`. Response
(200): `{ "ok": true }` if a waiter was resolved, `{ "ok": false }` if the
rpcId was unknown/expired.

### `GET /api/chat/version`
Bundle version, polled on every SSE `connected` so a stale PWA tab
self-upgrades. Response: `{ "version": "string" }` (`"unknown"` = no-op).

### `GET /parlay-ui.js` (TS only)
The embeddable panel loader, served `application/javascript`. Not under
`/api/chat`.

---

## Live commands (Go only)

The live-command registry: every running CLI verb reports itself so panels can
show what the fleet is doing. Full contract:
[`docs/live-commands.md`](./live-commands.md) (which owns it — summary here).
By design the registry stores **no free-form text**: verb, agent id, pid, flag
*names*, outcome token — never argv values, paths, or error strings.

The three report routes require POST **and** `Content-Type: application/json`
— anything else is **415** (`requireCommandReport`). TS serves none of these
routes (a TS 404 on `/api/chat/commands` is how the CLI's `commandreport`
detects an unsupported server and caches the 404 for 1h).

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

## Debug / diagnostics

### `POST /api/chat/debug-log` (documented, NOT WIRED)
The handler exists (`packages/server/src/debug-log.ts`) but has never been
registered in `router.ts` on either server — the route 404s, and the client
treats the 404 as "permanent no-op for the session" (confirmed, working
degradation). Shapes, for whenever it is wired: request
`{ "device", "ua", "url", "entries": [ { "ts", "level": "error"|"warn"|"trace", "source", "message", "detail"? } ] }`;
responses 204 (disabled/empty/success), 400 (invalid JSON), 500 (persist
failure to `$PARLAY_STATE_HOME/debug.log`). Fields truncated at 4000 chars,
50 entries per batch.

### `POST /api/debug/input-timing` (TS only)
Mobile keystroke-latency telemetry (no devtools on a phone). Guarded (under
`/api/debug/` prefix). Request:
`{ "device": "string (≤40)", "ua"?: "string (≤200)", "samples": [ { "costMs": N, "sinceLastMs": N } ] }`
→ 200 `{ "ok": true, "stored": N }`. Ring buffer of 200 samples per device.

### `GET /api/debug/input-timing` (TS only)
Per-device digest over the last 10 minutes:
`{ "<device>": { "ua": "…", "samples": N, "cost": { "p50": N, "p95": N, "max": N }, "cadence": { "p50": N, "p95": N } | null } }`.

### `GET /health` (Go only)
Liveness + store sanity, outside `/api/chat`. Response:
`{ "ok": true, "messages": N, "agents": N }`. Non-GET: 405 plain text.

### Static assets (Go only)
The Go server also serves the built panel bundle: `/` (SPA fallback to
`index.html`) and `/fleet/` (the `packages/webview` fleet dashboard), from
`-assets-dir`/`PARLAY_ASSETS_DIR`. Registered after all `/api/*` routes so it
can never shadow them. (In the TS deployment, static serving belongs to
Pulse, not this server.)

---

## SSE Events

### `GET /api/chat/events?device=<uuid>&after=<lastMsgId>&url=<pageUrl>&caps=<declaration>`
One persistent `EventSource` per tab. On any error the client closes and
reconnects with exponential backoff (1s → doubling, capped 30s).

Query params:
- `device` — client-generated localStorage uuid; enables device-scoped
  delivery (`navigate`/`reload`/`device_cmd`/`input_action` with a `device`
  target, cursorless RPC). TS stores it on the connection; Go accepts it
  without storing.
- `after` — last-seen message id. When resolvable, `history` in the connect
  burst is the delta after that id. When absent (TS), history is windowed
  per-channel: the newest 50 per channel (`PER_CHANNEL`), except the channel
  owning the page named by `url` gets 200 (`OWNER_LIMIT`), merged and sorted
  by timestamp. An unresolvable `after` (evicted or never-existed id) also
  degrades to that windowed replay, and the `history` event is identical in
  shape either way — a client cannot tell delta from replay, so dedup by
  message id regardless.
- `caps` — **TS server only** (Go parity is a tracked follow-up): url-encoded
  JSON interface-capability declaration, contract owned by
  [`docs/interface-capabilities.md`](./interface-capabilities.md) and the
  normative engine `tools/cli/internal/capability` (TS mirror:
  `packages/server/src/capability.ts`). A declared connection only receives
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

Connect burst, in order — TS: `connected`, `history`, `agents`,
`agent_presence`, `presence_map`. Go adds `commands` (live-command snapshot)
to the burst. Keepalive comment frame every 25s (TS `: ka`, Go
`: keep-alive`).

| Event | Payload | Notes |
|---|---|---|
| `connected` | TS: `{ "clientId": "uuid", "capabilities"?: { "schema", "recognized": [], "unknown": [] } }` · Go: `{}` | Resets client backoff; triggers the `/version` self-upgrade check. `capabilities` echoes the `?caps=` negotiation (which accepts names this server gates on vs. never heard of). |
| `history` | `ChatMessage[]` | Full, windowed, or delta history depending on `after`/`url`. |
| `agents` | `AgentInfo[]` | Full registry snapshot. |
| `agent_register` | `AgentInfo` | Single-agent upsert (registration, auto-register on reply/poll). |
| `agent_unregister` | `{ "id": "string" }` | Agent removed (unregister/DELETE/sweep). |
| `presence_map` | `Record<string, string>` (channel → status) | TS vocabulary: `"listening"`/`"idle"` (35s window, 10s sweep, broadcast on change only). Go: `"online"`. |
| `message` | `ChatMessage` | The core new-message event. Deduped client-side by id. |
| `message_received` | `{ "id", "channel"? }` | Delivery ack: a queued user message was polled → ◌→✓ pip. Go omits `channel`. |
| `presence` | `{ "status": "string" }` | Thinking-dots indicator (`"thinking"`/`"idle"`, emitted around send/reply on TS). |
| `draft` | `{ "text", "clientId"? }` | Cross-device draft sync; self-echoes ignored via `clientId`. |
| `agent_presence` | `{ "active": boolean }` | ≥1 long-poll waiter connected — "agent away" banner. |
| `tool_event` | *(opaque producer payload)* | Tool-activity line; fed through the Go ingress (below) by the tool tailer. |
| `tts_event` | `{ "id", "role": "tts_event", "type", "device", …, "ts" }` | TTS lifecycle fan-out from `POST /tts-event`. |
| `lavish_session` | `{ "key", "file", "proxyUrl", "status" }` | Embedded-workspace card upsert. **Producer routes not wired** — see below. |
| `reload` | *(none)* | `location.reload()`. |
| `navigate` | `{ "url", "openDrawer" }` | Workspace navigation. Gated by capability declarations. |
| `input_action` | `ActionEnvelope` (below) | Eval engine's computed actions, device-scoped. Gated. |
| `device_cmd` | `{ "cmd", "args"? }` | See `POST /device-cmd`. Gated. |
| `pages_patch` | `{ "added"?: [PageEntry], "removed"?: ["tag"] }` | Page-nav picker updates. |
| `cursorless_rpc` | `{ "rpcId", "op", "args" }` | Cursorless bridge, server → panel leg. |
| `commands` | live-command snapshot (see `GET /commands`) | Go connect burst only. |
| `command_update` | one command record | Go only; on every registry state change. |

Plugins may subscribe to additional event names via the client's
`onSse(event, handler)` shim; the table covers every name with a first-party
producer or subscriber. `commands`/`command_update` are owned by
[`docs/live-commands.md`](./live-commands.md).

### `POST /api/chat/events` (Go only)
The external-producer ingress into the Go SSE hub, for a producer that cannot
live inside that server process. The TS server has no such route — its
`/api/chat/events` is `GET`-only.

Callers: `packages/server/src/tool-tailer.ts`, via
`packages/server/src/hub-ingress.ts` (`pushHubEvent`; target
`PARLAY_HUB_URL`, default `http://127.0.0.1:4242`, 5s timeout, per-route
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
[`POST /api/chat/message`](#post-apichatmessage-go-only) with
`type: "system_update"`, which persists first and broadcasts as a
consequence. Rationale for each refusal is in
`packages/go-server/internal/handlers/events_ingress.go`'s doc comment, which
owns this contract.

This route is in the Go guard's `GuardedPaths`; see § Origin guard above for
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

### Gas City bus dual-write / consume (Go only, flags — not endpoints)
Behind default-off flags, the Go server can mirror its observability events
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
- **`POST /api/lavish/claim` and `GET /lavish-proxy/...`** — handlers exist
  in `packages/server/src/lavish.ts` but are **not imported by
  `index.ts`/`router.ts`: the routes 404**. The `lavish_session` SSE event
  they would feed has a client-side subscriber but no live producer. Dead
  code, documented so nobody "rediscovers" it as a live route.
- **`POST /api/chat/debug-log`** — handler written, never wired (see Debug).

---

## Divergences to fix

Every known TS↔Go behavioral mismatch, in one place. "Fix" here means either
converging the implementations or promoting the difference to a documented
feature — until then, a portable caller must tolerate both sides.

| # | Route / area | TS server | Go server |
|---|---|---|---|
| 1 | `connected` SSE payload | `{ clientId, capabilities? }` | `{}` — no clientId |
| 2 | `?caps=` capability gate | Implemented (400 on invalid, gated delivery, suppression counters) | Not implemented; param ignored |
| 3 | `presence_map` vocabulary | `"listening"` / `"idle"` | `"online"` |
| 4 | Poll timeout | 30s | 25s |
| 5 | Poll message shape | full `ChatMessage` | `{ id, role, text, from? }` subset |
| 6 | Poll unresolvable cursor | silent (never emits reset) | `cursorReset`/`skipped`, 50-message window |
| 7 | `message_received` payload | `{ id, channel? }` | `{ id }` |
| 8 | `register-agent` response | `{ ok, …stored entry }` | `{ ok, nicknames? }` |
| 9 | `unregister` success body | `{ ok, id }` | `{ ok, id }` — converged (was `{ ok }`); row number retired, not reused |
| 10 | `alert` with no targets | global channel + all registered agents | registered agents only |
| 11 | `reply` minimum body | empty text OK with action/images; agent falls back to env/context | text AND agent required |
| 12 | Wrong method | 404 fallthrough (except `/pages` 405 JSON) | 405 + `Allow` + plain text, everywhere |
| 13 | Malformed JSON body | 200 `{ error: "bad request" }` (mostly) | 400 `{ error: "invalid JSON body" }` uniformly |
| 14 | `subscribers` fields | full (memory, history, devices, presence_broadcasts, capability_suppressed) | subset: parlay/poll/registered + thin presence `{ channel, lastSeen }` |
| 15 | `PUT /draft` response | `{ ok: true }` | echoes saved draft object |
| 16 | `GET /draft` response | `{ text }` | `{ text, clientId?, updatedAt? }` |
| 17 | `PUT /settings` response | `{ ok, settings }` | stored settings bare, no `ok` |
| 18 | `noKeyboardMode` setting | absent from server interface — a PUT through TS drops it | present, round-trips |
| 19 | `upload` failure body | 200 `{ error: "…" }` (message per cause) | 200 `{ ok: false }`, no message |
| 20 | Upload acceptance check | MIME type or filename extension | content sniffing of actual bytes |
| 21 | `navigate` response field | `openDrawer` | `open_drawer` (SSE payload is `openDrawer` on both) |
| 22 | `declare-channel` echo | echoes requested channel | sticky per-session; echoes *effective* channel |
| 23 | `/system` truncation | 500 UTF-16 code units | 500 runes |
| 24 | `tts` error Content-Type | `audio/wav` (panel sniffs RIFF) | `application/json` |
| 25 | `tts-event` fan-out | SSE broadcast **and** resolves all poll waiters | SSE broadcast only |
| 26 | `tts/validate-splits` | live LLM evaluation | placeholder (`verdict: "unknown"`) |
| 27 | cursorless `rpc` `device` field | device-scoped; `no client for device X` error | ignored; broadcasts to all |
| 28 | `GET /api/chat/events` guard | unguarded (accepted residue) | guarded (no-ACAO stream) |
| 29 | Go-only routes | — | `POST /events`, `POST /message`, `/commands` + 3 report routes, `/health`, static `/` + `/fleet/` |
| 30 | TS-only routes | `/parlay-ui.js`, `/api/debug/input-timing` | — |

---

## Open Gaps

1. **The Go server's TTS synthesis path is lightly exercised** — its speak
   daemon socket protocol (`tts_engine.go`) mirrors the TS one but has not
   been verified against a live daemon end to end in this pass.
2. **`POST /api/chat/device-cmd` has no first-party POST call site in this
   repo** — the request shape is read from both servers' handlers (so it is
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
