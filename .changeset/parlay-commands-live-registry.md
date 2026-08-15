---
"parlay-cli": minor
---

Add **`parlay commands`** — what parlay is running right now, read from the server's live-command registry. One record per invocation that reported itself, carrying its verb, agent, pid, age, and how it ended.

- `parlay commands` lists the running set; `--all` also shows recently finished/failed/expired records.
- `--agent <id>` and `--verb <verb>` filter; `--json` prints the same field names the panel sees, for scripting.
- `--watch` follows changes over the existing SSE stream at `/api/chat/events` — one connection, no polling loop.

**One registry, two renderers.** The chat panel gained a live-commands view that renders the same server state, so the CLI and the panel cannot disagree about what is running. That sameness is pinned by a golden fixture (`packages/go-server/testdata/live-commands.golden.json`) read by three separate suites — the Go server handler, the Go CLI, and the client's Bun tests — so changing the wire shape fails all three in one commit.

**What it cannot see, stated in its own output.** Only the Go CLI reports itself, so shell entry points, the retired TypeScript CLI, and work the server originates on its own are not tracked. `parlay commands` also excludes itself, so the observer never appears in its own output, and `PARLAY_COMMAND_REPORT=0` opts an invocation out entirely. The gap is deliberate: a view that silently omitted half the running commands would be worse than one that says what it can and cannot see, and both renderers print the limit in their empty state.

**What is never recorded.** Flag values, positional arguments, message bodies, paths, and raw argv never leave the CLI — only the verb, the agent id, the pid, and flag *names* travel, because command lines routinely carry secrets. The CLI strips values before sending and the server sanitizes again on arrival; both apply the identical strict flag-name shape, and a token that fails it is dropped whole rather than trimmed into something that would arrive looking like a legitimate flag.

**Stale entries are reaped, not left running.** A command killed without reporting its end is marked `expired` (outcome `no-heartbeat`) after ~90s rather than sitting permanently in the running set.

**Watch stream end.** When `--watch`'s event stream ends, the verb says so and exits non-zero rather than returning quietly as if nothing were left to see. Under `--json` that notice is `{"ok":false,"error":"stream-ended"}` on stdout, keeping the promise that every stdout line parses; the reason the stream ended goes to stderr in both modes.

See `docs/live-commands.md` for the full design, including why registration is CLI self-reporting rather than a wrapper or server-side inference.
