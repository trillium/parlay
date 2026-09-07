# Command / chat server

**Code:** [`packages/go-server`](../packages/go-server) (Go), entrypoint
`packages/go-server/cmd/parlay-server`. It is the only server implementation in
the tree since the Bun `packages/server` was deleted in the Bun→Go cutover —
the root README's Quickstart runs `go run ./packages/go-server/cmd/parlay-server`
against `127.0.0.1:4242`. Run it with `-state-dir` / `-addr` flags or the
`PARLAY_STATE_HOME` / `PARLAY_SERVER_ADDR` env vars.

This is the process that owns `/api/chat/*`: chat history, the agent
registry, presence, the SSE event stream, the long-poll feed the [relay](relay.md)
consumes, uploads/drafts/settings, and the live-command registry
([`docs/live-commands.md`](live-commands.md)). The wire contract it
implements is [`docs/api-contract.md`](api-contract.md).

The server's storage lives under its state dir (`$PARLAY_STATE_HOME`, default
`~/.parlay`): messages/agents/drafts/settings/uploads. Chat history is
`messages.jsonl`, not the TS server's `chat-history.jsonl` (a different file,
not a synonym — see [`docs/events-history.md`](events-history.md)). The two
observability tailers (`hook-tailer.ts`, `tool-tailer.ts`) that once lived
in the TS server no longer broadcast in-process; they POST to
`PARLAY_HUB_URL` (default `http://127.0.0.1:4242`), which is this server's SSE
hub and message-persist route (`packages/go-server/internal/handlers/events.go`).

The server owns these areas, built up ticket by ticket (`main.go`'s own comment
enumerates C0 storage, C1 messaging/registry/long-poll, C2 the SSE hub, C3
drafts/uploads/settings, plus the later live-command registry in
`internal/store/commands.go`, event-ingress allowlist in
`internal/sourcecontracts`, TTS in `internal/handlers` `RegisterTTS`, pages
`RegisterPages`, and plugins `RegisterPlugins`).

The whole mux is fronted by a CORS/guard layer that enforces the security
boundary described in the root `CLAUDE.md`: the API is unauthenticated, so a
route is guarded by what it *does* (mutates, hands out identifiers), not by its
HTTP method. The guard is `packages/go-server/internal/guard/`; a new
mutating/identifier-aiming route must be added to `guard.GuardedPaths` and
nothing is guarded until you do.

**Env vars** (`PARLAY_STATE_HOME`, `PAI_DIR`, `PARLAY_HUB_URL`, `PARLAY_ALLOWED_ORIGINS`, `PARLAY_PUBLIC_HOST`, `PARLAY_SERVER_ADDR`, …) are documented canonically in [`examples/env.example`](../examples/env.example) — not repeated here.