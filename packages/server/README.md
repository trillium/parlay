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

The canonical, annotated env-var reference for the whole repo (this server,
`packages/go-server`, and `tools/cli`) is
[`examples/env.example`](../../examples/env.example) — copy-pasteable,
commented, and includes the traps below in full. This table is a quick index
into that file, not a second source of truth; update `env.example` first and
let this table follow.

| Env var           | Default          | Purpose                                                        |
| ----------------- | ---------------- | -------------------------------------------------------------- |
| `PARLAY_PORT`     | `4242`           | TCP port the server listens on. **No bind-address setting and no authentication** — `serve({ port })` has no hostname, so Bun binds `0.0.0.0`. |
| `PARLAY_DATA_DIR` | *(unset)*        | Redirects every persisted file resolved through `paths.ts` into one directory — the **write** side. Does not change what the server reads from until you also move the existing files there (see Data files); does not cover `src/tts.ts`. |
| `PAI_DIR`         | `~/.claude/PAI`  | Root the hook/tool tailers watch for firing events, and the root `src/tts.ts` writes its pronunciation reports into and creates/evicts its clip cache under. Resolved with `??`, not `||` — `PAI_DIR=""` is a *value*, not "unset", and turns every `$PAI_DIR/...` path relative to the server's cwd. |
| `PARLAY_AGENT_ID` | *(unset)*        | Identifies the calling agent for per-agent context lookups.    |
| `PARLAY_EVAL_ENGINE_URL` | `http://127.0.0.1:4343` | External eval engine for `/api/chat/eval`; returns 502 until running (Go engine deliberately deferred). |
| `PARLAY_HUB_URL`   | `http://127.0.0.1:4242` | Go SSE hub the hook/tool tailers push into (`src/hub-ingress.ts`) — `POST /api/chat/events` for tool events, `POST /api/chat/message` for hook firings. **Not** derived from `PARLAY_PORT`, which is this server's own listen port; set it explicitly whenever the Go hub is not on the default port. Unreachable ⇒ posts are dropped with a rate-limited warn and tailing continues. |
| `PARLAY_ALLOWED_ORIGINS` | *(unset)*  | Extra browser origins the guard accepts on guarded routes (comma-separated exact origins; `*` disables the origin check). Same-origin, loopback, `.local` and private-LAN origins are already allowed, and a request with **no** `Origin` — every CLI/curl/hook caller — always is. See `src/guard/` (route set in `src/guard/paths.ts`). Also read by `packages/go-server` with identical semantics. |

## Data files

Every persisted path is resolved in `src/paths.ts` — with one exception,
`src/tts.ts`, which resolves `PAI_DIR` itself and writes outside that routing
(see below). With `PARLAY_DATA_DIR` unset the `paths.ts` files sit in their
production locations:

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
into it, flat — nothing under `~/exchange` or `$PAI_DIR` is read or written by
those paths. `src/tts.ts` is the exception and is not redirected by
`PARLAY_DATA_DIR` at all: it resolves its own `PAI_DIR`
(`process.env.PAI_DIR ?? ~/.claude/PAI`) and then **appends** to
`$PAI_DIR/MEMORY/OBSERVABILITY/tts-pronunciation-reports.jsonl`, **creates**
`$PAI_DIR/MEMORY/STATE/tts-cache/`, and **`unlinkSync`-deletes** clips out of
that cache whenever it exceeds `DISK_CACHE_MAX` (100). Set `PAI_DIR` to a
scratch directory as well, or the run writes into and deletes out of the real
one. Any test or local run that imports this module must do both. Importing the
module and calling `startChat()` runs a startup prune sweep against whatever
registry `paths.ts` resolves; against the live one it permanently removes real
agent channels (robots-jcjj). `src/paths.test.ts` pins the guarantee that no
persisted path escapes the override — add new persisted files to `paths.ts`,
never inline in the module that uses them.

## Tests

```sh
cd packages/server && bun test    # link-rewrite, prune, paths, router-poll, guard
```

Run from inside this package (there is no root `bunfig.toml`; see the repo
`AGENTS.md`).

`src/guard/*.test.ts` is the origin-guard suite: the `paths`/`origin`/`allow`/
`reject` files are pure, while each `integration-*.test.ts` file spawns a real
server (`src/guard/scratch-server.ts`) on a port reserved by binding `:0`,
with `HOME`/`PARLAY_DATA_DIR`/`PAI_DIR`/`PARLAY_STATE_HOME` redirected to a
temp dir — nothing under `~/exchange` or `~/.parlay` is touched.

## Relationship to Pulse

Pulse (the PAI-era assistant that served `http://localhost:31337`) historically
mounted this chat module **in-process** by importing it at startup. That
coupling is gone: this server is standalone, serves the panel bundle itself
(`src/static.ts`), and clients point at it directly on `:4242`. A legacy Pulse
may still sit in front as a reverse proxy of `/api/chat/*` while it is being
retired, but nothing here depends on it.

The in-process path had a fatal failure mode that took down chat entirely: the
files under `src/` were once committed symlinks pointing at
`~/.claude/PAI/PULSE/modules/chat`, which was itself a symlink back to this
directory — a self-referential loop. Every `import` resolved to itself and threw
`ELOOP`, so the Pulse chat module failed to load and `/api/chat/*` returned 404
to every caller (CLI, relay, panel). The real source was recovered from git
history and those loop symlinks were replaced with the real files you see now.
Removing the `~/.claude/PAI/PULSE/modules/chat` symlink and switching Pulse to a
reverse proxy are production changes made outside this repository.
