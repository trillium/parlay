# Go CLI ticket B8: `resolve-handoff`/`say-guard` — the port was already done, only the dispatch wire was missing

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Ticket B8 targeted porting `packages/cli/src/resolve-handoff.ts` and
`say-guard.ts` (the create->submit death-window fix: `identity
--submit`/`--park`/`--handoff` resolve a missing id from the newest open
handoff bead; `parlay say`/`reply` warns loudly-but-non-blocking on stderr
when sent inside that unsubmitted window, with a separate gentle warning for
a handoff *inherited* from a prior session on the same agent id). By the
time this ticket ran, B1 (`internal/resolvehandoff`, `internal/sayguard`,
and `internal/identity/mem.go`'s `--handoff`/`--submit`/`--park` id
auto-resolution) had already landed the entire port faithfully, including
`internal/identity/say.go`'s `CmdSay` calling
`sayguard.WarnIfUnsubmittedHandoff` — B1 just deliberately left `say`/`reply`
out of `main.go`'s dispatch switch (see B1's note, previously in `say.go`'s
header) because that ticket's DoD only covered `identity`/`scratchpad`. B8's
actual remaining work was: add the `case "say", "reply":` arm to `main.go`,
plus the test coverage that gap had also left missing (`internal/sayguard`
had zero tests; `internal/identity/say_test.go` — CmdSay's own dispatch —
didn't exist, mirroring the TS side where `say.ts` similarly has no
`say.test.ts`, only `say-guard.test.ts`/`resolve-handoff.test.ts`). Lesson
for future tickets in this workstream: a "port X" ticket brief can already
be substantially or fully done by an earlier ticket's broader scope — grep
for the target package/behavior across `internal/` before assuming a blank
slate.
