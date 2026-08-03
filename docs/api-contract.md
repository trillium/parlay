# Parlay HTTP API contract

> Ground truth for this doc is the **client** (`packages/client/src/*`) and
> **CLI** (`packages/cli/src/*`) call sites, plus the one standalone server
> file that exists outside the broken symlink farm
> (`packages/server/src/debug-log.ts`). Every other server-side handler
> (`router.ts`, `router-poll.ts`, `router-device-cmd.ts`, `sse.ts`,
> `eval-relay.ts`, …) lives under `packages/server/src/`, which — as of
> 2026-08-01 — is a broken self-referential symlink loop into
> `~/.claude/PAI/PULSE/modules/chat` and cannot be read from this checkout
> (see the project `CLAUDE.md`). **Response shapes below are reconstructed
> from how callers consume them, not read from the handler source** — see
> [Open Gaps](#open-gaps) for exactly what that means per endpoint.
>
> Base path for all REST/SSE routes: `/api/chat`. `CHAT_BASE` in
> `packages/client/src/config.ts` is the single client-side constant; the CLI
> resolves the server origin via `serverUrl()` in `packages/cli/src/config.ts`
> (`PARLAY_SERVER` env → persisted `~/.parlay/config.json` `"server"` key →
> `http://localhost:4242`). All request/response bodies are JSON unless noted.

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
Lower-level message post used by `parlay supervise` to relay a daemon-authored
digest onto an agent's channel on the agent's behalf (not a captain/user
message).

Caller: `packages/cli/src/commands-supervise.ts` (`postToRelay`).

Request body:
```jsonc
{ "channel": "agent-id", "role": "agent", "text": "string" }
```
Response: not parsed by the caller — only `res.ok` is checked. Shape unknown.

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
{ "id": "msg-id", "role": "user"|"agent", "text": "string", "from": "string?" }
```
On timeout the caller loops immediately (no message emitted); on error the
caller sleeps and retries (2–3s backoff).

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
`packages/server/src/debug-log.ts` (`handleDebugLog`) but per its own header
comment, `router.ts` (where it would be registered) is unreachable through the
broken symlink loop, so it has never been connected. The client treats a 404
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
the still-unreadable `packages/server`/Pulse tree. Treat the request shape
below as inferred from the consuming SSE handler, not confirmed:
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

### `GET /api/chat/events?device=<uuid>&after=<lastMsgId>&url=<currentPageUrl>`
One persistent `EventSource` per tab. `after` (only sent once a message has
been received) asks the server for a delta instead of a full history replay;
`url` lets the server scope `history` more deeply for the page's owning
channel. On any error the client closes and reconnects with exponential
backoff (1s → doubling, capped 30s).

Caller: `packages/client/src/sse.ts` (`connect`).

| Event | Payload | Notes |
|---|---|---|
| `connected` | *(none)* | Resets reconnect backoff to 1s; triggers a `GET /version` self-upgrade check. |
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

1. **Server source is unreadable.** Every handler behind these routes lives
   under `packages/server/src/`, a broken symlink loop into
   `~/.claude/PAI/PULSE/modules/chat` as of 2026-08-01 (see project
   `CLAUDE.md`). Every response shape in this doc except `debug-log.ts`'s is
   reverse-engineered from what the calling code reads off the response, not
   read from the handler. Fields a handler sets but no client happens to read
   are invisible to this document by construction.

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
   site (an agent's `curl`, or code inside the still-unreadable server/Pulse
   tree) was not found. Request shape above is inferred, not confirmed.

5. **`POST /api/chat/message`'s response is unparsed** (`commands-supervise.ts`
   only checks `res.ok`) — its response shape is entirely unknown.

6. **`POST /api/chat/tts-report`'s persistence path** (a `.jsonl` file per a
   code comment) could not be verified against server source.

7. **`GET /api/chat/poll` timeout duration and `GET /api/chat/subscribers`'s
   exact `poll`/`registered`/`presence` field population rules** are
   long-poll/server-internal behavior not observable from any client call
   site.

8. **Auth/exposure.** No endpoint in this surface performs any
   authentication. `debug-log.ts`'s comment ("local/tailnet only — do not
   expose this port publicly") is the only explicit statement of the trust
   model found anywhere in the codebase; whether it holds for the *whole*
   `/api/chat` surface (not just debug-log) is an assumption, not a verified
   invariant.
