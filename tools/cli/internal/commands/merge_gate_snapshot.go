package commands

// ghAuthor is a PR/comment author.
type ghAuthor struct {
	Login string `json:"login"`
}

// ghComment is one issue comment on the PR.
type ghComment struct {
	Author ghAuthor `json:"author"`
	Body   string   `json:"body"`
}

// ghReview is one GitHub PR review (distinct from an issue comment).
type ghReview struct {
	Author ghAuthor `json:"author"`
	Body   string   `json:"body"`
	State  string   `json:"state"`
}

// ghCheck is one row of `gh pr checks --json name,state,bucket,description,link`.
// Bucket is gh's normalization of the many per-provider states into
// pass/fail/pending/skipping/cancel.
type ghCheck struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Bucket      string `json:"bucket"`
	Description string `json:"description"`
	// Link is the check's target URL. For GitHub Actions it is
	// .../actions/runs/<run>/job/<checkRunID>, which is the only place the
	// annotations API's id is available from a `gh pr checks` row.
	Link string `json:"link"`
	// Annotations are the check run's own annotations, fetched separately for
	// failing checks only. AnnotationsKnown distinguishes "fetched, and there
	// were none" from "could not read them" — the second must never be
	// mistaken for the first, since only positive evidence may downgrade a
	// failure out of code class.
	Annotations      []ghAnnotation `json:"annotations,omitempty"`
	AnnotationsKnown bool           `json:"annotationsKnown,omitempty"`
}

// ghAnnotation is one entry of `gh api repos/<repo>/check-runs/<id>/annotations`.
type ghAnnotation struct {
	Level   string `json:"annotation_level"`
	Message string `json:"message"`
	Title   string `json:"title"`
}

// ghPRView is the subset of `gh pr view --json …` this gate reads.
type ghPRView struct {
	Number           int         `json:"number"`
	URL              string      `json:"url"`
	State            string      `json:"state"`
	Mergeable        string      `json:"mergeable"`
	MergeStateStatus string      `json:"mergeStateStatus"`
	BaseRefName      string      `json:"baseRefName"`
	HeadRefOid       string      `json:"headRefOid"`
	HeadRefName      string      `json:"headRefName"`
	Author           ghAuthor    `json:"author"`
	Reviews          []ghReview  `json:"reviews"`
	Comments         []ghComment `json:"comments"`
}

// LiveBranchState compares the LOCAL base branch against `origin/<base>` —
// the difference between "this PR merged" and "this fix is live" (robots-oex0).
//
// The mechanic contract proves a fix LANDED with `git branch -r --contains
// <sha>` listing origin/main plus `gh pr view` reporting MERGED. That proof
// silently assumes origin/main IS the deployed artifact. In a repo whose
// working tree is itself the deployment target it is not: `~/.claude/hooks`
// is a symlink into the `pai-hooks` checkout, so the hooks that actually run
// are whatever local `main` says, and origin is a lagging mirror. Measured on
// that repo, origin/main was 20 commits behind local main — so both halves of
// the proof were wrong at once. A commit merged to origin/main satisfied
// "FIXED" without ever going live, and a commit that WAS live (cdaf08f) was
// not on origin/main at all and could never satisfy it.
//
// The gate cannot know what a given repo deploys, and must not guess. What it
// CAN observe is the drift itself, which is exactly the condition under which
// "merged" and "live" are allowed to disagree — so it reports the drift and
// lets the caller stop equating the two.
type LiveBranchState struct {
	// Known is false when the comparison could not be made at all (not in a
	// git work tree, no local branch of that name, no origin remote-tracking
	// ref). The gate then says nothing rather than implying agreement.
	Known bool `json:"known"`
	// Branch is the PR's base branch, e.g. "main".
	Branch string `json:"branch"`
	// Ahead counts commits on the LOCAL base branch that origin's copy does
	// not have — i.e. how far origin lags whatever this checkout runs.
	Ahead int `json:"ahead"`
	// Behind counts commits on origin's copy that the local branch does not
	// have. Normal and harmless on its own (a `git pull` fixes it); reported
	// only because a two-sided divergence needs a merge, not a fast-forward.
	Behind int `json:"behind"`
}

