---
"@parlay/cli": patch
---

Supervise the agent monitor's stream so a dead `tail` can no longer take an agent's only reply channel with it, silently (robots-gv6t).

`parlay listen` registers the agent and posts "listening — monitor armed" *before* shelling out to `tools/monitor/parlay-monitor.sh`, and the stream was terminal on both sides — `exec tail -F` in the script, a single `cmd.Run()` + `os.Exit(child's code)` in `tools/cli/internal/monitor`. Whatever ended that one process ended the channel, and the failure was reported only on stderr, which a harness Monitor tool never reads. The panel kept showing a ready agent while nothing reached it: registered-but-deaf, the same outcome as robots-dcag by a different route. The reported symptom was an exit 144 (128+SIGURG) moments after arming; the exact killer is not identified, and deliberately does not need to be — the fix is robust to any of them. (`exec.ExitError.ExitCode()` also answers -1 for a signalled child, so the notification could not even name what happened.)

Both layers now supervise:

- **`parlay-monitor.sh`** respawns `tail` in a loop instead of `exec`ing it, resuming at the byte offset delivery actually reached — a counting stage after `tail` reports the bytes it forwarded, so messages the relay spools during the gap are delivered rather than skipped, and already-emitted lines are not replayed. Repeated sub-`$PARLAY_MONITOR_MIN_UPTIME` deaths (default 2s) are bounded by `$PARLAY_MONITOR_MAX_RESTARTS` (default 5) and then give up.
- **`internal/monitor`** respawns the script on any unexplained exit, names the signal that killed it, and treats only the script's two deliberate refusals (`EXIT_USAGE`, `EXIT_RUNTIME`) as terminal.

Every stream transition is announced **on stdout** as `MONITOR|<kind>|<text>` — distinct from the relay's `CHAT_MSG|` lines, and the only stream a harness Monitor turns into an agent-visible event. Giving up also posts a "monitor DOWN" reply to the agent's own channel, retracting `listen`'s announce where the captain can see it (the registry has no listening flag to clear). Regression coverage: section D of `tools/monitor/parlay-monitor.test.sh` (kills `tail` mid-stream, asserts recovery, gap delivery, no duplicates, and a bounded loud give-up) and `tools/cli/internal/monitor/supervise_test.go`.
