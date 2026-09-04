# Shell harnesses self-isolate (robots-buu8)

`tools/monitor/parlay-monitor.test.sh` runs the relay monitor and its readers as
test subprocesses. Those children inherit the caller's environment, so a
developer PATH that shadows real system binaries with interactive shims leaks
straight into every polling loop.

## The incident (2026-09-04)

Five tests in sections B/C/D were red. All five trace to one root cause: a
`sleep` guard shim at `~/.local/bin`-style paths (a bun script whose whole job
is to fail loudly when `sleep` is used as if it were a wait strategy — it prints
"sleep is not a wait strategy" and exits 1). That shim shadowed `/bin/sleep`, so
every `sleep 0.1` in the monitor's polling loops aborted and the harness printed
five opaque failures. Under `env -i`, all 36 tests passed. The product was fine;
the environment was not.

`BASH_ENV`/`ENV`/`PROMPT_COMMAND` were NOT set. PATH was the sole leak vector.

## The fix — self-isolate at the entry point, once

`tools/monitor/parlay-monitor.test.sh` gets a preamble that:

1. **Prepends the system dirs** (`/usr/bin:/bin:/usr/sbin:/sbin`) to PATH.
   Unconditionally, not "if not already present" — PATH lookup is order
   sensitive, so the real `/bin/sleep` must sit ahead of any user shim dir.
   (A `case`-guard wrongly saw `/usr/bin` as already later in the dev PATH and
   skipped the pin; macOS has no `/usr/bin/sleep`, only `/bin/sleep`.)
2. **Unsenses the hook carriers** `BASH_ENV ENV PROMPT_COMMAND` and any
   `BASH_FUNC_*` exported-function env entries (`for __f in ${!BASH_FUNC_*}`),
   so an interactive parent can't inject functions into subprocesses.
3. **Scrubs the ambient `PARLAY_*` knobs** the harness manages itself
   (`PARLAY_SERVER`, `PARLAY_RELAY_RUNTIME`, etc.). CI sets `PARLAY_SERVER` to a
   closed port so stray spawns fail fast — but that ambient value leaks into the
   test subprocesses and breaks "unset PARLAY_SERVER" (B5) unless cleared. Each
   `run_monitor`/`launch_monitor` subshell re-exports exactly the knob it needs.
4. **Regression self-check**: spawn a fresh `/bin/sh -c 'sleep 0'` and refuse to
   run (exit 2) if a poisoned `sleep` still survives to a subprocess. Without
   this, a broken isolation degrades back into 5 opaque B/C/D failures.

## The lesson

**A bash harness must prove its own isolation before it tests anything, and that
proof must exercise a subprocess**, not just the harness's own shell — the
subprocess is what inherits the leak. If the test was green under `env -i` but
red in the developer's shell, the harness is not hermetic and the red run is not
evidence about the product.

## Related

- Same family as [`a-timing-assertion-loose-enough-not.md`](a-timing-assertion-loose-enough-not.md):
  harden the entry point and test-the-test rather than weakening an assertion.
- CI inclusion: the test now runs in the shell job's hermetic set
  (`.github/workflows/ci.yml`), where the environment is clean but `PARLAY_SERVER`
  is pinned — which is exactly why the scrub in step 3 is load-bearing.