// Relation names how a local branch sits against origin's PR head.
const (
	RelationUnknown  = "unknown"
	RelationSame     = "same"
	RelationAhead    = "ahead"
	RelationBehind   = "behind"
	RelationDiverged = "diverged"
)

// HeadFreshness compares the LOCAL copy of the PR's head branch against the
// `headRefOid` GitHub reports — the difference between "the fix I just wrote"
// and "the commit a merge would actually land" (robots-bn5d).
//
// Everything else this gate asserts is about origin's head, and correctly so.
// But the caller has just authored a fix and pushed it, and reads READY as a
// verdict on THAT. When the push went somewhere origin has not caught up with
// yet — the no-mistakes mirror, whose pipeline pushes to origin asynchronously
// — origin's head is still the pre-fix commit. Merging then lands the old head
// and silently drops the fix, which is the exact premature-FIXED failure the
// mechanic guardrails exist to prevent.
//
// The gate has no way to see a mirror or a pipeline run. What it CAN see, from
// any checkout or linked worktree of the repo, is that the local branch
// contains commits origin's PR head does not — the same fact observed from the
// side the gate has access to, and true for a plain forgotten `git push` as
// well. That is the signal; the pipeline is only one way to produce it.
type HeadFreshness struct {
	// Known is false when the comparison could not be made at all (not in a
	// git work tree, origin points somewhere else, no local branch of that
	// name, or origin's head sha is not in the local object store). The gate
	// then says so explicitly rather than implying agreement — "could not
	// tell" and "they agree" are different answers.
	Known bool `json:"known"`
	// Branch is the PR's head branch, e.g. "fix/robots-u7gu".
	Branch string `json:"branch"`
	// LocalHead is the sha the local branch points at.
	LocalHead string `json:"localHead"`
	// Relation is how LocalHead sits against origin's headRefOid: same, ahead,
	// behind, diverged, or unknown.
	Relation string `json:"relation"`
	// Ahead counts commits on the local branch that origin's PR head does not
	// have — i.e. how much of the local work a merge would drop.
	Ahead int `json:"ahead"`
	// Reason explains a Known=false answer, so the gap is legible.
	Reason string `json:"reason,omitempty"`
}

// MergeGateSnapshot is everything the gate needs about a PR, already fetched.
// Keeping it a plain struct is what lets ComputeMergeGate stay pure and
// unit-testable without a network or a gh binary.
type MergeGateSnapshot struct {
	PR ghPRView
	// Repo is the "owner/name" every gh call in this run was pinned to, and
	// RepoSource says how it was chosen. Both are reported, because a gate
	// that answers about the wrong repository is worse than no gate.
	Repo       string
	RepoSource string
	// Checks is empty when the PR has no checks reported at all — which is
	// itself a blocker, not a pass.
	Checks []ghCheck
	// Live is the local origin-vs-deployed-branch comparison. Zero value
	// (Known=false) means it could not be measured, and the gate stays quiet.
	Live LiveBranchState
	// Head is the local-vs-origin head comparison. Zero value (Known=false)
	// means it could not be measured, which the gate reports rather than
	// treating as agreement.
	Head HeadFreshness
	// UnresolvedThreads counts review threads still marked unresolved.
	UnresolvedThreads int
	// ThreadsKnown is false when the review-thread query could not be run;
	// the gate then reports the gap instead of silently claiming zero.
	ThreadsKnown bool
	// BehindBy is how many commits the base branch has that the head does not
	// — i.e. how stale the base this PR's checks ran against now is
	// (robots-1hs5). Read from the compare API, NOT from mergeStateStatus,
	// which only says BEHIND on a protected branch that requires up-to-date
	// branches. BehindKnown is false when that call could not be made; the
	// gate then reports the gap rather than claiming zero.
	BehindBy    int
	BehindKnown bool
}
