---
"parlay-cli": minor
---

**`parlay branch-audit` — reversion detection now compares against the merge-base, not the tip (robots-d988).**

`git diff origin/main <branch>` does not answer a question about the branch. Two-dot
diff renders the *symmetric* difference between two tips, so every file that exists
only on main comes back as a `D` (deleted) line, and every line main gained since the
branch was cut comes back as a `-`. A branch that is merely N commits behind therefore
reports as having deleted work it never touched.

Observed on `~/code/firstmate` (robots-90i7). A branch 16 commits behind main read as:

```
git diff --stat origin/main fm/fork-provenance-gaps
  -> 75 files changed, 1607 insertions(+), 2990 deletions(-)
git diff --diff-filter=D --name-only origin/main fm/fork-provenance-gaps
  -> bin/fm-agent-axi.sh, bin/fm-pool-reclaim.sh,
     tests/fm-pool-reclaim.test.sh, tests/fm-test-parlay-guard.test.sh
```

which reads as "this branch reverted PR #101 and PR #92". None of those four files
existed at the branch's merge-base; all four landed on main *afterward*. The branch's
real contribution was 21 files, all additions, 1214 insertions, zero deletions. The
false positive escalated to "do NOT merge this branch, captain decision required,
consider discarding the branch and redoing the work" — an artifact of diff direction
nearly threw away sound work.

`parlay branch-audit [<branch>] [--base <ref>] [--repo <path>] [--json]` never diffs
tip against tip. It reports, separately:

- **true contribution** — `git diff <merge-base> <branch>`;
- **staleness** — "N commits behind" on its own line, as a fact. Being behind removes
  nothing, so it is never counted as a deletion and never makes the verb non-zero;
- **merge strips** — for every merge in `<base>..<branch>`, a diff against its *own*
  parents. A file a parent had ADDED that the merge dropped is a real content strip
  (the union-merge shape). A file absent from the merge that predates both parents was
  deleted deliberately on one side, which is ordinary resolution and only a note.

Exit codes: `0` nothing dropped merged work — including a badly-behind branch, and
including files the branch's own commits delete, since deleting a file is ordinary
work; `3` STRIPPED, a merge dropped a file no commit on the branch authored a delete
for; `1` git could not answer; `2` usage.

Line-level reverts inside a file a merge *modified* rather than deleted stay out of
scope on purpose. That needs semantic review, and claiming to catch it here would be
the same overreach this verb exists to remove.

The mechanic contract in `claim.go`'s robots DoD now forbids reporting a reversion off
a tip-vs-tip diff and sends the question through this verb.
