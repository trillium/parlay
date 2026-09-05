# A retry loop against a refused port is a CPU spin, not a poll

`robots-zahn`. Touches `tools/lavish-poll/guards.ts` (the pure budget
arithmetic), `spin.ts` (the watchdog and the pacer that act on it), `index.ts`
and `spin.test.ts`. The split is not decorative — `index.ts` sits under a
250-line pre-commit ceiling with the loop already in it.

## What happened

A `lavish-poll` bridge was found running for 21 hours at 76–98% CPU against
`http://127.0.0.1:53715` — a port nothing had listened on since the Claude
session that spawned it exited. Its ppid was 1: launchd had adopted it.

Two independent defects stacked, and each alone would have been survivable.

## 1. A dead upstream does not long-poll — it rejects instantly

The loop's design assumption was that `GET /api/chat/poll` blocks for ~30s and
returns `{timeout:true}` on expiry, so restarting immediately after a timeout
costs one request every 30s. That holds only while something is listening.
Against a refused connection `fetch` **rejects at the transport layer in
microseconds**, and the `.catch(() => ({timeout:true}))` that keeps the bridge
alive through a blip mapped that rejection onto the identical value. The loop
could not tell the two apart, so "wait 30s and retry" became "retry as fast as
the CPU allows".

With no `--timeout-ms` the deadline is `Infinity` and the `while` condition is
never false, so nothing bounded it. Note the shape: the deadline *was* already
fixed to be a leg of the race rather than only the loop condition — that fix is
about a request that outlives the deadline, and it does nothing for a request
that fails faster than one.

The fix distinguishes the two (`ParlayMsg.failed`, synthesized locally and never
sent by Parlay) and adds:

- **a floor on every iteration** — an iteration that delivered nothing and
  returned faster than `backoffMs` (250ms) sleeps the remainder. Consecutive
  transport failures back off exponentially to a 5s cap; anything else uses the
  flat floor, so a busy channel is not slowed by a guard aimed at a dead one.
- **a give-up budget** — 30 consecutive failures or 60s of unbroken failure,
  whichever lands first, exits 1 with the reason on stderr and nothing on
  stdout. Both bounds are needed: a retry count says nothing without knowing how
  fast the retries came, and a window alone would hold the process open for the
  full window on a handful of slow failures.

Every sleep is capped by whatever remains of `--timeout-ms`, for the same reason
the native grace window is — a caller passing `--timeout-ms 100` must not get
300ms back.

## 2. Nothing checked whether anyone was still listening

Even a perfectly healthy run is pointless once its parent is gone: the bridge
exists to print one JSON line, and there is nobody left to read it. `ppid === 1`
means re-parented to launchd, which is a reliable signal on macOS.

It is checked once at startup and then sampled by an **unref'd 1s timer**, not
at the top of each iteration. That distinction is the whole point: a healthy
Parlay parks the loop in a 30s long-poll and a stalling one parks it until the
deadline, so a per-iteration check would leave a parentless process alive for
minutes — and against a stalling server with no `--timeout-ms`, forever. The
startup check sits *after* the `--agent-reply` POST, because that reply goes to
the captain's chat rather than to this process's parent and is still worth
delivering.

## Testing this without waiting out a production budget

`guards.ts` holds the arithmetic as pure functions so the budget can be
unit-tested directly without a process, and the budget is overridable through
`LAVISH_POLL_UNREACHABLE_WINDOW_MS`, `LAVISH_POLL_MAX_RETRIES`,
`LAVISH_POLL_BACKOFF_MS`, `LAVISH_POLL_MAX_BACKOFF_MS` and
`LAVISH_POLL_ORPHAN_CHECK_MS`. Those are test knobs, not CLI surface — a caller
who wants a bound on a healthy run passes `--timeout-ms`. A malformed override
falls back to the default *and warns*, because a typo that silently disarmed the
guard would be worse than no override at all (same rule `--timeout-ms` applies,
for the same `Number("abc") === NaN` reason).

`spin.test.ts` asserts on **request counts and eventual termination**, never on
elapsed time across the subprocess — a timing bound loose enough not to flake
cannot fail. Pre-fix those counts ran to five figures and the orphan case never
terminated at all; all five tests were confirmed red against `HEAD` before the
fix landed.

## The generalizable rule

Any poll loop in this repo that retries on a failed request needs a floor.
"Wait for the server to answer" is not a rate limit when the server is not
there.
