# Never merge on a green check alone — run `parlay merge-gate <pr>` (robots-jap6)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


A green status check in this repo is **not** evidence that anything reviewed
the code. CodeRabbit is the only *review* check — the CI jobs described in the
`.github/workflows/ci.yml` section below build and test, they do not review —
and it lies in two known ways: it reports the check CONCLUSION `pass`
when it never ran (the account-wide PR review limit; only the free-text
*description* says "Review rate limited"), and it reports success regardless
of how many findings it posted. `gh pr view` compounds it with
`mergeStateStatus=CLEAN`, `mergeable=MERGEABLE`, `reviews=0`. PRs #43 and #46
both landed completely unreviewed this way.

`parlay merge-gate <pr> [--repo owner/name] [--json]`
(`tools/cli/internal/commands/merge_gate.go`) is the truthful replacement. It
refuses to treat the conclusion as the merge signal: it reads each check's
*description* for a vacuous pass, requires an actual review (a human review,
or a CodeRabbit comment carrying `walkthrough_start` rather than the
rate-limit template), requires that review to name the **current head sha** —
CodeRabbit edits one comment in place, so `createdAt` can never detect a
stale review, but the body always prints the exact `base..head` range it
processed — and counts unresolved review threads via GraphQL, since
`gh pr view` has no field for thread resolution. Exit codes are fail-closed
in every direction: `0` ready/already-merged, `3` blocked on the code, `6`
infra-side failure, `5` review still pending, `4` needs-decision, `1` gh could
not answer, `2` usage. The mechanic contract in `claim.go`'s robots DoD now
sends every merge decision through it.

**Exit 4 is the bounded answer for "the reviewer is unavailable"
(robots-8kkq).** Non-zero alone was not enough: "a test is failing" and
"CodeRabbit is rate limited" are both blocked, but only the first is fixable
on the branch, so a mechanic told just "blocked" polls a rate limit forever —
`@coderabbitai review` recovered one PR once and then stayed limited across
three further attempts over ~40 minutes, and trillium/no-mistakes#7's
follow-up commit merged unreviewed as a result. Every blocker now carries a
`Class` (`code` / `pending` / `reviewer-unavailable`); when *every* blocker is
reviewer-unavailability the verdict is `NeedsDecision` and exit `4`, and the
notes name the only two honest options — merge-and-disclose or park — for the
captain to pick. One code-class blocker among them keeps the whole verdict at
`3`, so the downgrade can never launder a failing test into "the captain's
call".

**Exit 5 is "the review has not finished yet" (robots-rwf8).** A *pending*
check was landing in `3` — the code the mechanic contract documents as
"blocked on the CODE, fix it on the branch" — even though the check had said
nothing about the diff. Observed on trillium/no-mistakes#11: `check-pending` +
`no-review-evidence`, exit 3, and the same unchanged PR exited 0 minutes
later. An agent obeying the documented contract goes editing a branch with no
defect, and the new push restarts the review it was waiting on. "Not yet" is
neither "the code is wrong" (3) nor "the reviewer will never come" (4).
`check-pending` is `pending`-class, and while a check is pending so are
`no-review-evidence` and `stale-review` — a running check *is* the explanation
for both, which is the one thing the gate normally cannot infer. Class
precedence is **code > pending > infra > reviewer-unavailable**: a real finding
always wins, and pending outranks both infra and needs-decision because
escalating to the captain while a review is mid-flight asks for a decision on
information that is about to arrive. Anything whose class is unset counts as
code, so a forgotten class can never become a downgrade.

A stale review is normally code-class (push again, the reviewer catches up) —
but a stale review sitting next to a *live* rate-limit template is
reviewer-unavailability, because the re-review is exactly what is being
refused. That pairing is the no-mistakes#7 shape. A live refusal outranks an
unfinished check: that reviewer has already answered, and the answer was no.

