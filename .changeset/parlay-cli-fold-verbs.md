---
"@parlay/cli": minor
---

Fold in the firstmate-parity CLI surfaces. Four user-facing changes:

- **`parlay status`** repurposed from a redundant alias of bare `parlay` into the keyed agent→supervisor status verb (fold §3.6): `parlay status <verb> [--key <slug>] <note...>` appends a keyed status line, bare `parlay status` reads THIS agent's status file. The panel/fleet snapshot now lives ONLY on bare `parlay`. BREAKING for external callers that relied on `parlay status` printing the snapshot — acceptable at alpha (0.x).
- **`parlay guard`** — new runtime worktree-tangle + watcher-liveness alarm (`--repo`, `--grace`, `--read-only`, `--json`, `--beat`). Advisory: warns, never blocks.
- **`parlay robots-watch`** — new event poll-daemon that diffs store status cursors and routes robots-created→mechanic-dispatch and request-closed→notify (`--interval`, `--once`, `--verbose`).
- **`parlay robots-tail`** — new push fast-path that tails the robots `events.jsonl` byte offset → mechanic-dispatch in ~1s (`--once`, `--verbose`); the poll daemon stays the reconciler fallback.
