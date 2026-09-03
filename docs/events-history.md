# Events / chat history (JSONL)

**Code:** `packages/server/src/storage.ts` (Bun) and `packages/go-server/internal/store/store.go` (Go).

Chat history is an append-only JSON-Lines file, one `ChatMessage` per line —
**not the same file on the two server implementations**, verified
2026-09-03:

| Implementation | File | Rotation |
|---|---|---|
| `packages/server` (Bun) | `chat-history.jsonl`, under `$PARLAY_DATA_DIR` (or `~/exchange` if unset — see the root README's `PARLAY_DATA_DIR` warning) | at 5MB, renamed to `chat-history.<date>.jsonl` (`storage.ts`) |
| `packages/go-server` | `messages.jsonl`, under `--dir`'s configured data directory | at `MaxHistoryBytes` (`store.go`'s `DefaultMaxHistoryBytes`), compacted rather than simply renamed |

Two other JSONL streams feed into chat history rather than being it:

- **Hook firings** — `$PAI_DIR/MEMORY/OBSERVABILITY/hook-firings.jsonl`,
  written synchronously by Claude Code hooks
  (`hooks/lib/parlay-announce.ts`). `packages/server/src/hook-tailer.ts`
  tails it (1s poll, byte-offset tracked, restarts on truncation/rotation)
  and turns each line into a `system_update` chat message, routed to the
  channel resolved from the firing's `session_id` or the shared `system`
  pseudo-tab if none is known.
- **Tool events** — a parallel tailer (`tool-tailer.ts`) for tool-call
  activity, same mechanism.

Both tailers now push over HTTP to `PARLAY_HUB_URL`
(`packages/server/src/hub-ingress.ts`) rather than broadcasting in-process —
see [`command-server.md`](command-server.md) for why that currently means
tailed events only reach the panel if the Go server is also running as the
hub. The ingress side of that HTTP call is a **allowlisted** event-name gate
on the Go server (`packages/go-server/internal/sourcecontracts`, derived from
`contracts/sources/*.json` — see [`docs/source-contracts.md`](source-contracts.md));
`tool_event` is the one name enrolled today, per the root `CLAUDE.md`'s
warning not to widen that allowlist ad hoc.

This is genuinely load-bearing: it's how the panel/CLI can show `history`,
and how hook/tool activity becomes visible without hooks paying network
latency (the write is local and synchronous; the tail is the only thing
touching the network).
