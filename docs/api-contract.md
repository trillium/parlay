# Parlay HTTP API contract

> **How this doc was written, and what that means for trusting it.** It was
> reconstructed from the **client** (`packages/client/src/*`) and **CLI** call
> sites at a time when `packages/server/src/` could not be read from a checkout —
> the directory was a broken self-referential symlink loop (fixed since; see the
> project `CLAUDE.md`). So **response shapes below are inferred from how callers
> consume them, not read from the handler source.** They have held up in practice
> and this is the spec both server implementations are built against, but where a
> shape is under-determined it is called out in [Open Gaps](#open-gaps) rather
> than guessed at. The handler source is readable again — prefer it when the two
> disagree, and correct this doc when they do.
>
> Base path for all REST/SSE routes: `/api/chat`. `CHAT_BASE` in
> `packages/client/src/config.ts` is the single client-side constant; the CLI
> resolves the server origin via `config.ServerURL()`
> (`tools/cli/internal/config/config.go`, and its retired TS predecessor
> `serverUrl()` in `packages/cli/src/config.ts`): `PARLAY_SERVER` env →
> persisted `~/.parlay/config.json` `"server"` key → `http://localhost:4242`.
> All request/response bodies are JSON unless noted.

## Conventions

- **Transport errors** (server unreachable, non-2xx where the caller checks
  `res.ok`): the CLI's `getJSON`/`postJSON` (`packages/cli/src/http.ts`) call
  `die()` on any non-2xx, printing `GET/POST <path> failed: <status>
  <statusText>` and exiting non-zero. This is the CLI's uniform transport-error
  path for every endpoint it calls.
- **App-level errors on 200**: several endpoints (`register-agent`, `reply`,
  `send`, `alert`) are consumed by checking a `{ error?: string }` field on an
  **otherwise-successful** response, not by checking HTTP status — e.g.
  `cmdSend` in `commands.ts` does `if (r.error) return die(...)` after a
  successful `postJSON`. Whether the server actually returns 200 with
  `{error}` or a non-2xx with a JSON error body for these routes is **not
  verifiable from the client alone** — flagged in Open Gaps.
- **`/api/chat/unregister`** is documented (in `commands-agent-down.ts`) as
  failing loud with a *non-2xx* status on an unknown/already-gone id — a
  different convention from the group above.
- No auth headers are ever attached by any client or CLI call site. The one
  readable server file (`debug-log.ts`) says outright: *"trusts the network
  boundary (local/tailnet only — do not expose this port publicly) rather
  than authenticating requests."* Treat this as true for the whole surface
  unless proven otherwise.

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
Send a message into a channel from the panel UI, the CLI, or an agent.

Callers: `packages/client/src/input.ts` (`sendMsg`), `packages/cli/src/commands.ts` (`cmdSend`), `packages/client/src/settings-modal/debug.ts` (debug snapshot), `packages/cli/src/lavish-import.ts`.

Request body:
```jsonc
{
  "text": "string",              // required (or images present)
  "toAgent": "agent-id",         // optional — target channel; omitted = server-resolved default
  "images": ["url", "url"],      // optional — pending-upload URLs from POST /upload
  "from": "display name"         // optional — sender attribution override (cmdSend only)
}
```
Response (client reads `r.ok`; CLI reads `r.id`/`r.error`):
```jsonc
{ "ok": true, "id": "msg-id" }
// or
{ "error": "string" }
```
Client-side timeout: `AbortSignal.timeout(10_000)` — a stalled send never wedges the composer (input.ts:130-151).

### `POST /api/chat/reply`
An agent replies on **its own** channel. Identity comes from `agent` in the
body (the CLI wrapper — `parlay say`/`reply` — fills this from
`PARLAY_AGENT_ID`), not from any session/auth state.

Callers: `packages/cli/src/commands-identity/say.ts` (`cmdSay`), `packages/cli/src/listen.ts` (announce step), `bin/parlay-spawn` (hello message), `packages/cli/src/lavish-import.ts`, `packages/cli/src/lavish-poll.ts`.

Request body:
```jsonc
{
  "text": "string",     // required
  "agent": "agent-id",  // required — which channel this posts to
  "name": "string",     // optional (parlay-spawn's hello message only)
  "color": "#rrggbb"    // optional (parlay-spawn's hello message only)
}
```
Response:
```jsonc
{ "ok": true, "id": "msg-id" }
// or
{ "error": "string" }
```

### `POST /api/chat/alert`
Broadcast a message to multiple channels at once.

Caller: `packages/cli/src/commands.ts` (`cmdAlert`).

Request body:
```jsonc
{ "text": "string", "agents": ["agent-id", "..."] }  // "agents" omitted = broadcast to all
```
Response:
```jsonc
{ "ok": true, "channels": 3, "delivered": 2 }
// or
{ "error": "string" }
```
`channels` = channels the alert was recorded against; `delivered` = live pollers that received it immediately (see Open Gaps — exact semantics unverified).

### `POST /api/chat/message`
Lower-level message post: it persists the message and broadcasts the resulting
`message` event. Used by `parlay supervise` to relay a daemon-authored digest
onto an agent's channel on the agent's behalf (not a captain/user message), and
by the PAI hook tailer to put a hook firing on the panel from outside the
server process.

Callers: `packages/cli/src/commands-supervise.ts` (`postToRelay`),
`packages/server/src/hook-tailer.ts` (via `hub-ingress.ts`'s `postHubMessage`,
which targets the Go server).

Request body — `channel`, `role` and `text` are what `supervise` sends;
`type`, `source` and `meta` are the passthrough fields the hook tailer adds, all
optional and all stored on the resulting `ChatMessage` as-is:
```jsonc
{
  "channel": "agent-id",
  "role":    "agent",
  "text":    "string",
  "type":    "system_update",   // ChatMessage.type — NOT an SSE event name.
                                // This is the field that makes the panel render
                                // a muted system line and skip TTS; drop it and
                                // the hook firing becomes an ordinary agent
                                // bubble that is spoken aloud
  "source":  "SessionStart",    // label printed on that muted line; drop it and
                                // the line reads "system" and is otherwise
                                // unchanged
  "meta":    { "session_id": "s-1" }
}
```
Response: `{"ok": true, "id": "..."}` — the id the server assigned to the stored
message, and nothing else; the stored message is not echoed back. `supervise`
does not parse it — only `res.ok` is checked.

### `GET /api/chat/history?limit=N`
Recent chat history, newest presumably last (consumers iterate in order and
call `[...msgs].reverse()` to search backward).

Callers: `packages/cli/src/commands.ts` (`cmdHistory`, `cmdStats`, `cmdStatus`, `cmdDrawdown`).

Response: `ChatMessage[]`
```ts
interface ChatMessage {
  id: string
  role: "user" | "agent"
  ts: string          // ISO timestamp
  text: string
  channel?: string
  type?: "alert"       // cli/types.ts only lists "alert"; the client (thread.ts)
                        // also handles "system_update" and "action_request" — see Open Gaps
  source?: string      // set by POST /api/chat/message; the label the client
                        // prints on a system_update line (thread.ts falls back
                        // to "system"). It drives neither the muted rendering
                        // nor the TTS suppression — `type` does
  meta?: Record<string, unknown>  // opaque producer metadata, stored verbatim
}
```
`cmdStats` also reads `images?: unknown[]` and `type === "action_request"` off
raw messages beyond the typed `ChatMessage` shape (cast to `any[]`).

---

## Agent registry / presence

### `POST /api/chat/register-agent`
Upsert an agent's registry entry (identity, display metadata, nicknames,
capabilities). Idempotent — safe to call on every restart.

