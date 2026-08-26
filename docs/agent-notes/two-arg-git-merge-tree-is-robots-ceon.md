# Two-arg `git merge-tree` is not a predicate — teardown's landed check never fired (robots-ceon)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`isContentLanded` (`tools/cli/internal/commands/teardown.go`, mirrored in
`packages/cli/src/commands-teardown.ts`) is the only thing that lets `parlay
teardown`/`parlay sweep` release an agent whose work landed as a **squash**
commit — the original commits are unreachable from any remote ref, so
`hasUnpushed` is true and the git checks refuse. It was written as two-arg
`git merge-tree <branch> <head>` plus `out == "" || strings.Contains(out,
branch)`. On git >= 2.38 that form prints a bare tree OID, so `out` is never
empty and a branch name like `main` can never occur in 40 hex digits: the
function returned **false for every input** and the escape hatch had never
once fired since it shipped. It failed closed, so nothing broke loudly — it
just read as a working gate for as long as nobody tested it.

The working form (from firstmate's `bin/fm-teardown.sh` `content_in_default`)
is `git merge-tree --write-tree <ref> <head>` compared against `<ref>^{tree}`;
`<ref>` is the *remote-tracking* ref, refreshed with a best-effort fetch first,
because a stale `origin/<default>` cannot see the thing you are asking about.
Every inconclusive path still returns false — teardown refuses rather than
guesses. Pinned by `teardown_test.go` / `commands-teardown.test.ts`, both
running real git repos against a real bare origin; a mocked `sh()` would have
reproduced this bug instead of catching it.

Same ticket, second half: `teardown --help` advertised "PR patch-id (if
available) or merge-tree equality" and the TS source claimed "three
strategies". No patch-id strategy has ever existed in either CLI —
the fold design doc §3.7 (captain-private, not in this repo) describes the
firstmate original, not what was folded. When porting a gate, port its test
too; a gate with no test is indistinguishable from a gate that has never run.
