# The listen stream is supervised — never make an agent's reply channel one process (robots-gv6t)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Same registered-but-deaf outcome as robots-dcag, reached from the other end.
`parlay listen` registers and posts its "listening — monitor armed" announce
*before* it shells out, and the stream after that used to be terminal on both
sides: `exec tail -F` in `parlay-monitor.sh`, one `cmd.Run()` + `os.Exit(child's
code)` in `tools/cli/internal/monitor`. Whatever ended that single process ended
the channel, and it said so only on stderr — which a harness Monitor tool never
reads. The panel kept showing a ready agent receiving nothing. (The reported
symptom was exit 144 = 128+SIGURG; the killer was never identified and does not
need to be. Note `exec.ExitError.ExitCode()` answers **-1**, not 144, for a
signalled child — read `WaitStatus.Signal()` if you want to name it.)

Three rules this leaves behind for anything supervising a stream here:

- **Respawn, don't propagate.** The script restarts `tail`; the Go side restarts
  the script on any unexplained exit and treats only the script's own deliberate
  refusals (`EXIT_USAGE`/`EXIT_RUNTIME`) as terminal. Both bound the retries
  (`$PARLAY_MONITOR_MIN_UPTIME`, `$PARLAY_MONITOR_MAX_RESTARTS`) and then give up
  loudly rather than spinning.
- **Resume where delivery stopped, not where the file is now.** A counting stage
  after `tail` reports the bytes it actually forwarded, and the next `tail -c +N`
  starts there. Re-reading the spool's *current size* at restart would silently
  swallow everything spooled during the gap; `-n0` would swallow more.
- **Report on stdout.** stderr is invisible to the agent whose channel just
  dropped. Stream events are `MONITOR|<kind>|<text>` lines, deliberately distinct
  from the relay's `CHAT_MSG|`, and a give-up additionally posts "monitor DOWN"
  to the agent's own channel — the registry has no listening flag to clear, so
  the channel message is the only way to retract the announce.

Regression coverage: section D of `tools/monitor/parlay-monitor.test.sh` (kills
`tail` mid-stream, then asserts recovery, gap delivery, no duplicates, and a
bounded loud give-up) and `tools/cli/internal/monitor/supervise_test.go`.