**A refusal counts wherever it is written down, and waiting never clears one
(robots-eowy).** CodeRabbit edits its ONE comment in place, so a PR whose first
push got a real review keeps that walkthrough body forever — and when a later
push is refused, the refusal exists only in the check *description*.
Classifying off the comment alone made that shape (`vacuous-pass` +
`stale-review`, trillium/no-mistakes#13) exit `3`, which sends a mechanic
hunting a defect in code no reviewer ever objected to, and every edit it pushes
restarts the review and re-consumes the limit that is blocking it. A vacuous
check now reclassifies `stale-review` **and** `no-review-evidence` exactly as a
rate-limit comment does; `no-review-evidence` was only kept code-class because
the gate could not tell WHY nothing reviewed the PR, and a check that states
the reason is that knowledge. A *green* check still explains nothing, so an
unexplained missing review keeps the harsher code.

And exit 4 now names the way out: CodeRabbit does **not** re-review when the
rate-limit window lapses — it reviews only on a new push or an explicit
`@coderabbitai review` comment — so "wait and re-run the gate" deadlocks
forever. The notes give the captain three options (re-request /
merge-and-disclose / park) instead of two. The gate deliberately does not post
that comment itself: it is a read-only verb, and a gate called in a poll loop
would spam the reviewer and re-consume the very limit at issue.

**Exit 6 is "a check failed without ever running the code" (robots-6mw2).** A
GitHub Actions job that dies during action setup — `Failed to resolve action
download info` / `Service Unavailable` — reports `bucket=fail` with an **empty
description**, indistinguishable by status alone from a failing test. Three
`trillium/firstmate` runs failed that way in one afternoon and every open PR
showed unrelated red. Landing that in `3` sends a mechanic hunting a defect in
code that never executed; it is the exact sibling of the vacuous pass, since a
check that failed without running says as little about the diff as one that
passed without running. The discriminator is the check run's **annotations**
(`gh api repos/<repo>/check-runs/<id>/annotations`, id = the job id in the
check's link), not its description: a job that ran the code always annotates
`Process completed with exit code N`. The downgrade needs at least one
infra-shaped annotation **and** none that looks like the code failing;
unreadable annotations, an empty list, a non-Actions check, or unknown failure
text all stay `code`-class. A cancelled job is `infra` (an ending without a
verdict), but `The job has exceeded the maximum execution time of …` is
deliberately not — a hung test in the diff annotates exactly that. Precedence
is **code > pending > infra > reviewer-unavailable**: pending outranks infra
because `gh run rerun` refuses a run with jobs still in flight, and infra
outranks needs-decision because re-running the failed jobs is a bounded step
the mechanic can take alone.

**A green check only speaks about the base it ran against (robots-1hs5).**
GitHub runs `pull_request` workflows on `refs/pull/N/merge` — the merge
*result*, not the branch head — and it recomputes that ref when the base moves
but never re-triggers the check. So a PR that went green hours ago keeps that
green while describing a merge with a main that no longer exists, and merging it
lands a combination no CI ever evaluated. trillium/firstmate #76/#77/#79 each
held such a green and, merged in turn, collectively broke main with duplicate
entries in one shared JSON file (robots-ot20) — three individually correct PRs
that were jointly wrong. The gate now blocks `behind-base`, code-class (exit
`3`, fixed by merging the base in or rebasing, which re-triggers CI against the
current base).

The obvious field does not work: `mergeStateStatus=BEHIND` is only reported when
the base branch has protection requiring up-to-date branches, and firstmate's
main has **no protection at all** — `gh api …/branches/main/protection` is a
404, and every behind PR there reports `CLEAN` or `UNSTABLE`. Reading that field
alone would be a blocker that never fires in the exact case it was written for.
`fetchMergeGateSnapshot` asks the compare API instead
(`repos/O/R/compare/<base>...<headSha>` → `behind_by`), which is true on any
repo, and `BEHIND` is kept as corroboration and as the fallback if that call
fails. When neither is available the verdict says base freshness is UNKNOWN
rather than assuming current, and `--json` reports `behindBy: null` so a scripted
caller can tell "never asked" from "0 commits behind". The gate does **not** try
to exempt "behind, but CI ran after the base moved" by comparing check
completion against the base tip's commit date: commit dates are not push times
and can predate the push arbitrarily, so that refinement fails OPEN on exactly
the rebased and force-pushed branches where it would matter.

**Every verdict is about ORIGIN's head — say which commit that is (robots-bn5d).**
That is the right commit to gate, since it is what a merge lands. But the caller
is a mechanic who has just authored a fix and pushed it, and for whom READY
reads as "my fix is cleared to merge". `git push no-mistakes` goes to the
**mirror**, and the pipeline pushes on to origin asynchronously — so on
trillium/firstmate#91 origin's head was still the pre-fix commit while the gate
said READY, and merging there would have landed the old head and silently
DROPPED the fix for the finding that had blocked the PR. Minutes later, once the
pipeline pushed, the same PR went 0 → 4: the READY was not merely early, it was
the opposite of the eventual truthful answer. The gate cannot see a mirror or a
pipeline run, but from any checkout or linked worktree of the repo it can see
that the local branch holds commits origin's PR head does not —
`detectHeadFreshness` measures exactly that, pinned to the already-resolved repo
so a same-named branch elsewhere can never invent a blocker. Ahead (or diverged)
is `head-not-pushed`, **pending**-class, exit 5: nothing is wrong with the diff,
the commits simply have not arrived, and the answer changes on its own. On a
MERGED PR it is code-class instead — the stale merge already happened, waiting
cannot undo it, and `git branch -r --contains <sha>` will pass for the wrong
commit. Behind is only a note (a stale checkout risks nothing). Where no local
branch is available, the gate says the freshness is **UNVERIFIED** rather than
implying agreement, and hands over the one-line check it could not run. Exit 5
now has two shapes with opposite instructions, so its notes name whichever is
present instead of always printing the review-in-flight script.

**Never let `gh` pick the repository implicitly (robots-g4qz).** gh's
base-repo resolution prefers a remote named `upstream` over `origin`, so in a
fork clone — origin=`trillium/<repo>` plus an `upstream` remote, which is every
clone the fleet works in — a bare `gh pr view N` reads the *upstream* project's
PR #N. The numbers collide freely and the failure is silent: a well-formed
verdict about somebody else's pull request, worst case exit 0 "already MERGED"
for a fork PR that is still open and unreviewed. `resolveMergeGateRepo` now
resolves once (explicit `--repo` > `origin` remote > gh's pick, and only with no
usable origin) and passes that one answer to every gh call, and every verdict
prints the repo it answered about. Any new code here that shells out to `gh`
against a PR must pass `--repo` explicitly for the same reason.

**Merged is not always LIVE (robots-oex0).** The contract's landed-proof
(`git branch -r --contains <sha>` lists `origin/main`, `gh pr view` says
`MERGED`) assumes `origin/main` is the artifact that runs. Where the working
tree *is* the deployment target it is not: `~/.claude/hooks` symlinks into the
`pai-hooks` checkout, so local `main` is live, and origin sat **20 commits
behind** it (and 1 ahead — the squash of its PR #5). Both halves failed at
once — a merged commit satisfied "FIXED" without going live, and a commit that
*was* live was not on `origin/main` at all. Same drift, second symptom: a
branch cut from local `main` carries every unpushed commit into the PR, which
GitHub reports as `CONFLICTING`.

`detectLiveBranchDrift` measures it read-only (`git rev-list --left-right
--count origin/<base>...<base>`, using the PR's `baseRefName`; refs are shared
across linked worktrees, so a mechanic's worktree reads the same answer as the
primary). When the local base branch is ahead, the header says
`MERGED — BUT NOT LIVE` / `READY TO MERGE — BUT MERGING WILL NOT MAKE IT LIVE`,
checked *before* the already-MERGED short-circuit. It is a **note, never a
blocker**, and never changes the exit code: the drift says nothing bad about
the diff and nothing on the branch can fix it, so blocking would refuse fine
merges and get the whole signal ignored in the repos where it fires every run.
What it invalidates is the inference drawn afterwards, and that is what the
robots DoD now forbids. An unmeasurable checkout leaves `Known` false and the
gate stays silent — "could not tell" is not "they agree".

Decision logic lives in the pure `ComputeMergeGate(MergeGateSnapshot)` so
`merge_gate_test.go` pins the regressions with no gh binary and no network;
`fetchMergeGateSnapshot` is the only part that shells out. **Go-only, no TS
port** — `bin/parlay` execs the Go binary for everything except
`lavish-import`, so the verb is reachable everywhere, and `packages/cli` is
the retired path. The parity harness that used to diff Go verbs against their
TS counterparts (`tools/cli/parity/run.sh`, and its `GO_ONLY_VERBS` list) was
itself retired in T-08 along with `packages/cli`, so there is nothing to
register the verb with — and nothing that would catch a dropped flag on it
automatically either.

## Why `no-review-evidence` fires on literally every PR in this repo

Found 2026-08-26, after every PR in the range #108–#115 that landed at all —
seven of the eight, since #111 was closed unmerged — went in via
merge-and-disclose. The gate was right every time, and the cause was never
transient:

> This repository does not receive automatic reviews because it has fewer than
> 10 stars.
>
> — CodeRabbit, verbatim, on PR #114

CodeRabbit is installed and authenticated (Plan: Pro Plus, profile CHILL). It
simply never reviews `trillium/parlay` on its own. What it posts instead is a
comment carrying an unchecked **"🔍 Trigger review"** box — which is exactly the
shape the gate classifies as `vacuous-pass`: a `pass` conclusion attached to a
run that did no reviewing.

So the gate's two outcomes both have the same root cause:

| What CodeRabbit did | Gate result | Why |
|---|---|---|
| posted the "fewer than 10 stars" comment | exit 4, `vacuous-pass` | the comment *is* the positive evidence of why nothing reviewed |
| posted nothing at all | exit 3, `no-review-evidence` | no evidence of why, so the gate keeps the harsher code |

That difference is the gate working as designed and should not be "fixed".

### The remedy, which is one comment

Post `@coderabbitai review` on the PR. That triggers a real review — confirmed
working on #115 on 2026-08-26, which had received nothing at all until it was
asked. **Do this before reaching for merge-and-disclose.** Merge-and-disclose
is for a reviewer that is genuinely unavailable; a reviewer waiting to be asked
is not unavailable.

### Better: review before the PR exists

`coderabbit` is also a local CLI (`~/.local/bin/coderabbit`, `cr`). It has no
stars restriction, so it reviews on demand:

```sh
coderabbit review --agent --committed --base origin/main   # structured JSON for agents
coderabbit review                                          # plain text, tracked changes
coderabbit doctor                                          # auth + connectivity preflight
coderabbit update                                          # it ships fast; 0.6.1 was five versions stale
```

`--agent` emits newline-delimited JSON ending in a `{"type":"complete",
"findings":N,"reviewedFiles":[...]}` record — `findings` and `reviewedFiles`
together are the check worth asserting on, because a review that read zero
files also reports zero findings.

Two traps:

- **`--plain` is a top-level flag, not a `review` flag.** `coderabbit review
  --plain` is an error; plain text is the default anyway. Top-level flags
  (`--agent`, `--plain`) and `review` subflags (`--committed`, `--base`,
  `--dir`) are different namespaces, and `review --agent` happens to work only
  because `review` defines its own `--agent`.
- **Piping hides the exit code.** `coderabbit review … | tail -80` reports the
  pipeline's status, so an unknown-option error exits 0 and reads as a clean
  review. Redirect to a file and check `$?`, or set `pipefail`.

### The local CLI is not a way around the bot's rate limit

Do not reach for the CLI *because* the GitHub bot is limited. **They draw on
the same pool of 3 included reviews.** Spending one locally is one the bot
cannot spend on a PR, and vice versa. This was learned the hard way: three
local reviews in a row emptied the pool, and the fourth returned

```json
{"type":"error","errorType":"rate_limit","message":"Rate limit exceeded",
 "recoverable":true,"metadata":{"isProUser":false,"waitTime":"20 minutes", …}}
```

Note `isProUser: false` — **even though the org is on Pro Plus.** The same
payload explains why:

> Usage-based reviews are enabled, but this Git provider account isn't linked
> to an assigned seat for the selected organization. Link or assign the seat,
> or use an Agentic API key, then retry.

So the paid capacity exists and is simply not reaching this machine. Until a
seat is linked or `CODERABBIT_API_KEY` is set, the CLI is a *free-tier* client
wearing a paid subscription's name, and the 3-review pool is the real budget
for the whole repo — bot and CLI together.

Two consequences for planning:

- **Order matters, and it is the opposite of what is convenient.** Review
  locally *before* opening the PR, while a failure costs nothing. Once a PR is
  open and gate-blocked, `@coderabbitai review` is the only action that
  produces gate-visible evidence, so save the remaining budget for it.
- **A rate-limit error is not a review.** The `--agent` stream ends in
  `{"type":"error", …}` with **no** `{"type":"complete"}` record. Assert on the
  presence of `complete` plus its `reviewedFiles` length; a script that only
  greps for `"findings":0` reads an exhausted quota as a clean bill of health —
  the same silent-degradation shape as the rest of this note.

This does not satisfy `parlay merge-gate`, which reads GitHub and cannot see a
local run. Use both: the local review to catch defects before pushing, and the
`@coderabbitai` comment to put reviewable evidence where the gate can find it.
