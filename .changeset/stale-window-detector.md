---
"@parlay/cli": minor
---

Add `parlay stale <agent-id>`, and refuse `parlay send` to a pane that already finished its work (robots-9d2w).

**The defect.** A pane that FINISHED its task is indistinguishable, to a sender, from one that is still working. Both are registered, both are enrolled with the relay, both accept a message. So a re-task lands in the finished pane, and the new work is done on top of the old session's entire transcript. The captain caught one at 70% context — its own harness offering "new task? /clear to save 141.4k tokens" directly under the summary of the job it had just closed out. Every turn of the new task re-pays for 141.4k tokens of a job that is already merged. His note: *"a message would waste tokens, so this pane should be relaunched, rather than continued. We need a detector, this is a stale window."*

**What is stale.** A terminal status verb on an enrolled agent: `done` or `failed`. Nothing else, and each exclusion is load-bearing:

- `needs-decision` / `blocked` / `paused` are agents **waiting on a reply**. A message is the intended unblock; refusing that send would break the fleet's main steering path in order to fix a token leak.
- `unknown` is the fail-open case. `CrewStateForAgent` returns `unknown` whenever the relay is unreachable or nothing was recorded, so treating it as stale turns every transport hiccup into a refused send — trading a token leak for a lost message, the worse failure (robots-ngg5).
- A keep-listed id (`$PARLAY_STATE_HOME/sweep-keep`) is never stale. That list already exists for `sweep` as the escape hatch for long-lived agents that legitimately sit at `done` between jobs (robots-6xq7) — an agent designed to be re-tasked in place is precisely one whose `done` must not read as a spent window. One list, both verbs.

**Age is reported, never decisive.** A five-minute agent that posted `done` is already spent; a six-hour `working` agent is not. `session-start` is read for the reason line only, and any unreadable or future stamp degrades to "unknown age" rather than failing the caller.

**Two surfaces.** `parlay stale <agent-id> [--quiet]` prints `FRESH <id> — <reason>` and exits `0`, or `STALE <id> — <reason>` plus the relaunch commands and exits `3`. Exit 3 deliberately matches `context-check`'s ROTATE: the same question (this session should be replaced by a fresh one) from the other end of the pane, so a caller that already branches on 3 branches correctly here. And `parlay send` gains a second pre-flight, `refuseStaleWindow`, next to the existing registry check: `requireRegisteredTarget` answers *will this message be delivered?*, this one answers the question after it, *should it be?*. The refusal prints the remedy rather than just saying no, because the remedy is the point:

```
parlay send: "mc-robots-g4qz" is a STALE WINDOW (state=done — finished and sitting at
its prompt; session up 1.6h (PR #63 merged)) — refusing to send.
  Relaunch instead of continuing:
    parlay sweep --apply --agent mc-robots-g4qz     # close the finished pane
    parlay-spawn <id> <name> <color> --claim <task-id>   # a fresh pane for the new task
```

`--force` waives it, for the legitimate case: asking a finished agent a follow-up question **about** the work it just finished, where the old context is exactly what you want.

The verb is read-only — it never tears down, never spawns, never sends. Closing a stale window destroys a store, and that decision belongs to `parlay sweep --apply`, which asks the same question with the same holds, not to a detector something might call in a loop. Policy lives in the pure `ClassifyStaleWindow`, so other callers that continue panes (firstmate's `fm-send.sh`, the Pulse panel, `robots-watch`) can adopt the same answer without re-deriving it. Go-only, no TS port — same call as `merge-gate` and `sweep`; keep it out of `tools/cli/parity/run.sh`.
