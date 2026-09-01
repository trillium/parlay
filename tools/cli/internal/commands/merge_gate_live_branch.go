package commands

import (
	"fmt"
	"strconv"
	"strings"
)

// noteOriginLagsLive records the merged-is-not-live warning when the local
// base branch is ahead of origin's copy (robots-oex0).
//
// Deliberately notes, not blockers. The drift says nothing bad about the diff
// and nothing on the branch can fix it, so making it exit non-zero would
// refuse merges that are perfectly fine — and a gate that cries wolf on every
// run of a repo like pai-hooks gets ignored on the run that matters. What the
// drift breaks is the INFERENCE the mechanic draws afterwards, so the answer
// is to say plainly that the inference is unavailable here.
func noteOriginLagsLive(v *MergeGateVerdict, s MergeGateSnapshot) {
	l := s.Live
	if !l.Known || l.Ahead <= 0 {
		return
	}
	v.OriginLagsLive = true
	v.Notes = append(v.Notes, fmt.Sprintf(
		"ORIGIN LAGS LIVE: local %s is %d commit(s) ahead of origin/%s, so origin/%s is a lagging mirror of the branch this checkout actually runs. Merging lands the commit on origin/%s — it does NOT put it in the deployed tree. `git branch -r --contains <sha>` will list origin/%s and still be wrong about liveness (robots-oex0).",
		l.Branch, l.Ahead, l.Branch, l.Branch, l.Branch, l.Branch))
	reconcile := fmt.Sprintf("git checkout %s && git pull --ff-only origin %s", l.Branch, l.Branch)
	if l.Behind > 0 {
		// Two-sided divergence: a fast-forward is impossible, so do not
		// suggest one — that is the shape that leaves a mechanic stuck.
		reconcile = fmt.Sprintf("git checkout %s && git merge origin/%s", l.Branch, l.Branch)
	}
	v.Notes = append(v.Notes, fmt.Sprintf(
		"Before claiming FIXED, make the DEPLOYED branch contain the commit and stop the two diverging: after this lands, `%s`, then push local %s to origin. If you cannot, say plainly that the fix is merged but not live — do not report it as done.",
		reconcile, l.Branch))
	if strings.EqualFold(s.PR.Mergeable, "CONFLICTING") {
		// Same root cause, different symptom: a branch cut from the local
		// base drags every unpushed commit into the PR, which GitHub reports
		// as a conflict against a base that never saw them.
		v.Notes = append(v.Notes, fmt.Sprintf(
			"The CONFLICTING status above is most likely this same drift: a branch cut from local %s carries those %d unpushed commit(s) into the PR. `git rebase --onto origin/%s %s <branch>` gives the real diff.",
			l.Branch, l.Ahead, l.Branch, l.Branch))
	}
}

// detectLiveBranchDrift measures the local base branch against
// origin/<base>. Every failure path returns Known=false rather than a zero
// count: "could not tell" and "they agree" are different answers, and only
// one of them licenses the merged-means-fixed inference.
//
// Refs are shared across every linked worktree of a repo, so this reads the
// same answer from a mechanic's isolated worktree as from the primary
// checkout — which matters, because the contract sends every mechanic into a
// worktree. It never checks anything out and never writes.
func detectLiveBranchDrift(base string) LiveBranchState {
	st := LiveBranchState{Branch: strings.TrimSpace(base)}
	if st.Branch == "" {
		return st
	}
	if res := sh("git", "rev-parse", "--is-inside-work-tree"); !res.ok || strings.TrimSpace(res.out) != "true" {
		return st
	}
	local := "refs/heads/" + st.Branch
	remote := "refs/remotes/origin/" + st.Branch
	// A repo with no local copy of the base branch has no deployed branch to
	// disagree with origin, and one with no remote-tracking ref has nothing
	// to compare against. Neither is a defect; both are unknowable.
	if r := sh("git", "rev-parse", "--verify", "--quiet", local); !r.ok {
		return st
	}
	if r := sh("git", "rev-parse", "--verify", "--quiet", remote); !r.ok {
		return st
	}
	// --left-right --count over the symmetric difference: left = commits only
	// on origin (local is behind), right = commits only on local (origin lags).
	r := sh("git", "rev-list", "--left-right", "--count", remote+"..."+local)
	if !r.ok {
		return st
	}
	f := strings.Fields(r.out)
	if len(f) != 2 {
		return st
	}
	behind, err1 := strconv.Atoi(f[0])
	ahead, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return st
	}
	st.Known, st.Behind, st.Ahead = true, behind, ahead
	return st
}
