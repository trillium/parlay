# Command / chat server

**Code:** [`packages/server`](../packages/server) (Bun/TypeScript, the server the root README's Quickstart runs) and [`packages/go-server`](../packages/go-server) (Go rewrite of the same surface). Both bind `:4242` by default and both exist in the tree today — this is a genuine two-implementation state, not a doc lag.

This is the process that owns `/api/chat/*`: chat history, the agent
registry, presence, the SSE event stream, the long-poll feed the [relay](relay.md)
consumes, uploads/drafts/settings, and the live-command registry
([`docs/live-commands.md`](live-commands.md)). The wire contract both
implementations target is [`docs/api-contract.md`](api-contract.md).

**The two implementations are not interchangeable substitutes for each other
right now.** Per the comment at the top of `packages/server/src/hub-ingress.ts`
(verified 2026-09-03): the Bun server's own two observability tailers
(`hook-tailer.ts`, `tool-tailer.ts`) no longer broadcast in-process — they POST
to `PARLAY_HUB_URL` (default `http://127.0.0.1:4242`), which is meant to be the
**Go** server's SSE hub and message-persist route
(`packages/go-server/internal/handlers/events.go`). If only the Bun server is
running on that port, those POSTs 404 against routes the Bun server doesn't
serve, and the failure is swallowed by design (rate-limited warning, tailing
never stops) rather than surfaced. So a from-source Quickstart that follows
the root README literally — start `packages/server` alone — gets a working
chat API but **not** hook/tool activity flowing into the panel; that needs
the Go server also running as the hub.

- `packages/server` — the original, currently more complete implementation.
  Owns chat history persistence to `chat-history.jsonl` (rotated at 5MB —
  see [`docs/events-history.md`](events-history.md)), the two JSONL tailers
  above, and every route in `docs/api-contract.md` as of this writing.
- `packages/go-server` — built up ticket by ticket (`main.go`'s own comment
  enumerates C0 storage, C1 messaging/registry/long-poll, C2 the SSE hub, C3
  drafts/uploads/settings) against the same contract, plus the newer
  live-command registry (`internal/store/commands.go`) and event-ingress
  allowlist (`internal/sourcecontracts`) that only exist on this side. It
  persists chat history to `messages.jsonl`, not `chat-history.jsonl` — a
  different file, not a synonym.

Both are fronted by a CORS/guard layer that enforces the security boundary
described in the root `CLAUDE.md`: the API is unauthenticated, so a route is
guarded by what it *does* (mutates, hands out identifiers), not by its HTTP
method. The Bun side is `packages/server/src/guard/`; the Go side is
`packages/go-server/internal/guard/`; both must be updated together for any
new mutating/identifier-aiming route.

**Env vars** (`PARLAY_DATA_DIR`, `PAI_DIR`, `PARLAY_HUB_URL`, `PARLAY_ALLOWED_ORIGINS`, `PARLAY_PUBLIC_HOST`, `PARLAY_SERVER_ADDR`, …) are documented canonically in [`examples/env.example`](../examples/env.example) — not repeated here.
