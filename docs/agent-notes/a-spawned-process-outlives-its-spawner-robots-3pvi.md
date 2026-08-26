# A spawned process outlives its spawner unless something ENDS it (robots-3pvi)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Killing a shell does not kill what it started. The harness kills the shell it
spawned; every descendant reparents to init and keeps running. `parlay listen`
therefore leaked one `tail -F` per death — and a reader on a *quiet* channel
never writes, so it never even earns a `SIGPIPE` to notice nobody is listening.
Measured on the captain's box: **168 live readers, 142 orphaned**, oldest 3 days,
one channel with 20 of them. Because the spool is append-only and never
truncated, each extra reader re-delivers every directive to a dead session.

Rules this leaves behind, for anything here that spawns a long-lived child:

- **Own the child's death, not just its birth.** `exec`ing into a follower is
  the leak: nothing is left to notice the launcher is gone. Keep a supervisor,
  and end the child when your own `PPID` changes (reparenting = launcher gone).
  Both layers do this — `parlay-monitor.sh` watches its launcher, and
  `internal/monitor` watches *its* own, because 73 of the 168 stranded chains
  were rooted at an orphaned `parlay-cli` that the script below it could not see.
- **A trap can't fire during a foreground command.** Bash defers signal handlers
  until the running foreground command returns, and `tail -F` never returns — so
  `trap … TERM` above a foreground pipeline is dead code. Background the pipeline
  and `wait` on it; that is the construct bash interrupts.
- **`$!` after a pipeline is the LAST process, not the first.** Killing the `awk`
  in `tail | awk` leaves `tail` alive until its next write. Find the real child
  by exact command match scoped to your own children (`ppid == $$`).
- **Match processes as whole command lines, never a `pgrep -f` regex.** A
  metacharacter in a spool path must not be able to widen a kill.
- **A destructive sweep is scoped or it is a weapon.** `parlay monitor --reap`
  only considers readers under its own runtime dir, so a test (or a second
  server's relay) can never reach the captain's live readers. It is a dry run
  until `--apply`.

Cleanup for what already leaked: `parlay monitor --reap [--apply]`. Details in
`tools/monitor/NOTES.md` § Reader lifetime; regression coverage in section D of
`tools/monitor/parlay-monitor.test.sh` and `internal/monitor/monitor_test.go`.
