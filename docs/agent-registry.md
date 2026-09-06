# Agent registry & presence

**Code:** `packages/go-server/internal/store/registry.go` + `presence.go` (Go); the Bun equivalent lives across `packages/server/src/router-messages.ts`, `router-poll.ts`, and `paths.ts`.

Not to be confused with the **live-command registry**
([`docs/live-commands.md`](live-commands.md)), which tracks running `parlay`
CLI invocations. This is the **agent registry**: who is enrolled as a chat
tab at all.

- **`RegistryStore`** (`registry.go`, verified 2026-09-03) is a full-snapshot
  JSON file (`agents.json`), atomically rewritten on every change — small
  enough (a handful of agents) that, unlike message history, there's no
  append-log/ring-buffer split. `Upsert` is documented as idempotent and
  per-call-site partial: a caller that only sends `id` and `color` does not
  blank an existing `name` or `nicknames`. `nicknames` is the one field an
  explicit empty (non-nil) slice is allowed to clear.
- **`PresenceTracker`** (`presence.go`) is the transient half — connected
  panel (SSE) clients, active long-poll counts per channel, last-seen
  timestamps — held **only in memory**, on purpose: a connection count that
  survived a restart would be lying, since every live connection is gone the
  moment the process exits.

The root `CLAUDE.md`'s warning about `~/.claude/PAI/MEMORY/STATE` and
`parlay-agents.json` refers to the Bun server's older on-disk registry file
at that path, which is why `PARLAY_DATA_DIR` and `PAI_DIR` must both be set
before running a test instance — see the root README's Quickstart. The Go
`RegistryStore` above is the newer, server-owned equivalent; both represent
the same concept (which agents are enrolled) but are not the same file.

Agents populate this registry via `parlay listen`/`monitor` (see
[`docs/monitor.md`](monitor.md)) calling `register-agent`, and the [launcher](launcher.md)
(`parlay spawn`) posts a hello on launch so a freshly spawned agent's tab
goes live immediately rather than waiting for its own first enrollment call.
