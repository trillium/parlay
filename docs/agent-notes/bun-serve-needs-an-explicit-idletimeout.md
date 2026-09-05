# `Bun.serve()` needs an explicit `idleTimeout`, or it destroys your long-lived routes

Found 2026-09-05 diagnosing the captain's production chat-server wedge (task-2nwlw, P0).

## The symptom

`com.parlay.chat-server` (Bun, `packages/server/src/index.ts`, port 4242) would
serve correctly for a short window, then wedge: **TCP still accepted on :4242,
but every long-lived route timed out returning 0 bytes, while the process sat at
0% CPU.** 54 Bun crash reports had accumulated. `launchctl kickstart -k`
recovered it for about two minutes.

0% CPU with accepted-but-unanswered connections reads like a blocked event loop.
It was not. Nothing in parlay was blocking.

## The mechanism

`Bun.serve()` applies a **default `idleTimeout` of 10 seconds**: any connection
that transfers no bytes for that long is destroyed by the runtime. The client
already holds the `200` response line, so what it observes is an HTTP 200 with
an empty body — indistinguishable from a hung server.

This server deliberately holds two kinds of connection open for longer:

| route | intentional hold | fate under the 10s default |
|---|---|---|
| `GET /api/chat/poll` | parks a waiter **30s** before answering `{"timeout":true}` | destroyed at ~12s, **0 bytes** |
| `GET /api/chat/events` | SSE; first keepalive **25s** away | destroyed ~10s after the history flush — **the keepalive could never fire once** |

`idleTimeout` was set nowhere in the repo, so both were being killed on every
single connection. The panel reconnected in a loop, agents re-polled in a loop,
and the runtime kept tearing down `ReadableStream` controllers that JS timers
still referenced — the path each `panic(main thread): Segmentation fault at
address 0x0` in `~/Library/Logs/parlay/chat-server.err.log` is logged
immediately after, always preceded by Bun's own
`[Bun.serve]: request timed out after 10 seconds. Pass 'idleTimeout' to configure.`

The Go hub on :4243 carried the same workload and stayed healthy throughout,
because Go's `http.Server` defaults to **no** timeouts. That contrast is the
tell: the wedge tracked the runtime, not the traffic.

## Measured numbers (Bun 1.3.11, macOS)

`idleTimeout` is in **seconds**, max **255** (256 throws `ERR_INVALID_ARG_TYPE`),
`0` disables it. The timer is coarse — the effective kill lands a few seconds
past nominal:

| `idleTimeout` | held response destroyed at |
|---|---|
| unset (default 10) | ~12s |
| 1 | ~4s |
| 5 | ~8s |

A test that wants to straddle the boundary must therefore clear it by several
seconds, not by one — 12s is a coin flip against the default, 15s is not.

## The rule

**Any `Bun.serve()` that holds a connection open longer than 10 seconds must
pass an explicit `idleTimeout` that exceeds its longest hold**, and any SSE
keepalive must be *shorter* than that timeout or it is dead code.

`packages/server/src/timeouts.ts` owns both the holds and the derived timeout so
the margin cannot silently drift; `timeouts.test.ts` fails if it ever does, and
asserts on `index.ts`'s source that `serve()` is actually handed the value —
a correct constant nothing passes to the runtime is worth nothing.
