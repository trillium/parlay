# A timing assertion loose enough not to flake is too loose to fail

Found 2026-08-26, writing tests for `tools/lavish-poll/` (PR #119).

## What happened

CodeRabbit pointed out that the native grace window in `tools/lavish-poll/index.ts`
waited a flat `NATIVE_GRACE_MS` (200ms) after Parlay won the race, so a run given
`--timeout-ms 100` could take 300ms — a deadline the code overshoots by a fixed
margin.

The fix is one line:

```ts
const graceMs = Math.max(0, Math.min(NATIVE_GRACE_MS, deadline - Date.now()))
```

The first test written for it asserted on the clock:

```ts
const started = Date.now()
const r = await runBridge({ args: ["doc.md", "--timeout-ms", "50"], ... })
expect(Date.now() - started).toBeLessThan(2_000)
```

It passed. Then the defect was reintroduced — and **it still passed.**

## Why it could never have failed

The bridge runs as a subprocess, so every measurement includes `bun` startup.
That is on the order of 150–400ms and varies run to run and machine to machine.
The quantity under test is 200ms. The noise floor is the same size as the signal.

So the bound had to be wide enough to survive a slow CI runner, which
automatically made it wide enough to swallow the very overshoot it existed to
catch. There is no threshold that satisfies both. The test was not *weak*; it was
incapable, and it would have gone on reporting coverage forever.

## What replaced it

Stop measuring time. Measure something the two implementations *disagree* about:

- Parlay answers instantly, so the grace window opens at ~t0.
- The native side answers at t0+150ms.
- The deadline is t0+100ms.

Capped, `graceMs` is the ~90ms remaining, the native side misses it, and the
emitted record has `dom_snapshot: ""`. Uncapped, a flat 200ms wait catches the
native answer at 150ms and the snapshot is present. Same input, two different
outputs, no clock in the assertion. That version fails without the fix.

The companion test matters as much: with no `--timeout-ms`, `deadline` is
`Infinity`, so the cap must reduce to the full `NATIVE_GRACE_MS` rather than to
zero — otherwise this fix would silently delete the grace window that the
one-AbortController-per-request fix exists to make usable. Pinning both
directions is what stops a bound-tightening fix from becoming a feature deletion.

## The rule

**When the quantity you want to assert on is smaller than the noise floor of how
you are measuring it, change what you measure — not the threshold.**

Loosening a bound to stop a flake is deleting the test in slow motion. If a
timing assertion is the only thing available, that is a signal the behaviour
needs an observable output, not that the test needs more slack.

And the only reason this was caught at all: **test-the-test.** Reintroduce the
defect and confirm the specific test fails. A test written against a fix you
already applied has never once been observed failing, which means nothing is
known about whether it can.

## Related

- The rest of `tools/lavish-poll/`'s tests assert on emitted JSON for the same
  reason.
- Same family as the CI gaps in [`ci-is-github-workflows-ci-yml.md`](ci-is-github-workflows-ci-yml.md):
  a check that passes because it never looked.
