# `gc … --json` exits silently on a config error, so never trust an empty failure

**The rule:** a `gc` invocation with `--json` that exits non-zero may print
**nothing at all** — not on stdout, not on stderr. Treat "exit 1, both streams
empty" as *"the error was discarded"*, never as *"there was no error to
report"*, and re-read the failure through a verb that does not silence itself.
In the gc launch path that verb is `gc --city <dir> config show`, run
read-only, which is what `gcDiagnoseSilentFailure` in
`tools/cli/internal/commands/gc_spawn.go` does automatically.

**Why it happens:** gc's JSON mode routes config-load diagnostics to
`io.Discard`. At the pinned lineage this is one line —
`cmd/gc/cmd_agent.go`: `configWarnWriter(jsonOutput, stderr)` returns
`io.Discard` when `jsonOutput` is true — and the same suppression covers the
error path of `gc session new`, so a city whose config cannot compose reports
its failure to nobody.

**Evidence (robots-wdbvx, 2026-10-18), production binary
`/Users/trilliumsmith/.local/bin/gc` (`0.15.1.trillium`) against a copy of the
live city, `GC_HOME` redirected:**

```
$ gc --city <city> session new parlay.probe-x --json --no-attach
exit=1   stdout ""   stderr ""

$ gc --city <city> session new parlay.probe-x --no-attach      # no --json
exit=1   stderr 'gc session new: city import "bd": remote include
         https://github.com/gastownhall/gascity.git#main is not cached
         at <city>/.gc/cache/includes/…'

$ gc --city <city> config show
exit=1   stderr 'gc config show: city import "bd": … is not cached at …'
```

The first form is exactly what `parlay gc-spawn` runs, and exactly what the
`robots-watch` log recorded for every dead mechanic: *"did not emit typed JSON
(run err: exit status 1): stdout \"\", stderr \"\""* — a report that names no
cause. The 300-second spawn timeout is not the failure mode; the binary exits
immediately.

**What this looked like downstream:** the empty report was read as *"the
city's bead store may not be bootstrapped"* (the message the error text
suggests), which is a plausible and completely wrong diagnosis — the store was
fine, the city could not compose. An uninformative failure is not a neutral
gap; it actively redirects the investigation, so the fix is to make the *code*
recover the real error rather than to teach the reader to be suspicious.

**Do not "fix" this by dropping `--json` from the launch call.** The typed
envelope is the contract (`session_id`, `session_name`, `template` come from it)
and gc's non-JSON mode is human text. Keep the JSON call, and on a parse failure
with both streams empty, re-run the READ-ONLY `config show` in the same child
environment and fold its output into the error. `config show` composes no
session, starts no city, and writes no city state, so it is safe on a failure
path; a verb that *does* work would be a second side effect, not a diagnosis.

**The related trap in the same incident:** the config that would not compose was
the scaffold's own builtin imports. `gc`'s bundled-source resolution is
version-dependent — a versionless `[imports.core]`/`[imports.bd]` resolves
offline at the pinned ref and not at all on the binary that runs `parlay spawn`.
`docs/agent-notes/city-is-the-authored-gas-city-source.md` owns that half.

**What the probe does NOT cover:** composition is the failure it explains, not
every silent failure. This was measured, not assumed — against the fixed
scaffold, `gc session new parlay.bogus --json --no-attach` is *still* exit 1
with both streams empty (an unknown-template refusal), while the same call
without `--json` prints `agent "parlay.bogus" not found in city.toml`; and the
`config show` probe returns exit 0 for it, so the caller keeps the generic
message. Do not "close" that gap by re-running `session new` without `--json`:
on the *success* path that creates a second session. A future probe must use a
verb that cannot act.
