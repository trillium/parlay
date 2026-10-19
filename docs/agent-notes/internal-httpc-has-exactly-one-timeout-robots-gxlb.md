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

## Bounded retry is a policy on top of the bounded client, not a third client (robots-wsv8a)

`httpc.PostJSONRetry[T](path, body, httpc.Retry)` exists for the requests that
arm an agent's only reply channel — `parlay listen`'s `register-agent` and
"listening" announce — where one 10s shot landing on a momentarily busy server
cost the agent every steer for the rest of its session, with an error that
could not say whether the server was down or merely slow. It retries a
*transport* failure (timeout, refused, reset, dropped) under a `Retry` policy
— `Attempts`, `PerAttempt`, doubling `Backoff` capped by `MaxBackoff`, and a
wall-clock `Budget` that always wins (no attempt starts past it; the last
attempt's own wait is clipped to what remains) — narrates each failed attempt
on stderr, and only then dies with `ExitRuntime`. The shared policy is
`httpc.EnrollRetry` (6 attempts, 10s each, 0.5s→4s pauses, 30s budget).

Three things it deliberately does **not** do. It never retries a server
*answer*: a non-2xx or an undecodable body dies at once with PostJSON's exact
message, because a server that said no will say no again and a loop there
hides the reason. It never retries a non-transient transport error (bad
scheme, unknown host): those die with the raw error on the first attempt. And
it never adds a client: each attempt builds a fresh `http.Client{Timeout:
wait}` on the shared transport, the `TryPostJSON` shape, so `UnboundedClient`
stays the one timeout-less client and this rule stays true.

The slow-vs-unreachable verdict comes from what the request experienced, not
from a second probe: an `httptrace.ClientTrace.GotConn` hook records whether a
connection was ever obtained, and `classifyTransport` reads it alongside the
error. That flag is the whole trick — Go's client reports a dial-phase timeout
and an awaiting-headers timeout with the identical `context deadline exceeded`
text, so without it "slow" and "down" are indistinguishable on the client. The
final message keeps the `Cannot reach Parlay server at <base>` prefix callers
grep for and appends `SLOW, not down` / `UNREACHABLE` / `UNSTABLE` with the
attempt tally and the one remedy that fits. `retry_test.go` pins all of it
against a stalling `httptest` server, a refused port and a bad scheme;
`internal/monitor/listen_retry_test.go` pins the enrollment wiring.
