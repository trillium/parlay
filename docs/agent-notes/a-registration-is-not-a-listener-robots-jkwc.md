# A registration is not a listener, and a spool is not delivery (robots-jkwc)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


An agent's whole lifecycle runs over its parlay channel, and three independent
holes let that channel go silent with everything still *looking* healthy. All
three were on `tools/cli` — the path `bin/parlay` actually execs since B10.

- **Nothing retired a monitor.** robots-ycfa built a closed self-heal loop:
  prune → tombstone → the next poll gets 410 Gone → the relay drops that
  channel's loop → the monitor sees itself absent from the registry twice and
  exits. Its last two links existed only in the retired
  `packages/cli/src/monitor.ts`; the Go `runRelayMonitor` had no watchdog at
  all and `pollOnce` folded 410 into the generic 2s retry, so a tombstoned
  channel's poller resurrected itself forever.
  `tools/cli/internal/monitor/watchdog.go` is the missing link
  (`startRegistryWatchdog`, `terminateProcessTree`), and 410 is now the ONLY
  terminal poll status — a 500 still retries. Every ambiguity resolves toward
  staying alive (robots-dcag): a failed request, a non-2xx, an unparseable
  body, or an EMPTY registry all reset the strike count, and only two
  consecutive well-formed answers omitting this agent retire it.
  `PARLAY_NO_REGISTRY_WATCHDOG=1` opts out. Retirement kills the whole child
  process tree, not the child pid: `--notify-safe` runs `tail -F | awk` under
  bash, and SIGTERM to bash alone orphans the tail — the exact leaked-tail
  shape the loop exists to end.
- **`tail -n0` started at end-of-file.** Every line the relay spooled while no
  monitor was attached was skipped forever, and `parlay listen` has no replay
  — so a dropped listener silently ate every directive sent in that window
  while the lines sat unread in the spool. `parlay-monitor.sh` now resumes
  from `<agent>.chan.cursor`, a line count the awk stage advances AFTER each
  line is delivered rather than when the stream is armed (which is why the
  default path lost its `exec tail` and gained one awk, ~1MB: only a per-line
  stage can do that, and a monitor killed mid-backlog must re-deliver what it
  never emitted). Three bounds, all load-bearing: no cursor (a first arm)
  starts at EOF so a new listener is never handed the channel's history; a
  cursor past the end (truncated/recreated spool) replays from 0; and at most
  `$PARLAY_REPLAY_MAX` lines (default 50, newest kept) replay, with the skip
  announced on stderr — dumping 4,000 backlogged lines into an agent's context
  destroys the session it was restoring, but a *silent* truncation is just the
  original bug with better manners. `PARLAY_REPLAY_MAX=0` restores start-at-EOF.
  Pinned by section D of `tools/monitor/parlay-monitor.test.sh`, whose stub
  relay creates the spool if-missing rather than truncating it, matching the
  real relay's `O_CREATE|O_APPEND`.
- **`parlay launch` reported registry membership as liveness.** A registration
  is a row nothing removes when a listener dies, so `[live]` counted 148
  `mc-robots-*` agents against 11 real listener processes. Liveness is now the
  registry INTERSECTED with the process table (`monitor.LiveListenerAgents`,
  one `ps` read, classification in the pure `launchStatus`): `[live]`,
  `[ghost]` (registered but nothing is listening — messages to it are accepted
  and lost; clear the stale row with `parlay agent-down <id>`), `[offline]`.
  `ps` is local, so an unreadable process table leaves registered agents
  `[live]`: a failed probe is not evidence of a dead listener, and a wrong
  `[ghost]` sends the captain to `agent-down` on a working agent.

The usage line describing that new column diverges from `packages/cli`'s, so it
lands in `tools/cli/parity/run.sh`'s four `help` diffs — which were **already
failing before this change** on the Go-only `claim`/`merge-gate`/`branch-audit`/
`sweep` verbs (robots-xaxt). `pass=39 fail=4` is the harness's standing state,
not a regression from this work, and not green either.
