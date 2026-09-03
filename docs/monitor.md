# Monitor / listen

**Code:** `tools/cli/internal/monitor` (`monitor.go`, `listen.go`, `singleton.go`, `watchdog.go`).

`parlay monitor` and `parlay listen` are how an agent process actually
receives messages after it shows up in the [agent registry](agent-registry.md).
`listen` is the one-call self-enrollment path: register with the server,
announce (post a hello so the tab goes live), then start monitoring — all in
one idempotent call, which is why every launch brief in this fleet tells an
agent to run it as its first action.

Two delivery paths exist, selected by `--legacy-poll`:

- **Default (relay-backed)** — `monitor`/`listen` register with the standalone
  [relay](relay.md) daemon over its Unix socket and then tail their own spool
  file (`{runtime-dir}/<agent>.chan`). This is the cheap path: one relay
  process holds the upstream long-poll loop for every registered agent,
  instead of one ~40MB Bun poll process per agent.
- **`--legacy-poll`** — polls the server's `/api/chat/poll` directly in Go,
  natively, with no relay involved. This is what makes `listen --legacy-poll`
  usable on a fresh clone before anyone has built the relay binary
  (`tools/relay/build.sh` — gitignored, not built by `bun install` or
  `bin/parlay`).

**A real, documented gap** (verified against the root README's own text,
2026-09-03): bare `listen` *without* `--legacy-poll` registers and announces
with the server **before** it starts the relay connection. On a fresh clone
where the relay binary hasn't been built yet, this leaves an agent that is
enrolled (visible in the registry, looks live) but can never actually receive
anything — it posts a `monitor DOWN` notice on its way out, but the registry
entry survives, so the agent stays "enrolled and deaf." This is exactly the
class of thing issue #210 tracks for a related gap in `ensure-up`/exit-3
handling.

`singleton.go` enforces one live poll loop per agent channel (an "arming a
listener is a takeover" guard — a second `listen` for the same agent
supersedes rather than duplicates); `watchdog.go` is the post-spawn liveness
check the [launcher](launcher.md) uses to confirm a newly spawned agent's
first turn actually fired.

**`parlay shutdown <id>`** (task-35ww, landed after this doc's initial pass —
verified 2026-09-03 against `docs/agent-notes/graceful-agent-shutdown-task-35ww.md`)
is the counterpart to enrollment: explicit, on-demand teardown instead of
letting a retiring agent's listener/spool/registry row time out or get
pruned by the hourly sweep. One call does all three: kills any local
`listen`/`monitor` process for that id (same detect/SIGTERM/grace/SIGKILL
sequence the singleton guard above already uses), deregisters it server-side
(`POST /api/chat/unregister`, tombstoning the channel and reporting an
undelivered-message count rather than discarding it), and immediately
resolves any long-poll parked on that channel with `{gone: true}` instead of
waiting out its own timeout. It's idempotent — a 404 on the unregister step
means the agent was already retired, which counts as success, not error.
Scope note: this verb only covers `packages/server` (the live production
server) and `tools/relay`/`tools/cli` — `packages/go-server` was not given
parity here, matching the precedent of two earlier server-only PRs.
