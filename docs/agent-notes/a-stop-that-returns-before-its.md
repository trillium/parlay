# A stop() that returns before its goroutine does is not a stop()

Found 2026-08-26 in `tools/cli/internal/monitor/watchdog.go`, chasing
`task-hgm5s` — four tests that failed under `go test -race` while CI, which
runs without `-race`, stayed green.

## What it looked like

A bug filed as "flaky under `-race`". The natural reading is that the race
detector is being fussy: the tests are timing-sensitive, `-race` slows the
binary, some deadline slips. That reading is wrong, and it is worth writing
down because it is the reading that keeps a real defect alive.

Three of the four failures were one bug in a test helper. The fourth was a
production defect that nothing else in the repo could have found.

## The production defect

`startRegistryWatchdog` polls `/api/chat/agents` and, after two clean absences,
calls a `retire` callback. Its caller is a respawn loop:

```go
childPID := cmd.Process.Pid
stopWatchdog := startRegistryWatchdog(agent, func() { terminateProcessTree(childPID) }, …)
runErr := cmd.Wait()
stopWatchdog()
// … loop: spawn a new child with a new pid …
```

So `retire` means **kill the process tree rooted at `childPID`**.

`stopWatchdog` closed a `done` channel and returned. It did not wait. The
goroutine could already be inside `fetchRegistry()` — a real HTTP call with a
15-second timeout — and on return would go on to evaluate its strike count and
call `retire`.

`cmd.Wait()` has already reaped that pid. The number is free. The kernel is
entitled to hand it to something else, and the most likely candidate is the
very next child this same loop spawns. So the failure mode is: **a watchdog
kills a process that is not the one it was watching**, occasionally, on the
captain's box, with no log line that would explain it.

That is the same class as the `pgrep -f` pattern-matching AGENTS.md forbids —
acting on an identifier whose owner may have changed — arrived at from the
other direction.

## Why two fixes were needed, not one

Making `stop()` block until the goroutine exits fixes the *data race*. It does
not fix the *bug*: it just means `stop()` waits patiently while the stray kill
happens.

The property the caller needs is "after `stop()` returns, `retire` will never
be called". That takes both:

- the goroutine re-checks `done` **after** `fetchRegistry` returns, so an
  observation made before the stop cannot act after it;
- `stop()` waits on a `finished` channel, so nothing is still touching shared
  state when it returns.

The plain `bool` re-entry guard also became a `sync.Once` — two goroutines
could each read `false` and both reach `close(done)`, which panics.

## The test-helper defect

`captureStdout` in `supervise_test.go` had a copying goroutine append to a
`strings.Builder` that the test goroutine then read:

```go
defer func() { os.Stdout = orig; w.Close(); <-done; r.Close() }()
fn()
return out.String()   // <-- evaluated BEFORE the defer runs
```

Go evaluates a return expression before running deferred functions, so this
read the builder while the drain had not happened yet. Not just a race — the
returned string could be **truncated**, and truncation fails open: a
`strings.Contains` assertion on missing text just reports the line was absent.

Six other capture helpers in the same repo pass the finished string over a
channel and are all correct. This one was the outlier. The fix was to make it
match them.

## The rule

**A `-race` failure is a report about production, not about the test.** Read it
before deciding the detector is being fussy.

And its corollary, which is the more useful half: when a check exists and is
not being run, a real defect can sit behind a green check indefinitely. `-race`
is now on in CI for all six Go modules for exactly that reason.

## The sequel, found in review of the same PR

The rewritten helper still leaked, on the one path a passing suite never runs.

`t.Fatal` **does not return**. It calls `runtime.Goexit`, which runs deferred
functions and nothing else, so every statement after `fn()` is skipped. Closing
the pipe's write end lived there:

```go
defer func() { os.Stdout = orig }()   // only this ran on a t.Fatal

fn()

os.Stdout = orig
_ = w.Close()                          // skipped
out := <-done
```

With `w` never closed, `io.Copy` never sees EOF and the copying goroutine parks
forever — one leaked goroutine and two leaked file descriptors per bail-out, for
the lifetime of the test binary. It is invisible while tests pass, and it
compounds precisely when they do not: a suite failing inside many captured
functions leaks in proportion to how badly it is failing.

Both closes moved into the deferred cleanup. Double-closing on the normal path
is deliberate — the second `Close` returns `os.ErrClosed`, which is discarded,
and paying that is cheaper than tracking which path already closed what. The
drain stays un-deferred, because it must still precede the read of `done`.

Two things generalize:

- **In a test helper, anything that must happen even when the test fails has to
  be deferred**, because `t.Fatal` unwinds via `Goexit` rather than returning.
  A cleanup written after the callback runs only on the happy path.
- **To test that path, call `runtime.Goexit` directly.** A panic is a different
  unwind and would leave the real path untested. The regression test runs 32
  bail-outs and compares settled goroutine counts; reverting the defer fails it
  at exactly 32.

Worth recording separately: this was caught by CodeRabbit, on the first PR in
this repo that a reviewer actually looked at. Automatic review is off for
trillium/parlay because it has fewer than 10 stars — see `task-zu9nt` and
`task-6ch1h`. An explicit `@coderabbitai review` comment works and is the
supported path.

## Related

- [`ci-is-github-workflows-ci-yml.md`](ci-is-github-workflows-ci-yml.md) — the
  other two "a check that passes because it never looked" gaps found the same
  week.
- [`a-spawned-process-outlives-its-spawner-robots-3pvi.md`](a-spawned-process-outlives-its-spawner-robots-3pvi.md)
  — the process-lifecycle family this belongs to.
