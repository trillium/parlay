# @parlay/input

A thin, framework-agnostic DOM input wrapper for the parlay protocol. No
dependencies, no framework assumptions — it operates on a plain DOM
`Element`.

## Primary use case

The [herdr](https://github.com/trillium) Chrome web UI (or any other
DOM-based UI) needs a terminal-style input box wired up to a parlay server:
keystrokes/commits sent to the server, and server-pushed actions (clear the
box, set a value, focus it, ...) applied back to the element. Implementing
parlay's wire protocol by hand in every consumer is exactly the kind of
drift this package exists to avoid — `npm install @parlay/input` and call
`parlayInput(element, { server })` instead.

## Usage

```ts
import { parlayInput } from '@parlay/input'

const input = document.querySelector('#terminal-input')!

const unsubscribe = parlayInput(input, {
  server: 'http://localhost:4242',
  event: 'input', // DOM event that triggers a send; defaults to "input"
  action(action) {
    // handle actions pushed FROM the server, e.g. { type: 'clear' }
    if (action.type === 'clear') input.value = ''
  },
})

// later, e.g. on component teardown:
unsubscribe()
```

## Transports

- **`transport: 'websocket'` (default)** — opens a single duplex connection
  (derived from `server`, e.g. `http://localhost:4242` ->
  `ws://localhost:4242/api/input`) used for both outbound value updates and
  inbound `action` callbacks. This is the fully working path today.
- **`transport: 'post'`** — sends each value as a plain HTTP POST. This is a
  partial skeleton: outbound sends work, but there is no server -> client
  channel wired up yet, so `action` is never invoked in this mode. See the
  `TODO`s in `src/index.ts` for what's needed to close that gap (most likely
  a companion SSE or polling channel, matching the pattern already used by
  `packages/client`'s `sse.ts`).

Both transports talk to `path` (default `/api/input`) relative to `server`
— this endpoint does not exist on `packages/server` yet; it will need to be
implemented there before either transport works against a real deployment.

## Development

```sh
bun install
bun test    # run the test suite
bun run build   # emit dist/index.js (bun) + dist/index.d.ts (tsc)
```
