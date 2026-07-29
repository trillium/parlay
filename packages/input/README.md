# @parlay/input

A thin, framework-agnostic DOM input wrapper intended for wiring a webpage's
primary input box into the parlay command server. No dependencies, no
framework assumptions — it operates on a plain DOM `Element`.

## ⚠️ Current implementation does not match the real server protocol

This package was originally scaffolded against an invented protocol — a
single duplex WebSocket to `/api/input` sending `{type:'input', event,
value}` frames. **That endpoint does not exist anywhere in `packages/server`**,
and the real system does not use WebSockets at all. The section below
documents the actual protocol, discovered by reading the live reference
implementation (`packages/client` + `packages/server`). Anyone picking this
package back up should treat `src/index.ts`'s current transport as a stub to
be replaced, not a working client — its tests pass only because they exercise
a mock WebSocket server that also doesn't correspond to anything real.

## The real protocol: REST + a single shared SSE stream

Parlay's actual input pipeline is **not per-input-box request/response** — it's
one shared, page-wide Server-Sent Events connection that multiplexes many
event types, plus separate REST endpoints for each concern. There is no local
evaluation on the client: every keystroke is relayed to a compiled Go engine
(`packages/eval-engine`) which decides what should happen (clear the box,
submit, show a picker, ...) and pushes the result back down the SSE stream.

Reference implementation (read these before writing new client code):

- `packages/client/src/input.ts` — wires the DOM input element up end to end
- `packages/client/src/sse.ts` — the shared `EventSource` connection + plugin registry
- `packages/client/src/commands/dispatcher/` — the `ActionEnvelope` staleness/seq/resync logic (`up.ts`, `apply.ts`, `types.ts`)
- `packages/server/src/eval-relay.ts` — the server-side relay to the Go eval engine
- `packages/server/src/router.ts` — top-level `/api/chat/*` route dispatch

### Endpoints

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/chat/events?device=...&after=...&url=...` | GET (SSE) | The one shared connection per page. Carries `input_action` (server-pushed actions) plus many other event types (`message`, `draft`, `presence`, `agents`, `reload`, `navigate`, ...). |
| `/api/chat/eval` | POST | Up-channel. Sends the current buffer for server-side evaluation after every edit. |
| `/api/chat/eval-push` | POST (server-internal) | The Go engine's own per-stream timer can independently fire a submit; relayed to the owning device the same way as an `/eval` response. |
| `/api/chat/draft` | PUT / GET | Cross-device draft persistence, decoupled from evaluation. |
| `/api/chat/send` | POST | Actual message submission (distinct from evaluation — evaluation never sends a message itself). |

### `POST /api/chat/eval` request body

```ts
{
  streamId: string        // per-page-load epoch: `eval-${device}-${epoch}` — avoids
                           // colliding with the engine's version counter across reloads
  version: number          // monotonic, bumped on EVERY local buffer mutation (bumpInputVersion())
  text: string              // current buffer contents
  cursor: { anchor: number, active: number }
  reason: string             // e.g. 'input', 'voice-settle'
  voiceEnabled: boolean
  tabs: { id: string, name: string, nicknames: string[] }[]
  device: string
  paVersion: string
}
```

The client does **not** apply the synchronous HTTP response directly — actions
are only ever applied via the SSE `input_action` event, so there's a single
source of truth for ordering/staleness regardless of whether an action
originated from a live POST or the engine's own server-owned timer fire. The
synchronous response is used only for round-trip timing (latency overlay).

### `input_action` SSE event (`ActionEnvelope`)

```ts
{
  v: number                // protocol major version
  streamId: string
  seq: number               // strictly increasing per stream; a gap ⇒ dropped SSE event ⇒ resync
  baseVersion: number       // echoes the `version` this action was computed against
  actions: { verb: string, args?: {...} }[]
  timing?: { engineEvalNs?: number, relayMs?: number, serverOwnedFire?: boolean }
}
```

Every inbound envelope goes through `applyEnvelope()`
(`packages/client/src/commands/dispatcher/apply.ts`), which rejects it rather
than applying it when:

- `baseVersion < currentInputVersion()` — the user has typed something newer
  since this action was computed → **stale**, dropped, triggers a resync.
- `seq` skips ahead of the expected next value — an SSE event was dropped in
  transit → triggers a resync.

`types.ts` also declares an `ACTION_TTL_MS` (1500ms) constant and a
`rejected-expired` `ApplyResult` variant for action-age expiry, but
`applyEnvelope()` does not currently check action age anywhere — TTL
rejection is declared in the type, not enforced by the dispatcher.

A "resync" re-POSTs the current buffer to re-anchor the server's view of state.
This staleness handling is the part most likely to be skipped by a naive
reimplementation — without it, a slow network round-trip can silently apply
an action computed against text the user has since edited away.

### Debounce / send cadence

Sends are **not** fired on every keystroke. `scheduleEval()`
(`packages/client/src/commands/dispatcher/up.ts`) debounces on a
"voice-settle" timer (`ctx.settleMs`) so a rapid dictation burst collapses
into one evaluation of the stabilized text — the server never evaluates
mid-correction text.

## What "enrolling a webpage's primary input" requires

To wire a new input element into the real system, an implementation needs to:

1. **Establish a device identity** — a stable per-browser id (see
   `getDeviceId()` in `packages/client/src/sse.ts`), used to scope both the
   SSE subscription and every eval POST.
2. **Open the single shared SSE connection** — `GET /api/chat/events?device=...`
   via `EventSource`, with exponential-backoff reconnect (`packages/client/src/sse.ts`'s
   `connect()`). Register an `input_action` listener through the `onSse()`
   plugin registry rather than opening a second connection.
3. **On every input event**: call `bumpInputVersion()`, then
   `scheduleEval(getText, getCtx, immediate, reason)` to debounce-POST
   `/api/chat/eval` with the current buffer + cursor + device/stream context.
4. **On every inbound `input_action` SSE event**: call `applyEnvelope(env, resync)`
   and apply whichever verbs it returns to the DOM element — do not skip the
   staleness/seq checks described above.
5. **Wire draft sync** (optional but expected by the reference UI) — `PUT
   /api/chat/draft` debounced on edit, `GET /api/chat/draft` on load, and
   handle the `draft` SSE event for cross-device sync.
6. **Wire message submission separately from evaluation** — `POST
   /api/chat/send` on explicit submit; evaluation (`/api/chat/eval`) never
   sends a message on its own.

This is meaningfully richer than a generic "send value, get action back"
wrapper: the version/seq staleness protocol and the single-shared-stream
multiplexing are load-bearing, not optional polish. A future
`@parlay/input` v2 needs to implement a real subset of this contract — likely
exposing the debounce/version/resync machinery as configurable hooks — rather
than assuming a bespoke duplex channel per input box.

## Development

```sh
bun install
bun test        # run the test suite (currently exercises the stale WebSocket transport)
bun run build   # emit dist/index.js (bun) + dist/index.d.ts (tsc)
```
