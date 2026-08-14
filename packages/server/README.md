# parlay-server

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
| `PARLAY_DATA_DIR` | *(unset)*        | Redirects **every** persisted file into one directory (`paths.ts`). Unset ⇒ the production locations below. |
| `PAI_DIR`         | `~/.claude/PAI`  | Root the hook/tool tailers watch for firing events.            |
| `PARLAY_AGENT_ID` | *(unset)*        | Identifies the calling agent for per-agent context lookups.    |
| `PARLAY_EVAL_ENGINE_URL` | `http://127.0.0.1:4343` | External eval engine for `/api/chat/eval`; returns 502 until running (Go engine deliberately deferred). |
| `PARLAY_ALLOWED_ORIGINS` | *(unset)*  | Extra browser origins the guard accepts on guarded routes (comma-separated exact origins; `*` disables the origin check). Same-origin, loopback, `.local` and private-LAN origins are already allowed, and a request with **no** `Origin` — every CLI/curl/hook caller — always is. See `src/guard/` (route set in `src/guard/paths.ts`). |

## Data files

Every path the server writes is resolved in `src/paths.ts`. With
`PARLAY_DATA_DIR` unset they sit in their production locations:

- `~/exchange/chat-history.jsonl` — message log (rotates at 5 MB).
  **Live data — do not move or clobber it.**
- `~/exchange/chat-draft.txt` — persisted composer draft.
- `~/exchange/parlay-agent-channels.json` — session → channel declarations.
- `~/exchange/parlay-settings.json` — server-persisted settings.
- `~/exchange/parlay-uploads/` — uploaded image attachments.
- `$PAI_DIR/MEMORY/STATE/parlay-agents.json` — the agent/channel registry.
  **The prune sweep deletes from this file** (`prune.ts`).
- `$PAI_DIR/MEMORY/STATE/parlay-session-channels.json` — session → channel map
  learned from tool activity.

`~/pulse-pages/` (watched page directory) is served, never written, and is not
affected by `PARLAY_DATA_DIR`.

Set `PARLAY_DATA_DIR` to a scratch directory and **all** of the above relocate
into it, flat — nothing under `~/exchange` or `$PAI_DIR` is read or written.
Any test or local run that imports this module must do this. Importing the
module and calling `startChat()` runs a startup prune sweep against whatever
registry `paths.ts` resolves; against the live one it permanently removes real
agent channels (robots-jcjj). `src/paths.test.ts` pins the guarantee that no
persisted path escapes the override — add new persisted files to `paths.ts`,
never inline in the module that uses them.

## Tests

```sh
cd packages/server && bun test    # 44 tests across link-rewrite + prune + paths
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
