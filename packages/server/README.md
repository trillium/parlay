# @parlay/server

A standalone [Bun](https://bun.sh) HTTP server that owns the Parlay chat API
(`/api/chat/*`): message history, agent presence/registry, SSE streams, polling,
TTS, uploads, hook/tool tailers, and the server-side-eval relay. It runs as its
own process and depends on nothing outside this package at import time (only Bun
and Node built-ins).

## Run it

```sh
bun run start        # bun src/index.ts
bun run dev          # bun --watch src/index.ts
```

`src/index.ts` binds the port, loads history + drafts from disk, starts the
pages watcher, and serves every request through `handleChatRequest`
(`src/router.ts`) with a debug pre-router (`src/router-debug.ts`).

## Configuration

| Env var           | Default          | Purpose                                                        |
| ----------------- | ---------------- | -------------------------------------------------------------- |
| `PARLAY_PORT`     | `4242`           | TCP port the server listens on.                                |
| `PARLAY_DATA_DIR` | `~/exchange`     | Directory for chat history + draft (`storage.ts`).             |
| `PAI_DIR`         | `~/.claude/PAI`  | Root the hook/tool tailers watch for firing events.            |
| `PARLAY_AGENT_ID` | *(unset)*        | Identifies the calling agent for per-agent context lookups.    |

## Data files

History and draft honor `PARLAY_DATA_DIR`; the rest are anchored under the
user's home directory:

- `$PARLAY_DATA_DIR/chat-history.jsonl` — message log (rotates at 5 MB).
  **Live data — do not move or clobber `~/exchange/chat-history.jsonl`.**
- `$PARLAY_DATA_DIR/chat-draft.txt` — persisted composer draft.
- `~/exchange/parlay-agent-channels.json` — agent registry / channel declarations.
- `~/exchange/parlay-settings.json` — server-persisted settings.
- `~/exchange/parlay-uploads/` — uploaded image attachments.
- `~/pulse-pages/` — watched page directory served to clients.

When testing locally, set `PARLAY_DATA_DIR` to a scratch directory so you never
touch the live `~/exchange` history.

## Tests

```sh
cd packages/server && bun test    # 37 tests across link-rewrite + prune
```

Run from inside this package (there is no root `bunfig.toml`; see the repo
`AGENTS.md`).

## Relationship to Pulse

Pulse (the personal assistant that serves `http://localhost:31337`) historically
mounted this chat module **in-process** by importing it at startup. That coupling
is being removed: Pulse should instead **reverse-proxy `/api/chat/*` to this
standalone server**, so the chat API's availability no longer depends on Pulse's
import succeeding.

The in-process path had a fatal failure mode that took down chat entirely: the
files under `src/` were once committed symlinks pointing at
`~/.claude/PAI/PULSE/modules/chat`, which was itself a symlink back to this
directory — a self-referential loop. Every `import` resolved to itself and threw
`ELOOP`, so the Pulse chat module failed to load and `/api/chat/*` returned 404
to every caller (CLI, relay, panel). The real source was recovered from git
history and those loop symlinks were replaced with the real files you see now.
Removing the `~/.claude/PAI/PULSE/modules/chat` symlink and switching Pulse to a
reverse proxy are production changes made outside this repository.
