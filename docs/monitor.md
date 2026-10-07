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
  `bin/parlay`). Nothing records what a direct poll consumed: no spool line, no
  ledger hand-over, no relay claim. That is why `parlay timeline` requires the
  relay's own claim trail to show the relay was polling a channel before it will
  report a recorded message as `unhanded` ([`timeline.md`](timeline.md) — a
  channel the relay never claimed is never called lost).

**The relay is preflighted before enrollment, so a missing relay is a clean
refusal, not a deaf tab** (verified 2026-09-03 against
`internal/monitor/listen.go`, `monitor.go` and `internal/commands/claim.go`).
Every enrolling entry point — `listen`, `monitor`, `claim` — runs
`parlay-monitor.sh --preflight` *before* it POSTs `register-agent`. On a fresh
clone with no relay binary the preflight fails, the verb exits `ExitRuntime`
saying `NOT registered, so nothing is deaf`, and the agent never appears in
`GET /api/chat/agents`. `--legacy-poll` deliberately skips the preflight: it is
the no-relay escape hatch, so requiring a relay there would defeat its purpose.

That preflight is what the "registered-but-deaf" trap was about — an agent
enrolled in the panel whose event stream does not exist takes no directives for
the rest of the session and nothing says so. `parlay monitor` was the one entry
point still missing it (it registered, *then* discovered the dead relay — fixed
2026-09-03), so this section and the root README both blamed bare `listen`,
which had been closed earlier and was pointing readers at a hole that was no
longer there.

**The preflight also checks that the relay is polling *your* server**, not just
that it is up (added 2026-10-05). The relay is a per-user singleton that binds
one `-server` for its whole life, so two parlay instances on one host share it;
without this check a second instance's `listen` enrolled into a relay polling
the *first* instance's chat server and came up live-looking and permanently
deaf — the identical outcome the preflight exists to prevent, reached by a
different road. `parlay-monitor.sh` now reads the `server` field off the relay's
`/health` and refuses with both URLs named before anything is registered.
`localhost`≡`127.0.0.1` and the scheme's default port are normalized first, and
a relay that reports no `server` at all (a build predating the field) is not
treated as a mismatch — see [`relay.md`](relay.md). The green line changed
shape for the same reason: it now reads `preflight OK — canonical relay is up
for 'X' and polling <url>`, because "the relay is up" was never the whole
precondition.

`singleton.go` enforces one live poll loop per agent id (an "arming a
listener is a takeover" guard — a second `listen` for the same agent
supersedes rather than duplicates); `watchdog.go` is the post-spawn liveness
check the [launcher](launcher.md) uses to confirm a newly spawned agent's
first turn actually fired.

> **The takeover is host-wide, not per instance — and it fires across
> servers.** The guard matches a `ps` line for a `parlay`/`parlay-cli` binary
> running `listen`/`monitor`/`agent-up` with the same `--agent <id>`; it has no
> notion of *which* server, state dir or relay runtime dir that listener belongs
> to. So two parlay instances on one host — `parlay-dev` beside production, a
> `-state-dir` server beside the default one, two `parlay remote set` targets —
> evict **each other**'s listener the moment their agent ids collide, and the
> loser is left *registered but deaf*: its row is still in its own server's
> registry while nothing reads the channel. Verified 2026-10-05 end to end: two
> isolated servers on different ports, different state dirs, different `HOME`s,
> same agent id — the second `listen --legacy-poll` printed
> `1 existing listener(s) ... ending them` and the first process died, while
> server A kept the stale registry row. Two further consequences worth knowing:
> `parlay shutdown` kills by the same id-based match, so it has the same
> host-wide reach; and the match requires the process to be named `parlay` or
> `parlay-cli`, so a renamed copy of the binary (or a `go run` build) is never
> detected and duplicate delivery returns silently.
>
> The same argv[0] requirement is what keeps a *shell* out of the victim set
> (fixed 2026-10-05). The match used to accept any `ps` line whose subcommand
> token was merely *preceded by* a parlay binary basename, which a wrapper
> satisfies too: `bash -c 'cd /x && ( /path/to/parlay-cli listen --agent demo
> … )'` was classified as a duplicate, and `listen` SIGTERMed **its own
> caller**. Requiring the candidate's own argv[0] to be the parlay binary
> excludes every wrapper, script, `tmux send-keys` and agent harness by
> construction — and needs no ancestry walk, which could not have saved it
> anyway, because the `( … & )` subshell exits and reparents the listener out
> of reach of any `ps` ppid map. Reproduced and fixed: arming from a wrapper
> now leaves the wrapper alive and prints no bogus `existing listener(s)`
> line, while a genuine second `listen` for the same id still reaps the first.
>
> The opt-out is `PARLAY_LISTEN_NO_SINGLETON=1`, which skips the reap and says so
> on stderr (duplicate delivery becomes possible). The reliable answer for a
> second instance is a distinct agent id per instance — `--name` and `--color`
> are per registration and do not scope the match.

**`parlay shutdown <id>`** (task-35ww, landed after this doc's initial pass —
verified 2026-09-03 against `docs/agent-notes/graceful-agent-shutdown-task-35ww.md`)
is the counterpart to enrollment: explicit, on-demand retirement instead of
leaving a departing agent's listener, spool, and registry row behind. It kills
any local `listen`/`monitor` process for that id (same
detect/SIGTERM/grace/SIGKILL sequence the singleton guard above already uses),
then deregisters it server-side via `POST /api/chat/unregister`. It's idempotent
— a 404 on the unregister step means the agent was already retired, which
counts as success, not error. Scope note: every step is an HTTP call to the
chat server, so this verb covers `packages/go-server` and `tools/relay` /
`tools/cli` equally; the go-server *is* the server, so there is no second
implementation sitting outside the change.

> **What the server actually does today (verified 2026-09-03 against
> `packages/go-server`).** The verb was written against a server that, on
> unregister, tombstoned the channel, immediately resolved parked long-polls
> with `{"gone": true}`, reported an `undelivered` count, and answered **410
> Gone** to any later poll. The Go server does none of those: `unregister`
> removes the registry row, broadcasts `agent_unregister`, and returns
> `{ok, id}` — `undelivered` is always absent, a parked poll resolves only on a
> message, its 25s timeout, or a client disconnect, and no request anywhere in
> the server answers 410. Consequence for a reader: the relay's 410-tombstone
> path (`relay_poll.go`) and `parlay monitor`'s 410 exit never fire against
> this server, so after `shutdown` a remote agent's local spool survives and
> its poll loop simply keeps polling a channel that is no longer enrolled (and
> will still receive anything sent to it). The verb is still correct for a
> *local* agent, where step 1 kills the listener; treat the server-side
> cascade as unimplemented rather than relying on it.