Callers: `packages/cli/src/listen.ts`, `packages/cli/src/commands-identity/lifecycle.ts` (`--rename`), `packages/cli/src/commands-nickname.ts`, `bin/parlay-spawn`.

Request body (all fields optional except `id`; each call site sends a subset):
```jsonc
{
  "id": "agent-id",
  "name": "Display Name",
  "color": "#rrggbb",
  "nicknames": ["nick1", "nick2"],   // commands-nickname.ts; [] clears
  "caps": { }                          // arbitrary JSON, forwarded from `parlay listen --caps`
}
```
Response:
```jsonc
{ "ok": true, "nicknames": ["nick1", "nick2"] }
// or
{ "error": "string" }
```

### `POST /api/chat/unregister`
Deregister an agent's channel from the registry.

Callers: `packages/cli/src/commands-agent-down.ts`, `packages/cli/src/commands-variant.ts`, `packages/cli/src/commands-teardown.ts`.

Request body:
```jsonc
{ "id": "agent-id" }
```
Response: not parsed by most callers (`commands-teardown.ts` ignores the body
entirely, `.catch(() => {})`s network errors). `commands-agent-down.ts` types
it as `{ ok?: boolean; id?: string }` but doesn't branch on it — failure is
detected purely via non-2xx status (`postJSON`'s `die()`).

### `GET /api/chat/agents`
List all registered agents.

Callers: `packages/cli/src/commands.ts` (`cmdAgents`, `cmdSend` listing, `cmdStats`, `cmdLaunch`), `packages/cli/src/commands-doctor.ts`.

Response: `AgentInfo[]`
```ts
interface AgentInfo {
  id: string
  name: string
  color: string
  nicknames?: string[]
  urls?: string[]
  path?: string[]
}
```

### `GET /api/chat/subscribers`
Connection/presence snapshot: panel clients, long-pollers, registered agents.

Callers: `packages/client/src/tab-online.ts`, `packages/cli/src/commands.ts` (`cmdStatus`, `cmdSubscribers`), `packages/cli/src/commands-crew-state.ts`, `packages/cli/src/commands-doctor.ts`.

Response: `SubscribersInfo`
```ts
interface SubscribersInfo {
  parlay?: { clients?: number }
  poll?: { count?: number; channels?: Array<{ channel: string | null; id?: string; name?: string }> }
  registered?: { count?: number; agents?: AgentInfo[] }
  presence_broadcasts?: number
  capability_suppressed?: Record<string, number>  // TS server only: gated event → deliveries suppressed by capability declarations; devices[] entries likewise carry surface/accepts when the connection declared ?caps=
  capability_declarations?: Array<{ surface: { kind: string; instance?: string }; accepts: string[]; content: string[]; interactions: string[]; connectedAt: string; device?: string }>  // TS server only: one entry per declared SSE connection (device-identified or not), all three declaration axes
  presence?: Array<{ channel: string; lastSeen: string | null }>   // read by tab-online.ts
  memory?: Record<string, number>     // read by commands-doctor.ts, optional
  history?: Record<string, number>    // read by commands-doctor.ts, optional
}
```

### `GET /api/chat/poll?after=<lastId>&channel=<agentId>`
Legacy long-poll loop (superseded by the relay-backed `parlay monitor`, but
still the fallback for `--legacy-poll` and used unconditionally by
`lavish-poll.ts`). Each call blocks server-side until a new message arrives
or times out.

Callers: `packages/cli/src/monitor.ts` (`runMonitor`, `--legacy-poll` path), `packages/cli/src/lavish-poll.ts`.

Response:
```jsonc
{ "timeout": true }
// or
{ "id": "msg-id", "role": "user"|"agent", "text": "string", "from": "string?",
  "cursorReset": "boolean?", "skipped": "number?" }
```
On timeout the caller loops immediately (no message emitted); on error the
caller sleeps and retries (2–3s backoff).

`cursorReset`/`skipped` are **Go-server only** (`packages/go-server`); the TS
server never emits them, so a caller must treat both as optional and absent.
They appear only when `after` names a message the server cannot resolve among
the channel's retained messages — a truncated or rotated store, a cursor from a
previous server run, or a cursor belonging to a different channel. Rather than
silently delivering nothing (the old behavior, which loses every message in the
gap) the server resumes from the newest `min(50, retained)` messages on that
channel and says so: `cursorReset: true`, with `skipped` counting the older
retained messages left outside that window. The bound mirrors the relay's
`PARLAY_REPLAY_MAX` default for the same reason — replaying a multi-thousand
backlog into an agent's context destroys the session it was restoring, but a
*silent* truncation is the original bug with better manners.

The reset frame carries the oldest message of that window, so the caller's next
`after` resolves normally and the walk forward continues one message per call.
A resolvable cursor never sets either field.

---

## Drafts

### `GET /api/chat/draft`
Read the currently-saved draft for this device/session.

Caller: `packages/client/src/input.ts` (`loadDraft`).

Response:
```jsonc
{ "text": "string" }
```

### `PUT /api/chat/draft`
Save (or clear, with `text: ""`) the current input draft. Debounced 600ms
client-side; also fired synchronously on clear.

Caller: `packages/client/src/input.ts` (`scheduleDraftSave`, `clearDraft`).

Request body:
```jsonc
{ "text": "string", "clientId": "uuid" }
```
`clientId` is a per-page-load id the client uses to ignore its own draft echo
over SSE (see `draft` event below). Response not consumed by the caller.

---

## Uploads

### `POST /api/chat/upload`
Upload an image attachment (drag/paste/file-picker). `multipart/form-data`,
not JSON.

Callers: `packages/client/src/annotation.ts`, `packages/client/src/attachments.ts`.

Request: `FormData` with a single `file` field.

Response:
```jsonc
{ "ok": true, "url": "string" }
```
Callers treat any other shape (`!res.ok || !res.url`) as failure and null out
the result — no error message is surfaced from the response body itself.
Client-side constraints referenced in UI copy: images only, 10MB max, up to 8
pending attachments per composer / 4 per paste into an annotation.

---

## Settings

### `GET /api/chat/parlay/settings`
Load persisted panel settings (voice phrases, layout, text scale, …).
3s client-side timeout; on failure the client silently falls back to
`DEFAULTS`.

Caller: `packages/client/src/settings-modal/io.ts` (`loadSettings`).

### `PUT /api/chat/parlay/settings`
Persist the full settings object (whole-document replace, not a patch).

Caller: `packages/client/src/settings-modal/io.ts` (`saveSettings`).

Body / response shape (`ParlaySettings`, `packages/client/src/settings-modal/types.ts`):
```ts
interface ParlaySettings {
  panelSide: 'left' | 'right'
  triggerSide: 'left' | 'right'
  enabledProjects: 'all' | string[]
  voiceEnabled: boolean
  voiceSubmitPhrases: string[]
  voiceClearPhrases: string[]
  voiceStopPhrase: string
  commandPhrases: Record<string, string[]>
  hybridVoice: boolean
  localOnlyVoice: boolean
  textScale: number
  voiceSettleMs: number
  noKeyboardMode: boolean
}
```
The client also migrates a legacy `voiceClearPhrase: string` field (singular)
into `voiceClearPhrases: string[]` on load if present — the server may still
be serving the old shape for some records.

---

## Voice / command eval (server-owned input evaluation)

### `POST /api/chat/eval`
The client sends NO local evaluation of typed/dictated text — every buffer
change (settle-debounced ~450ms, or immediate on resync) is POSTed here, and
the compiled Go voice/command engine computes actions, returned both
synchronously (timing only) and via the `input_action` SSE event (source of
truth for applying actions).

Callers: `packages/client/src/commands/dispatcher/up.ts` (`postEval`, main
composer), `packages/client/src/channel-picker.ts`, `packages/client/src/sender-picker.ts` (picker variants, distinct `streamId`s).

Request body:
```jsonc
{
  "streamId": "eval-<device>-<pageEpoch>",   // or "picker-…" / "sender-picker-…"
  "version": 42,                              // monotonic per-page input-version counter
  "text": "string",                            // the stabilized buffer
  "cursor": { "anchor": 0, "active": 0 },
  "reason": "input" | "resync" | "...",
  "voiceEnabled": true,
  "tabs": [{ "id": "string", "name": "string", "nicknames": ["string"] }],
  "device": "device-uuid",
  "paVersion": "string",
  "mode": "channel-select" | "sender-select"   // picker variants only; absent for main composer
}
```
Client timeout: `AbortSignal.timeout(3_000)`; failures are logged, not
surfaced to the user (best-effort telemetry channel).

Response (synchronous; the ACTUAL actions arrive over SSE, this is timing-only
per `up.ts`'s comment):
```jsonc
{ "timing": { "engineEvalNs": 12345, "relayMs": 3 } }
```
See [`input_action` SSE event](#input_action) for the actions themselves and
`docs/COMMAND_DESIGN_CONTRACT.md` / `docs/CHANNEL_PICKER_CONTRACT.md` for the
full action-protocol semantics (out of scope here — this doc covers wire
shape, not the engine's decision logic).

---

## Debug / diagnostics

### `POST /api/chat/debug-log`
Batched client error/warn/trace log shipped so a captain on mobile (no
devtools) can be diagnosed via a tailed log file.

Caller: `packages/client/src/debug-log.ts`.

**Server-side status: NOT WIRED.** The handler exists at
`packages/server/src/debug-log.ts` (`handleDebugLog`) and has never been
registered in `router.ts`: per its own header comment, that file was
unreachable through the broken symlink loop when the handler was written, and
nothing has wired it since. The client treats a 404
response as "permanent no-op for the session" and stops sending — this is
confirmed, working degradation, not a bug.

Request body:
```jsonc
{
  "device": "device-uuid",
  "ua": "navigator.userAgent",
  "url": "location.href",
  "entries": [
    { "ts": "iso", "level": "error"|"warn"|"trace", "source": "string", "message": "string", "detail": "any?" }
  ]
}
```
Sent with `keepalive: true` so a flush queued right before navigation still
lands. Batch max 50 entries, flushed every 2s or on batch-full.

Response (from the handler source, once wired):
- `204 No Content` — disabled (`PARLAY_DEBUG_LOG=0`), empty batch, or success
- `400` — invalid JSON body
- `500` — failed to persist to `$PARLAY_STATE_HOME/debug.log` (default `~/.parlay/debug.log`)

Fields are truncated server-side at 4000 chars each; entries capped at 50 per
batch even if the client sent more (defense in depth — client already caps at
`QUEUE_MAX = 50`).

### `POST /api/chat/tts-report`
Report a mispronunciation for the last-spoken TTS sentence (🚩 button or voice
command).

Caller: `packages/client/src/speech-highlight.ts` (`flagLastSpoken`).

Request body:
```jsonc
{
  "sentence": "string",
  "clipMeta": { "source": "panel", "msgId": "string|null" }
}
```
Response: only `res.ok` is checked; body shape unknown. Server is understood
to append to `tts-pronunciation-reports.jsonl` (per the client-side comment),
unverified from source.

---

## Plugins

### `GET /api/chat/plugins`
List installed plugin manifests. 3s client-side timeout; a fetch failure is
treated as "Pulse down — plugins just don't load this session" (non-fatal).

Caller: `packages/client/src/plugins.ts` (`initPlugins`).

Response:
```jsonc
[{ "id": "string", "version": "semver string" }, ...]
```
Each `id` matching `/^[a-z0-9-]+$/` gets a `<script>` tag injected pointing at
`/annotate/plugins/<id>.js?v=<version>` (a static asset path, not under
`/api/chat` — not otherwise documented here).

### `GET /api/chat/version`
Bundle version check, polled on every SSE `connected` event so a stale PWA
tab self-upgrades (reload once, guarded by a sessionStorage flag against
reload loops).

Caller: `packages/client/src/sse.ts` (inside the `connected` handler).

Response:
```jsonc
{ "version": "string" }   // "unknown" or equal to the client's compiled-in PA_VERSION means no-op
```

---

## Server → browser only (no known CLI/HTTP caller in this repo)

### `POST /api/chat/device-cmd`
Referenced only in a client-side comment (`packages/client/src/device-cmd.ts`
header: *"Agents POST /api/chat/device-cmd to drive the client"*) and in
`docs/CLI_VERBS_AND_EVENTS.md` (`router-device-cmd.ts`, "server → browser").
**No call site exists anywhere under `packages/cli/` or `bin/`** — agents are
presumably expected to `curl` this directly, or a wrapper exists only inside
the `packages/server`/Pulse tree, which could not be read when this was
written. Treat the request shape below as inferred from the consuming SSE
handler, not confirmed:
```jsonc
{
  "cmd": "reload" | "reset-tts" | "ping" | "switch-channel" | "list-channels" | "set-hands-free",
  "args": { "channel": "agent-id" }        // switch-channel only
  // or
  "args": { "enabled": "true" | "false" }  // set-hands-free only, omit to toggle
}
```
Delivered to the browser as the `device_cmd` SSE event — see below.

---

## SSE Events

### `GET /api/chat/events?device=<uuid>&after=<lastMsgId>&url=<currentPageUrl>&caps=<declaration>`
One persistent `EventSource` per tab. `after` (only sent once a message has
been received) asks the server for a delta instead of a full history replay;
`url` lets the server scope `history` more deeply for the page's owning
channel. On any error the client closes and reconnects with exponential
backoff (1s → doubling, capped 30s).

`caps` is **TS server only** (Go-server parity is a tracked follow-up): a
url-encoded JSON interface-capability declaration, contract owned by
[`docs/interface-capabilities.md`](./interface-capabilities.md) and the
normative engine `tools/cli/internal/capability`. A declared connection only
receives the presentation-command events (`navigate`, `reload`, `device_cmd`,
`input_action`, `draft`) it lists under `accepts`; all other events are
ungated. No `caps` at all = legacy client, byte-identical full delivery. An
*invalid* declaration is refused with `400 {"error"}` rather than falling back
to legacy — fail-open would widen delivery against declared intent. Unrelated
to `register-agent`'s free-form `caps` field, which is INPUT-direction agent
metadata.

Caller: `packages/client/src/sse.ts` (`connect`).

| Event | Payload | Notes |
|---|---|---|
| `connected` | `{ "clientId": "string", "capabilities"?: { "schema": "string", "recognized": ["string"], "unknown": ["string"] } }` | Resets reconnect backoff to 1s; triggers a `GET /version` self-upgrade check. The client reads no fields today. `capabilities` (TS server only) echoes the negotiation iff the connection declared `caps`: which accepts names this server gates on vs. has never heard of. |
| `history` | `ChatMessage[]` | Full or delta history depending on `after`. Persisted to IndexedDB. |
| `agents` | `AgentInfo[]` | Full agent-registry snapshot, upserted into local `agentInfo` map. |
| `agent_register` | `AgentInfo` | Single-agent upsert (incremental, vs. the bulk `agents` event). |
| `presence_map` | `Record<string, string>` (channel → status) | Consumed via `setChannelStatuses`. |
| `message` | `ChatMessage` (+ optional `images`, `type`, `received`) | The core new-message event; drives unread badges, TTS, compact-timer arm/clear. Deduped client-side by `data-pa-id`. |
| `message_received` | `{ "id": "string" }` | Delivery ack: a queued user message was polled by the agent → flips the ◌→✓ pip. |
| `presence` | `{ "status": "string" }` | Drives the thinking-dots indicator (`status === "thinking"`). |
| `draft` | `{ "text": "string", "clientId": "string" }` | Cross-device draft sync. Self-echoes (`clientId` match, or within 3s of local send) are ignored. |
| `agent_presence` | `{ "active": boolean }` | Toggles the "agent away" banner state on the drawer. |
| `tool_event` | *(opaque, passed to `appendToolEntry`)* | Tool-activity log line; also resets the compacting-banner timer. |
| `lavish_session` | `{ "key": "string", "file": "string", "proxyUrl": "string", "status": "string" }` | Inserts/updates a "lavish" (embedded workspace) card. |
| `reload` | *(none)* | `location.reload()` — full page reload. |
| `navigate` | `{ "url": "string", "openDrawer": boolean }` | Client-side workspace navigation (Parlay-as-shell). |
| `input_action` | `ActionEnvelope` (see below) | Voice/command engine's computed actions for the composer, channel picker, or sender picker (routed by `streamId`). |
| `device_cmd` | `{ "cmd": "string", "args"?: {...} }` | See [`POST /api/chat/device-cmd`](#post-apichatdevice-cmd) above. |
| `pages_patch` | `{ "added"?: {"tag":"string","title":"string"}[], "removed"?: "string"[] }` | Incremental update to the page-nav picker's page list. |

Plugins may subscribe to arbitrary additional event names via the client's
`onSse(event, handler)` shim (`packages/client/src/sse.ts`); the table above
covers every event name with a **first-party** subscriber in this repo, not
the full space of names the server could theoretically emit.

The live-command registry adds two further first-party event names
(`commands`, `command_update`) on this same stream, alongside its own
`/api/chat/commands` read route and three report routes. Both are additive —
an older client ignores unknown frames — and are documented in
[`docs/live-commands.md`](./live-commands.md), which owns that contract.

### `POST /api/chat/events` (Go server only)
The external-producer ingress into the Go SSE hub, for a producer that cannot
live inside that server. `Hub.broadcast` is in-process-only, so this is the one
seam by which an outside process puts a frame on the panel.

Callers: `packages/server/src/tool-tailer.ts`, via
`packages/server/src/hub-ingress.ts` (`pushHubEvent`). The TS server has no such
route — its `/api/chat/events` is `GET`-only.

Request body:
```jsonc
{
  "event": "tool_event",   // required; must be in the ingress allowlist
  "data":  { }             // optional; forwarded to the wire byte-identical
}
```
Response: `{"ok": true, "event": "<echoed name>"}`. An absent `data` broadcasts
`{}`.

**The allowlist is one name per real producer** — `tool_event` alone today.
Anything else is **400**, including every name the server produces from its own
persisted state (`message`, `history`, `agents`, `agent_register`,
`message_received`, `presence_map`, `commands`, `command_update`), every
panel-aiming name with no producer in the repo (`navigate`, `reload`,
`device_cmd`, `input_action`, `draft`), and any unknown name. `system_update` is
refused too: it is a `ChatMessage.type` carried on `message`, not an event name
— a producer wanting one posts to
[`POST /api/chat/message`](#post-apichatmessage) with `type: "system_update"`,
which persists first and broadcasts as a consequence. Rationale for each
refusal is in `packages/go-server/internal/handlers/events_ingress.go`'s doc
comment, which owns this contract.

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

---

## Endpoints referenced but out of scope for this doc

- **The relay control API** (`http://relay/register` etc. over a Unix socket
  at `$TMPDIR/parlay/relay.sock`, used by `tools/monitor/parlay-monitor.sh`)
  is a *local* control-plane protocol between the CLI and the
  `tools/relay/parlay-relay` daemon — not part of the network-facing
  `/api/chat/*` surface this doc covers.
- **`parlay status`** (the fold §3.6 keyed status verb used throughout this
  session, e.g. `parlay status working "..."`) is **pure local file I/O**
  (`commands-status.ts` → `$PARLAY_STATUS_FILE` or
  `~/.parlay/agents/<id>/status`) — it makes no HTTP call at all.
- **`POST /api/events/bead-status`** is a *proposed, not-yet-built* endpoint
  discussed in `docs/CLI_VERBS_AND_EVENTS.md` §2.6 — do not treat it as live.

---

## Open Gaps

1. **Response shapes were reconstructed, not read.** Every handler behind these
   routes lives under `packages/server/src/`, which was a broken symlink loop
   into `~/.claude/PAI/PULSE/modules/chat` when this doc was written on
   2026-08-01 (see project `CLAUDE.md`). That loop is fixed and the source is
   readable now, but nothing here has been re-verified against it: every
   response shape in this doc except `debug-log.ts`'s is still
   reverse-engineered from what the calling code reads off the response, not
   read from the handler. Fields a handler sets but no client happens to read
   are invisible to this document by construction. The one exception is
   § Origin guard, which is read from the handlers and the guard packages —
   treat every other section as client-derived unless it says otherwise.

2. **Inconsistent error convention across endpoints.** `register-agent`,
   `reply`, `send`, and `alert` are all consumed via an `{ error?: string }`
   field checked *after* a successful fetch, while `unregister` is documented
   in-repo as failing via non-2xx status, and the generic CLI transport
   (`http.ts`) treats *any* non-2xx as fatal. Whether the four `{error}`-style
   endpoints ever also return non-2xx (and for which failure classes) could
   not be determined without server source.

3. **`/api/chat/history`'s `type` field.** `packages/cli/src/types.ts`'s
   `ChatMessage.type` only types `"alert"`, but `packages/client/src/thread.ts`
   branches on `"system_update"` and `"action_request"` too (with an
   `action: {...}` payload for the latter). The CLI type is likely
   stale/incomplete rather than the client being wrong — worth reconciling in
   `types.ts` if the CLI ever needs to render these.

4. **`POST /api/chat/device-cmd` has no in-repo caller.** It's documented from
   the *consuming* SSE handler and a code comment only; the actual POST call
   site (an agent's `curl`, or code inside the server/Pulse tree, unreadable at
   the time) was not found. Request shape above is inferred, not confirmed.

5. **`POST /api/chat/message`'s response is unparsed** (`commands-supervise.ts`
   only checks `res.ok`) — its response shape is entirely unknown.

6. **`POST /api/chat/tts-report`'s persistence path** (a `.jsonl` file per a
   code comment) could not be verified against server source.

7. **`GET /api/chat/poll` timeout duration and `GET /api/chat/subscribers`'s
   exact `poll`/`registered`/`presence` field population rules** are
   long-poll/server-internal behavior not observable from any client call
   site.

8. **Auth/exposure.** No endpoint in this surface performs any
   authentication, on either server, and that is deliberate — `debug-log.ts`'s
   comment ("local/tailnet only — do not expose this port publicly") states
   the trust model the whole surface assumes. The one part of it that is now
   enforced rather than assumed is the cross-origin half: see § Origin guard
   above, and `packages/server/src/guard/` /
   `packages/go-server/internal/guard` for the policy and its tests. Nothing
   else about the trust model is verified — a caller that reaches the port at
   all, with no `Origin` header, is still trusted everywhere.
