# `internal/httpc` has exactly one timeout-less client, and it is not the default (robots-gxlb)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`httpc.Client` is bounded by `httpc.DefaultTimeout` (10s) and is what every
one-shot request uses. `httpc.UnboundedClient` has no total timeout and has
exactly one legitimate caller: `internal/monitor`'s `pollOnce`, because
`packages/go-server` holds `GET /api/chat/poll` open for 25s before answering
with its `{"timeout":true}` marker — a bounded client severs every poll in
flight. Do not widen its use, and do not "fix" it by giving it a timeout.
`TryGetJSON`/`TryPostJSON` both take an explicit timeout; pass
`DefaultTimeout` unless there is a reason to pick something else.

The reason this is a rule and not a preference: `TryPostJSON` used to use the
shared, unbounded client, and it is what `parlay supervise` posts the
supervisor wake through. A relay that accepted the connection and never
answered hung the process forever — and firstmate's `bin/fm-watch.sh` mirrors
actionable status wakes through `parlay supervise`, so that hang would have
frozen the whole supervision loop. (Firstmate independently wraps every
`parlay` call in `timeout`/`gtimeout` via `bin/fm-parlay-lib.sh`'s
`fm_parlay_run`; that workaround stays, but it was covering for this.)

**`supervise`'s failure paths exit non-zero — keep it that way.** Both halves
used to print an error and exit 0. On a failed wake post it also printed the
success-shaped `supervisor woken: ...` line to stdout while deliberately not
advancing the seen marker (correct for event-loss safety), so the same line
re-fired on every call and a caller reading stdout would loop forever; on
`--drain` failure a caller could not tell a delivered drain from a retained
queue by exit code. Both now `httpc.Die(..., config.ExitRuntime)`. The marker
is still left un-advanced on failure — that is the event-loss guarantee, and
`TestSuperviseRetriesTheSameLineAfterTheRelayRecovers` pins it.
