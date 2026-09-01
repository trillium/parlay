// parlay merge-gate — the truthful "is this PR actually safe to merge?" verb
// (robots-jap6).
//
// The defect this exists to fix: a green status check is NOT evidence that
// anything reviewed the code. CodeRabbit — the only check on this repo, since
// there are no .github/workflows at all — reports a check CONCLUSION of
// `pass` even when it never ran, because the account hit its PR-review rate
// limit. `gh pr checks 45` prints:
//
//	CodeRabbit  pass  0  Review rate limited
//
// The word "pass" is the lie; the free-text description is the only truthful
// field, and `gh pr view` compounds it with mergeStateStatus=CLEAN,
// mergeable=MERGEABLE, reviews=0. Any agent following the standing mechanic
// guardrail "merge when all required checks are green" therefore auto-merges
// completely unreviewed code. PRs #43 and #46 both landed that way.
//
// This is the second known way that check is untruthful — the captain
// previously found it reports success regardless of how many findings the
// review posted. So this verb deliberately does NOT trust the check
// conclusion as the merge signal at all. It asserts, in order:
//
//  1. the PR is open, not conflicting, and not behind its base — a check that
//     ran against an older base has not evaluated the merge that would happen,
//  2. there is at least one check, and no check is failing or still pending,
//  3. no check is a VACUOUS pass — conclusion green but description
//     admitting it did not run (the rate-limit case above),
//  4. a review actually HAPPENED: a human review, or a CodeRabbit comment
//     carrying a real-review marker rather than the rate-limit template,
//  5. that review covered the CURRENT head sha, not an older push,
//  6. no review thread is left unresolved (the findings-count lie).
//
// Exit codes are deliberately fail-closed in every direction: 0 only when
// the PR is genuinely ready (or already merged), 3 when a real blocker in the
// code is found, 5 when the reviewer simply has not finished yet, 6 when the
// only red is a check that failed without ever running the code, 4 when the
// only thing missing is a reviewer that is unavailable, 1 when gh/network
// could not answer, 2 on usage. A caller that just branches on non-zero
// refuses the merge in all six failure modes, which is the correct default
// for a gate.
//
// 4 exists because 3 alone gave the fleet no bounded answer (robots-8kkq).
// "CodeRabbit is rate limited" and "a test is failing" are both non-zero, but
// they call for opposite behavior: the second is fixed by working on the PR,
// while the first cannot be fixed from the PR at all. A mechanic told only
// "blocked" polls forever. Splitting the exit code lets the caller stop with a
// terminating condition instead of burning the night on a wait.
//
// What 4 must NOT do is escalate before spending the one cheap action that can
// change the answer (task-6ch1h). For a long time this verb told the caller
// that re-requesting "has stayed limited across repeated attempts" and to hand
// the choice to the captain without trying it. That advice rested on a
// diagnosis nobody had checked, and it was wrong twice over:
//
//   - Automatic review is not merely unreliable here, it is OFF. CodeRabbit
//     says so in plain text on any PR that asks: "This repository does not
//     receive automatic reviews because it has fewer than 10 stars." So
//     "park until the reviewer returns" was never a terminating strategy —
//     nobody was coming.
//   - An explicit `@coderabbitai review` DOES work. #122 came back "Action
//     performed: Review finished" within a minute and found a real goroutine
//     and fd leak in that PR's own new code.
//
// Six PRs (#116–#121) were merged unreviewed under the old advice, each with a
// no-review disclosure, when one comment apiece would have gotten a real
// review. That is the cost of a gate that hands back a decision it could have
// resolved. The notes on the 4 path now spend the re-request first and escalate
// only after its stated window has lapsed.
//
// The rate limit is real, though — it is just a quota, not a refusal. The free
// tier includes roughly one review per hour and the reply states the wait
// ("Next included review available in NN minutes"), so re-requests have to be
// SEQUENCED across PRs. Firing three at once spends the window on whichever
// lands first; #123 and #124 both got "Action not completed — Review rate
// limited" that way.
//
// A refusal counts wherever it is written down (robots-eowy). CodeRabbit
// edits its ONE comment in place, so a PR whose first push got a real review
// keeps that walkthrough body forever; when a later push is refused, the only
// place the refusal appears is the check DESCRIPTION. Classifying off the
// comment alone made that shape exit 3 — `vacuous-pass` + `stale-review` on
// trillium/no-mistakes#13 — which is the worst possible answer: it sends a
// mechanic hunting a defect in code no reviewer ever objected to, and every
// edit it makes pushes a new head, restarting the review and re-consuming the
// limit that is blocking it. A vacuous check therefore reclassifies
// `stale-review` and `no-review-evidence` exactly as a rate-limit comment
// does. `no-review-evidence` keeping the harsher code was only ever justified
// by the gate not knowing WHY nothing reviewed the PR; a check that states the
// reason is that knowledge.
//
// 6 exists because a check can also fail WITHOUT EVER RUNNING THE CODE
// (robots-6mw2). GitHub Actions jobs die during action setup —
//
//	##[error]Failed to resolve action download info. Error: Service Unavailable
//	##[error]Service Unavailable
//
// — before a single line of the repo executes, and the check reports
// bucket=fail with an empty description, indistinguishable by status alone
// from a genuinely failing test. Three trillium/firstmate runs failed that way
// in one afternoon, and every PR open at the time showed red that had nothing
// to do with its diff. Landing that in 3 tells a mechanic to "fix it on the
// branch" — hunting a defect in code that never executed, on a branch whose
// only real problem is GitHub's availability. This is the exact sibling of the
// vacuous pass: a check that failed without running says as little about the
// diff as a check that passed without running.
//
// The discriminator is the check run's ANNOTATIONS, not its description (which
// GitHub Actions leaves empty). An infra death annotates only GitHub's own
// errors; a real failure always annotates "Process completed with exit code N"
// from the step that ran. So the downgrade requires positive evidence — at
// least one infra annotation and no annotation that looks like the code
// failing — and anything unreadable stays code-class.
//
// 5 exists for the same reason in the opposite direction (robots-rwf8). A
// check that is STILL RUNNING was landing in 3 — the code the mechanic
// contract documents as "blocked on the CODE, fix it on the branch" — even
// though nothing was wrong with the diff and the reviewer had simply not
// finished. Observed on trillium/no-mistakes#11: `check-pending` plus
// `no-review-evidence`, exit 3, and minutes later the SAME unchanged PR
// exited 0. An agent obeying the documented contract goes editing a branch
// that has no defect. "Not yet" is neither "the code is wrong" (3) nor "the
// reviewer will never come" (4); it is its own answer, and the only one of
// the three that is genuinely transient. Re-run the gate; do not edit, do not
// escalate, do not merge.
//
// A green check is also only evidence about the base it ran against
// (robots-1hs5). GitHub runs `pull_request` workflows on refs/pull/N/merge —
// the merge RESULT, not the branch head — and it recomputes that ref when the
// base branch moves, but it does NOT re-trigger the check run. So a PR whose
// CI went green hours ago keeps that green forever, describing a merge with a
// main that no longer exists. Merging it lands a combination no CI ever
// evaluated. trillium/firstmate #76/#77/#79 each stayed green that way and
// collectively broke main with duplicate entries in a shared JSON file
// (robots-ot20) — three PRs that were individually correct and jointly wrong.
//
// The obvious signal does not work: `mergeStateStatus=BEHIND` is only ever
// reported when the base branch has protection with "require branches to be up
// to date before merging" enabled, and the repos this gate runs against have no
// protection at all — every behind PR there reports CLEAN or UNSTABLE. Reading
// that field alone would be a blocker that never fires in exactly the case it
// was written for. So the gate asks the compare API instead
// (`repos/O/R/compare/base...head` -> `behind_by`), which is true regardless of
// repo settings, and treats BEHIND as corroboration when it is present.
//
// Being behind is code-class (3): it is fixable on the branch by merging the
// base in or rebasing, which re-triggers CI against the current base. The gate
// deliberately does NOT try to time-correlate check completion against the base
// tip's commit date to exempt "behind but CI ran after the base moved" — commit
// dates are not push times and can predate the push arbitrarily, so that
// refinement fails OPEN on exactly the force-pushed and rebased branches it
// would matter for. Blocking every behind PR is the same rule GitHub's own
// "require branches to be up to date" enforces, and it costs one rebase.
//
// Separately from all of that, the gate reports when merging is not the same
// act as DEPLOYING (robots-oex0). The mechanic contract proves a fix landed by
// showing origin/main contains the commit — which assumes origin/main is the
// artifact that runs. In `pai-hooks` it is not: `~/.claude/hooks` symlinks
// into the checkout, so local main is live and origin was 20 commits behind
// it. Both halves of the proof failed at once — a merged commit satisfied
// "FIXED" without going live, and a commit that WAS live was not on
// origin/main at all. The gate cannot know what a repo deploys, but it can see
// the drift that makes "merged" and "live" come apart, so it says so in the
// header and leaves the exit code alone: the PR is still safe to merge, it is
// the conclusion drawn afterwards that is not safe.
//
// The same skepticism the gate applies to a check CONCLUSION it now applies to
// head FRESHNESS (robots-bn5d). Every assertion above is evaluated against
// origin's `headRefOid` — which is correct, since that is the commit a merge
// would land — but the caller is a mechanic who has just authored a fix and
// pushed it, and for whom READY reads as "my fix is cleared to merge". On
// trillium/firstmate#91 the fix had gone to the no-mistakes MIRROR and the
// pipeline had not yet pushed it to origin, so origin's head was still the
// pre-fix commit: the gate said READY, and merging there would have landed the
// old head and silently DROPPED the just-authored fix for the reviewer's
// finding. Minutes later, once the pipeline did push, the same PR went 0 -> 4.
// So the READY was not merely early, it was the opposite of the eventual
// truthful answer. The gate cannot see a mirror, but it can see that the local
// branch holds commits origin's PR head does not — which is the same fact from
// the only side it has access to — so an unpushed local head is a PENDING
// blocker, and origin's head sha is printed on every verdict so the
// discrepancy is visible even where the gate cannot check.
package commands
