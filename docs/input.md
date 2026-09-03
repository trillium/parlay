# Input

**Code:** [`packages/input`](../packages/input) (published as the npm package `parlay-input`, the only package in this repo that ships to npm — see the root README's Layout table).

`parlay-input` is a small, dependency-free DOM wrapper: point it at a plain
`Element` (an `<input>`, `<textarea>`, or `contenteditable`) and a running
parlay server, and it wires that element into parlay's real input protocol.
It does **no client-side interpretation of what you type** — every edit is
POSTed to the server for evaluation by the compiled Go phrase engine
(`tools/cli/internal/evalengine`, served as `parlay eval serve`), which
decides the effect (submit, clear, open a picker, switch tabs, …) and pushes
the result back down a single shared Server-Sent Events connection. The
wrapper applies the core verbs (`setText`/`clear`/`submitNow`) to your element
itself; anything else (custom picker UI, tab switching) is handed to an
`onAction` callback you provide.

This is the piece that makes "dictate on a phone, an agent picks it up"
possible: it's the up-channel from whatever UI is showing the composer
(the not-open-sourced Pulse panel today) into the [command/chat server](command-server.md).

Verified against `packages/input/src/index.ts` and `packages/input/README.md`
(2026-09-03) — the README's own usage example matches the exported
`parlayInput(element, options)` signature.

**Env vars:** none of its own; the server URL is passed in as an option, not read from the environment. See [`examples/env.example`](../examples/env.example) for the variables the server side of this connection consumes.
