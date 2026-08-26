# `git diff origin/main <branch>` is not a question about the branch — use `parlay branch-audit` (robots-d988)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Two-dot diff renders the **symmetric** difference between two tips. Every file
that exists only on `origin/main` therefore comes back as a `D` (deleted) line,
and every line main gained since the branch was cut comes back as a `-`. A
branch that is merely N commits behind reads as having deleted work it never
touched — and the report is well-formed and specific, which is what makes it
convincing.

On `~/code/firstmate` (robots-90i7) a 16-commits-behind branch produced "75
files changed, 2990 deletions" and named four files it had "deleted" from two
already-merged PRs. None of the four existed at the branch's merge-base; all
four landed on main *afterward*. The real contribution was 21 files, all
additions, 1214 insertions, **zero** deletions. The false positive escalated to
"do NOT merge, captain decision required, consider discarding the branch and
redoing the work" — that second option would have thrown away sound work over a
diff direction.

`parlay branch-audit [<branch>] [--base <ref>] [--repo <path>] [--json]`
(`tools/cli/internal/commands/branch_audit.go`) never diffs tip against tip. It
reports three things separately:

- **true contribution** — `git diff <merge-base> <branch>`
  (`~/code/firstmate/bin/fm-review-diff.sh` already gets this right, via the
  equivalent `<base>...<branch>`);
- **staleness** — "N commits behind" on its own non-alarming line. Being behind
  removes nothing, so it is never a deletion and never makes the verb non-zero;
- **merge strips** — every merge in `<base>..<branch>` diffed against its **own
  parents**, the only honest test for a merge, since combining two histories is
  that commit's entire job. A file a parent had ADDED that the merge dropped is
  a real strip (exit 3, the union-merge shape). A file absent from the merge
  that predates both parents was deleted deliberately on one side: ordinary
  resolution, only a note.

Exit 0 covers a badly-behind branch **and** files the branch's own commits
delete — deleting a file is ordinary work, and a verb that blocked on it would
teach the fleet to ignore it. Whenever the classifier cannot answer (no
resolvable ancestor) it fails toward "not a strip": a false "this branch
reverted merged work" is the defect, so that is the safe direction here.
Line-level reverts inside a file a merge *modified* rather than deleted are
deliberately out of scope — that needs semantic review, and claiming to catch it
here would be the same overreach this verb removes. It is listed in `run.sh`'s
`GO_ONLY_VERBS` for the help-diff reason described in the B10 section.

Policy lives in the pure `ComputeBranchAudit(BranchAuditSnapshot)`, but the
shape tests in `branch_audit_test.go` build **real throwaway git repositories**:
the defect is about diff direction, which a hand-built snapshot cannot express.
`TestStaleBranchIsNotAReversion` reproduces robots-90i7 and asserts the two-dot
artifact still exists as its own precondition. One sharp edge that test hit:
give every fixture file distinct content, or git's rename detection pairs a
main-only file with a branch-only one and silently drops it from
`--diff-filter=D`. The mechanic contract in `claim.go`'s robots DoD now forbids
reporting a reversion off a tip-vs-tip diff and routes the question here.
**Go-only, no TS port** — same reasoning as `merge-gate` above; no `check`
case in `tools/cli/parity/run.sh`.
