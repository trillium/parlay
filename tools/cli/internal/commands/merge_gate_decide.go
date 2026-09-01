package commands

import (
	"fmt"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// ComputeMergeGate is the whole decision, as a pure function of a snapshot.
func ComputeMergeGate(s MergeGateSnapshot) MergeGateVerdict {
	v := MergeGateVerdict{Blockers: []MergeBlocker{}, Notes: []string{}, BehindKnown: s.BehindKnown}

	// Which repository this answer is about comes FIRST, above even the
	// merged short-circuit — the whole robots-g4qz defect was a verdict that
	// read perfectly while describing a different repository's PR.
	if s.Repo != "" {
		src := s.RepoSource
		if src == "" {
			src = "unspecified"
		}
		v.Notes = append(v.Notes, fmt.Sprintf("repo: %s (from %s)", s.Repo, src))
	}
	if got, ok := repoFromPRURL(s.PR.URL); ok && s.Repo != "" && !strings.EqualFold(got, s.Repo) {
		v.Notes = append(v.Notes, fmt.Sprintf(
			"WARNING: asked about %s but GitHub answered for %s — if that is not a repository rename, this verdict is about the wrong PR.",
			s.Repo, got))
	}

	// Whether "merged" even means "live" comes before the MERGED
	// short-circuit, because an already-merged PR in a lagging repo is the
	// exact case the mechanic misreads as done (robots-oex0).
	noteOriginLagsLive(&v, s)
	// Head freshness runs BEFORE the MERGED short-circuit, because a PR merged
	// at a head the local branch is ahead of is not a resolved question — it is
	// this defect already realized, with the fix dropped and the merge sitting
	// there looking like proof it landed (robots-bn5d).
	assessHeadFreshness(&v, s)

	switch strings.ToUpper(s.PR.State) {
	case "MERGED":
		// Already landed. Nothing left to gate — say so plainly rather than
		// running checks that no longer mean anything. The one exception is a
		// head-freshness blocker: that one is about whether WHAT landed is what
		// the caller thinks landed, which merging does not answer.
		v.Merged = true
		v.Notes = append(v.Notes, "PR is already MERGED — nothing left to gate.")
		if len(v.Blockers) == 0 {
			v.Ready, v.ExitCode = true, config.ExitOK
		} else {
			v.ExitCode = ExitMergeBlocked
		}
		return v
	case "CLOSED":
		block(&v, "pr-closed", "PR is CLOSED without being merged.")
		v.ExitCode = ExitMergeBlocked
		return v
	}

	if strings.EqualFold(s.PR.Mergeable, "CONFLICTING") {
		block(&v, "conflicting", "PR conflicts with the base branch (mergeable=CONFLICTING).")
	}

	// --- base freshness -----------------------------------------------
	//
	// Everything below this point reasons about checks and reviews, and both
	// of them are statements about a merge with SOME base. If that base has
	// moved, every one of those statements is about a merge that will not
	// happen (robots-1hs5). Nothing re-runs on a base move, so the gate is
	// the only thing that can notice.
	baseLabel := s.PR.BaseRefName
	if baseLabel == "" {
		baseLabel = "the base branch"
	}
	switch {
	case s.BehindKnown && s.BehindBy > 0:
		block(&v, "behind-base",
			"%s has %d commit(s) this branch does not — every check here ran against a merge with an older %s, and GitHub does not re-run them when the base moves. Merge %s in (or rebase) so CI evaluates the code that would actually land.",
			baseLabel, s.BehindBy, baseLabel, baseLabel)
	case strings.EqualFold(s.PR.MergeStateStatus, "BEHIND"):
		// Only reachable on a protected base that requires up-to-date
		// branches; kept because when it IS present it is authoritative, and
		// it is the one path that still works if the compare call failed.
		block(&v, "behind-base",
			"GitHub reports mergeStateStatus=BEHIND: this branch is out of date with %s, so its green checks describe a merge with an older base. Merge %s in (or rebase).",
			baseLabel, baseLabel)
	case !s.BehindKnown:
		v.Notes = append(v.Notes,
			"Could not compare this branch against its base — whether the checks ran against the CURRENT base is UNKNOWN, not confirmed.")
	}

	checkPending, checkVacuous, rerunHint := evaluateChecks(&v, s)
	evaluateReviewEvidence(&v, s, checkPending, checkVacuous)

	// --- open findings -------------------------------------------------
	if !s.ThreadsKnown {
		v.Notes = append(v.Notes, "Could not read review threads — unresolved findings are UNKNOWN, not zero.")
	} else if s.UnresolvedThreads > 0 {
		block(&v, "unresolved-threads",
			"%d review thread(s) are still unresolved. The check conclusion stays green regardless of finding count, so it will not tell you this.",
			s.UnresolvedThreads)
	}

	finalizeVerdict(&v, s, rerunHint)
	return v
}
