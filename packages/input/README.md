# parlay-input

A thin, framework-agnostic DOM input wrapper for wiring a webpage's primary
input box into the parlay command server. No dependencies, no framework
assumptions — it operates on a plain DOM `Element`.

It implements parlay's real input protocol: a REST up-channel plus a single
shared Server-Sent Events down-channel, with client-owned version/seq staleness
handling. There is no client-side evaluation — every edit is relayed to the
compiled Go engine (`tools/cli/internal/evalengine`, run as `parlay eval serve`), which decides what happens (clear
the box, submit, show a picker, …) and pushes the result back down the SSE
stream.

## Install / quick start

```sh
npm install parlay-input
```

You need a running parlay server (see the parlay repo — `packages/server`, or
the Go rewrite in `packages/go-server`; default port `4242`). Point the wrapper
at it and hand it your input element:

```ts
import { parlayInput } from "parlay-input"

const el = document.querySelector("#composer")! // <input>, <textarea>, or contenteditable

const unsubscribe = parlayInput(el, {
  server: "http://localhost:4242",
})

// later: tear down the DOM listener and the SSE connection
unsubscribe()
```

That's the whole happy path. On every edit the wrapper POSTs the buffer to the
server for evaluation, and applies whatever the engine pushes back over SSE —
clearing the box, submitting, etc. The core verbs (`setText`/`clear`/
`submitNow`) are applied to `el` for you.

If your app already runs its own page-wide SSE connection, or renders custom UI
for verbs like pickers and tab switches, wire those in via options:

```ts
const unsubscribe = parlayInput(el, {
  server: "http://localhost:4242",
  // Reuse an existing shared EventSource instead of opening a second one:
  subscribe: (event, handler) => myEventBus.on(event, handler), // returns an unsubscribe
  // Handle UI verbs the wrapper doesn't apply itself (pickers, tab switches, …):
  onAction: (action, ctx) => { /* render your UI */ },
})
```

## API

`parlayInput(element, options)` attaches `element` to a parlay server and
returns an idempotent `Unsubscribe`. Key options (see `src/index.ts` for the
full, typed surface):

- `server` — base URL of the parlay server (required).
- `event` — DOM event to listen for. Default `"input"`.
- `settleMs` — voice-settle debounce; rapid edits collapse into one eval of the
  stabilized text. Default `450`.
- `onAction(action, ctx)` — handle verbs the wrapper does not apply itself. The
  core input verbs (`noop`/`setText`/`clear`/`submitNow`) are always applied to
  the element directly; everything else (pickers, tab switches, navigation) is
  delegated here so the wrapper stays UI-agnostic.
- `onSubmit(text)` — how `submitNow` actually submits. Default `POST
  /api/chat/send`. Evaluation never submits on its own.
- `subscribe(event, handler)` — plug into an existing shared SSE connection
  instead of opening one; return an unsubscribe. When omitted, the wrapper opens
  its own `EventSource` to `/api/chat/events` with exponential-backoff reconnect.
- `device` / `streamId` / `tabs` / `voiceEnabled` — protocol context; `device`
  is auto-generated and persisted to localStorage when omitted.
- `fetch` / `EventSource` — injectable implementations (for a non-DOM host or
  tests).

The **staleness/version/seq machinery is configurable, not hard-coded** to the
reference UI's choices — `settleMs`, the protocol version, and the SSE transport
are all options.

## The protocol: REST + a single shared SSE stream

Parlay's input pipeline is **not** per-input-box request/response — it's one
shared, page-wide SSE connection that multiplexes many event types, plus
separate REST endpoints for each concern.

Reference implementation (the source of truth this wrapper follows):

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

### Which origins the server accepts

The parlay server has **no authentication** and is not meant to be reachable
from the public internet. What stands in for auth on the writing half of the
API is an **origin guard**, and `POST /api/chat/eval` — this package's entire
up-channel — is one of the routes behind it. So the page you mount
`parlayInput` on has to be served from an origin the server allows.

