# A PR with a merge conflict gets NO Actions runs — missing checks, not red ones

## The incident

PR #164 (source-contract enrollment, 2026-08-31). The first CI run failed on a
real test bug; a fix was pushed — and no new CI run ever appeared. Not failed:
absent. An empty retrigger commit produced nothing. Close/reopen produced
nothing. Meanwhile other branches' PRs were triggering runs within seconds, so
Actions itself was healthy.

The tell that ruled out every "Actions is slow/broken" theory: the head
commit's check-suites list had suites for netlify, vercel, claude,
coderabbitai, sentry and gitguardian — and **no github-actions suite at all**.
GitHub had never even scheduled the workflow.

## The mechanism

`pull_request`-triggered workflows do not run against your head commit. They
run against the PR's **merge ref** (`refs/pull/N/merge`) — the result of
merging the head into the base. While the PR conflicts with its base, that
merge commit cannot be built, so GitHub silently creates no workflow run for
any pull_request event: not `synchronize`, not `reopened`.

What had happened: between the PR's first (green-triggering) push and the fix
push, other PRs merged to main, one of which added lines to AGENTS.md at the
same spot this PR did. From that moment the PR was conflicted and Actions went
dark — `mergeable: null` / `merge_commit_sha: null` in the API.

Apps that check out the **head SHA** instead of the merge ref (CodeRabbit,
GitGuardian) keep reporting normally, which is what makes this so misleading:
the PR shows *some* green activity on every push, and the four required CI
checks are simply not listed. A "2 passed, 0 failed, 2 total" checks summary
on a repo with 4 required checks IS the symptom.

## The rule

When a push to an open PR produces no CI run within a couple of minutes:

1. Do not spam retrigger commits or close/reopen — a conflicted PR ignores
   both, for the same reason it ignored the push.
2. Check `gh-axi api repos/trillium/parlay/pulls/<n>` for
   `mergeable: false/null` and `merge_commit_sha: null`, or run
   `git merge-tree --write-tree origin/main <branch>` locally (exit 1 =
   conflict — see the two-arg merge-tree note for why `--write-tree` is the
   only honest form).
3. Merge `origin/main` into the branch, resolve, push. The next `synchronize`
   event can build the merge ref again and Actions comes back on its own.

Corollary: on this repo the required checks are what gate the merge, so a
conflicted PR is double-blocked — unmergeable AND unable to earn the checks
that would show it green. The state is self-hiding; only the absence of runs
reveals it.
