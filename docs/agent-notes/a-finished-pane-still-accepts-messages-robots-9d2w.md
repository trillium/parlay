# A finished pane still accepts messages — check `parlay stale` before re-tasking one (robots-9d2w)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Registered and live is not the same as **worth sending to**. A pane that
finished its task is indistinguishable, to a sender, from one still working:
both are registered, both are enrolled, both accept a message. So a re-task
lands in the finished session and the new work is done on top of the old
job's whole transcript — one was caught at 70% context with its own harness
offering "/clear to save 141.4k tokens" directly under the summary of the job
it had just merged. Every turn of the new task re-pays for that.

`parlay stale <agent-id>` (`tools/cli/internal/commands/stale.go`) answers it:
exit `0` FRESH, exit `3` STALE plus the relaunch commands. Exit 3 matches
`context-check`'s ROTATE — the same "replace this session" signal from the
other end of the pane. `parlay send` calls the same policy as a second
pre-flight beside `requireRegisteredTarget` (which answers *will this be
delivered?*; this answers *should it be?*); `--force` waives both.

Stale is **only** `done`/`failed` on an enrolled agent, and every exclusion is
load-bearing: `needs-decision`/`blocked`/`paused` are *waiting on a reply*, so
refusing that send would break the fleet's steering path to fix a token leak;
`unknown` fails open, because a relay hiccup must never become a lost message
(robots-ngg5); and a `sweep-keep` id is never stale, since that list already
means "re-tasked in place by design" (robots-6xq7) — one list, both verbs. Age
is reported as evidence, never decisive: a five-minute agent that posted `done`
is spent, a six-hour `working` agent is not. The verb is read-only — closing a
window destroys a store, which belongs to `parlay sweep --apply`. Policy lives
in the pure `ClassifyStaleWindow` so other continuers (firstmate's
`fm-send.sh`, the Pulse panel, `robots-watch`) can adopt it without
re-deriving it. Go-only, no TS port; keep it out of `tools/cli/parity/run.sh`.