**Works:**

- **Same origin** as the server (the normal case — the panel itself).
- **Loopback**: `http://localhost:<port>`, `http://127.0.0.1:<port>`.
- **`.local` hostnames** (mDNS/Bonjour), e.g. `http://mac-studio.local:4242`.
- **Private-LAN literals**: `10.x`, `192.168.x`, `172.16–31.x`, `169.254.x` —
  this is how the phone reaches the panel over the LAN.
- **Anything listed in `PARLAY_ALLOWED_ORIGINS`** on the server (see below).
- **No `Origin` header at all** — every non-browser caller (curl, the CLI,
  server-to-server). Not something a browser page can opt into.

**Does not work:** any public origin — `https://your-app.example.com`,
a Vercel/Netlify preview URL, a CodePen/JSFiddle frame, a `file://` page or a
sandboxed iframe (both send `Origin: null`).

**The failure is a deliberate refusal, not a network problem.** A disallowed
origin gets **`403`** with a JSON body of `{"error": "cross-origin request
rejected"}` and **no CORS headers at all**, so the browser also blocks your
code from reading that response — in the console it surfaces as a CORS error
and in your code as a rejected `fetch`, which looks exactly like the server
being down. It is not: the server answered, and refused. A preflight from such
an origin is refused the same way, and so is a `POST` that tries to skip the
preflight by using a simple content type: **the origin check runs first**, so
that request is a `403` too, not a `415`. Nothing here is retryable and
nothing reaches a handler.

**`415` is a different failure, and it means your origin was accepted.**
`{"error": "Content-Type: application/json required"}` on a guarded `POST`/
`PUT` comes from the content-type gate, which is only reached once the origin
passed — so if you are getting `415`, fix the request's `Content-Type` (this
package already sends `application/json`); if you are getting `403`, no header
you can set will help, and the origin has to be allowed server-side.

**The escape hatch is explicit opt-in, server-side.** Set
`PARLAY_ALLOWED_ORIGINS` on the parlay server to a comma-separated list of
exact origins (the single value `*` disables the origin check entirely):

```sh
PARLAY_ALLOWED_ORIGINS="https://your-app.example.com" bun run start
```

There is no client-side equivalent, by design — the whole point is that the
server decides. Full policy: `docs/api-contract.md` § Origin guard.

### `POST /api/chat/eval` request body

```ts
{
  streamId: string        // per-page-load epoch: `eval-${device}-${epoch}` — avoids
                           // colliding with the engine's version counter across reloads
  version: number          // monotonic, bumped on EVERY local buffer mutation
  text: string              // current buffer contents
  cursor: { anchor: number, active: number }
  reason: string             // e.g. 'input', 'resync'
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
synchronous response is used only for round-trip timing.

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

Every inbound envelope is validated before any action touches the element:

- `baseVersion < currentVersion` **and** the envelope mutates the buffer — the
  user has typed something newer since this action was computed → **stale**,
  dropped, triggers a resync. A stale *non-mutating* action (a hint, a picker)
  is harmless and still applied.
- `seq` skips ahead of the expected next value — an SSE event was dropped in
  transit → triggers a resync.

A "resync" re-POSTs the current buffer to re-anchor the server's view of state.
This staleness handling is load-bearing: without it, a slow network round-trip
can silently apply an action computed against text the user has since edited
away. `submitNow` additionally re-verifies its `requireTail` against the truly
current buffer at apply time before firing — the server's decision is ~1
round-trip stale, and on a slow link the tail has often already moved.

Note: `ACTION_TTL_MS` (1500ms) and a `rejected-expired` `ApplyResult` variant
are declared for wire parity, but — matching the reference dispatcher — action
age is **not** enforced. The constant is exported so the contract is visible,
not because a TTL check runs.

## Development

```sh
bun install
bun test        # run the test suite (fakes the REAL REST + SSE endpoints)
bun run build   # emit dist/index.js (bun) + dist/index.d.ts (tsc)
```
