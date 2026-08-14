---
"parlay-input": minor
---

Rewrite `parlay-input` as a real client for parlay's actual protocol, and ship it as a single self-contained package

`parlay-input@0.1.0` was a non-functional stub: it spoke an invented WebSocket
transport that no parlay server implements, and it shipped as a two-package
split (`parlay-input` re-exporting `@parlay/input`) whose `@parlay/input`
dependency cannot be published, so `npm install parlay-input` produced an
unresolvable dependency.

- Replace the invented WebSocket transport with a real client for parlay's
  actual protocol: a REST up-channel (`POST /api/chat/eval`, `POST
  /api/chat/send`) plus a single shared Server-Sent Events down-channel (`GET
  /api/chat/events`, `input_action` envelopes). Framework-agnostic and
  dependency-free — it operates on a plain DOM `Element`. Actions are applied
  only from the SSE stream, with client-owned version/seq staleness handling
  and debounce/reconnect exposed as options.
- Collapse the two-package split into one published package, `parlay-input`,
  built directly from `packages/input/src/index.ts`. The `@parlay/input`
  package and the re-export shim are gone; the tarball declares no runtime
  dependencies.

This is a breaking rewrite of the public transport and behavior, so `0.2.0`.
