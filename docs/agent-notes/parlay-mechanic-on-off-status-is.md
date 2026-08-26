# `parlay mechanic on|off|status` is the kill switch for the robots→mechanic auto-spawner

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Filing a `robots` bead auto-dispatches a mechanic agent; `parlay mechanic off`
pauses that without touching launchd or killing the tailer/watcher.
Authoritative code: the gate is `mechanicDispatchOff()` in
`tools/cli/internal/robotswatch/handlers.go`, checked at the top of
`dispatchMechanic` — the single choke point both the PUSH (`robots-tail`) and
POLL (`robots-watch`) paths converge on. The verb itself is
`tools/cli/internal/commands/mechanic.go`. Disabled state = the sentinel file
`$PARLAY_STATE_HOME/mechanic-dispatch.off` (default `~/.parlay/`) OR env
`PARLAY_MECHANIC_DISPATCH=off`; the sentinel is durable operator intent and
wins over `PARLAY_MECHANIC_DISPATCH=on`. When OFF the tailer/poller keep
running and advancing their offsets, so re-enabling does NOT replay the
backlog. **Go-only, no TS port** — same reasoning as `merge-gate`: no `check`
case in `tools/cli/parity/run.sh`, but it must be in that script's
`GO_ONLY_VERBS` or its usage line reddens every help diff (robots-xaxt);
it was missing there until the live-commands branch added it. Complementary to the durable-mechanic-lifecycle
work (robots-jkwc) — that gates nothing, this dispatches nothing when off.
