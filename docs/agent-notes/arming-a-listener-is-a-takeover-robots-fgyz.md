# Arming a listener is a TAKEOVER, not an addition (robots-fgyz)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`parlay listen` used to be purely additive: every restart, reconnect, or fresh
turn started another poll loop on the same channel and nothing ever ended the
previous one. The Mayor agent accumulated **12** live `parlay-cli listen
--agent mayor` processes (every other agent had exactly one), so every
captain→mayor message was delivered and processed up to 12 times, with 14
leaked long-poll shells and the Mayor session burning 20-27% CPU feeding them.

`tools/cli/internal/monitor/singleton.go` enforces one live loop per agent
channel: `CmdListen` reaps every other loop on that channel **before**
register/announce, so an HTTP failure can never leave a duplicate running.
Three decisions worth knowing before touching it:

- **Takeover, not "reuse and exit".** The process arming now is the one wired
  to the live harness `Monitor{}` task; exiting immediately would leave that
  task dead with the agent registered-but-deaf — the robots-dcag shape.
- **`ps` match, not a pidfile.** A pidfile only knows about listeners armed
  after this landed; the twelve that already existed had none, and a pidfile
  adds its own staleness failure mode. The process table cannot go stale.
- **Matching fails toward "not a duplicate", because the two error directions
  are not symmetric.** Killing a non-duplicate ends a live agent's session;
  missing one only leaves the pre-existing duplicate. So: exact token compare
  on the agent id (`--agent mayor` never matches `mayor-2`), the subcommand
  must be preceded by a parlay binary basename (a shell wrapper whose *command
  string* contains the invocation is not the listener), scanning stops at
  `--name`/`--caps` because `ps` flattens argv unquoted and a ticket title
  routinely contains `--agent`, and self plus every ancestor is protected —
  the harness arms through a shell whose command string is the whole
  invocation, so reaping an ancestor kills the reaper.

`PARLAY_LISTEN_NO_SINGLETON=1` opts out (announced on stderr). The singleton
enforcement is Go-only, no TS port — but `listen` itself exists in both CLIs,
so do **not** add it to `GO_ONLY_VERBS`. No `check` case in
`tools/cli/parity/run.sh` either: the singleton behavior causes a deliberate
divergence the harness can't reconcile.

(The `parity/run.sh` and `GO_ONLY_VERBS` paragraphs above are archaeology — both
were deleted with `packages/cli` in T-08. The singleton rule itself is live.)

## The match is host-wide, so two instances evict each other (verified 2026-10-05)

`selectDuplicateListeners` matches an agent **id**. It never learns which server,
state dir, or relay runtime dir a candidate listener belongs to — the `ps` line
carries no such thing, and the file's own "fail toward not-a-duplicate" doctrine
means guessing one would be the wrong direction. So the guard is
host-wide-per-id, and the README explicitly blesses running a second instance
(`parlay-dev`, a `-state-dir` server, `parlay remote set`). When their agent ids
collide, the second `listen` **SIGTERMs the first's listener across servers** and
the loser is left *registered but deaf* — its row still in its own server's
registry, nothing reading the channel.

Reproduced end to end (two isolated servers, different ports, different state
dirs, different `HOME`s, same `--agent`): instance B printed

```
parlay listen: 1 existing listener(s) for '<id>' (pid NNNN) — ending them so this channel keeps exactly one
```

instance A's process died, and `GET /api/chat/agents` on server A still listed
`<id>`. Three consequences to carry:

- **A test instance is a FIFTH surface, not four redirects.** `HOME`,
  `PARLAY_STATE_HOME`, `PARLAY_AGENT_HOME` and `PAI_DIR` isolate everything
  *this* box writes except this: the guard reads the process table, and relay
  spools live in `$TMPDIR/parlay`. An isolated `listen --agent X` can kill a
  production listener on an id you picked without thinking.
- **`KillLocalListeners` (i.e. `parlay shutdown <id>`) has the same host-wide
  reach** — same `selectDuplicateListeners`, no server discrimination.
- **The binary-basename allowlist is a silent coverage hole.** `ps` must show
  `parlay` or `parlay-cli` for a process to be classified as a listener, so a
  renamed copy of the binary is never reaped and duplicate delivery returns with
  no signal. Observed while reproducing this: a scratch build at
  `/tmp/parlay-iter21-cli` armed a listener and the next `listen` for the same
  id printed nothing and killed nothing; renamed to `parlay-cli` the same pair of
  commands reaped as designed.

The honest mitigations, all now user-facing in `parlay listen --help`,
`parlay monitor --help`, `docs/monitor.md` and the root README: distinct agent
ids per instance (`--name`/`--color` are registration cosmetics and do not scope
the match), or `PARLAY_LISTEN_NO_SINGLETON=1` in the instance that must not
evict. What is deliberately NOT claimed: the match cannot be made instance-aware
without a way to read another process's server, which this repo has no portable
way to do.
