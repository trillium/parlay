# Go rewrite of `packages/server`: use `docs/api-contract.md`, not `docs/scope-go-server.md`

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`packages/go-server` (module `parlay/go-server`) is the Go rewrite of Pulse's
HTTP/SSE chat server, built ticket-by-ticket (C0: HTTP skeleton + storage
layer in `internal/store`; C1: messaging/registry/legacy-poll handlers in
`internal/handlers`; C2: the SSE hub behind `GET /api/chat/events`, also in
`internal/handlers` — see `events.go`'s package-level doc comment for exactly
which of the 17 event names documented in `docs/api-contract.md` have a live
producer today (message, message_received, agent_register, plus the
connect-time burst of connected/history/agents/presence_map) versus which are
wire-ready but unproduced pending a future ticket (drafts, device-cmd,
tool/session events, etc.); C3: drafts/uploads/settings, also in
`internal/handlers` — see the dedicated section below; C4+ still open —
eval-relay/debug-log, parity harness, deploy tooling). Ticket briefs for this
workstream point at `docs/scope-go-server.md` as the authoritative spec —
**that file has never existed anywhere in this repo's git history** (checked
with `git log --all --diff-filter=A -- '*scope-go-server*'`, no hits, as of
this note). Use `docs/api-contract.md` instead: a ~600-line HTTP contract for
every `/api/chat/*` route, reconstructed from `packages/client`/
`packages/cli` call sites (since the real handler source is the broken
symlink farm described above), already referenced by name in C0's own store
doc comments. It landed on `origin/main` for real via PR #27 — if a future
worktree is missing it, that's a stale/pre-#27 base, not a sign the doc
doesn't exist; rebase onto current `origin/main` rather than re-deriving or
re-cherry-picking it. (History note: before #27 merged, C1 found it early by
checking `git log --all` for every local ref, not just `origin/main` — it was
sitting on a diverged, not-yet-pushed local `main` commit. That workaround is
now obsolete but is the reason to always check `git log --all` before
concluding a referenced doc "doesn't exist" in future tickets.)
