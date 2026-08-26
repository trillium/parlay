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
