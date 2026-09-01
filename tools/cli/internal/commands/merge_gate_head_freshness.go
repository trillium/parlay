package commands

import (
	"fmt"
	"strconv"
	"strings"
)

// assessHeadFreshness reports what the local checkout knows about the commit a
// merge would actually land, and blocks when that commit is missing work the
// local branch already has (robots-bn5d).
//
// Origin's `headRefOid` is printed on every verdict, unconditionally. That is
// the minimum the caller needs: the whole failure was a mechanic reading READY
// as a verdict on the fix they had just written, when the gate had in fact
// answered about the commit before it, and nothing in the output named which
// commit that was.
//
// The blocker is deliberately PENDING-class while the PR is open. Nothing is
// wrong with the diff; the commits simply have not arrived at origin yet, and
// the answer changes on its own once they do — exactly the "not yet" that exit
// 5 exists for. On a MERGED PR it is code-class instead: the merge already
// happened at the stale head, so no amount of waiting fixes it and the work has
// to be pushed and re-landed.
func assessHeadFreshness(v *MergeGateVerdict, s MergeGateSnapshot) {
	head := strings.TrimSpace(s.PR.HeadRefOid)
	if head == "" {
		return
	}
	label := shortSHA(head)
	if b := strings.TrimSpace(s.PR.HeadRefName); b != "" {
		label = fmt.Sprintf("%s on %s", shortSHA(head), b)
	}
	h := s.Head
	merged := strings.EqualFold(s.PR.State, "MERGED")

	if !h.Known {
		reason := h.Reason
		if reason == "" {
			reason = "no local copy of this branch to compare against"
		}
		// "Could not tell" is not "they agree". Say which commit the verdict is
		// about and hand the caller the one-line check the gate could not run.
		// On a MERGED PR there is nothing left to do "before merging" — the
		// same doubt is about what already landed, so say that instead.
		advice := fmt.Sprintf(
			"If you just pushed a fix for this PR, check it yourself before merging: `gh pr view %d --json headRefOid` must match your local HEAD. A push that has not reached origin yet leaves every verdict above describing the PRE-fix commit (robots-bn5d).",
			s.PR.Number)
		if merged {
			advice = "That is the commit that MERGED. If you had a fix in flight for this PR, confirm it is in there — `git branch -r --contains <your sha>` must list origin's default branch. A push that never reached origin means the merge landed the PRE-fix commit and dropped your work (robots-bn5d)."
		}
		v.Notes = append(v.Notes, fmt.Sprintf(
			"origin head: %s — NOT verified against a local branch (%s). %s",
			label, reason, advice))
		return
	}

	switch {
	case h.Ahead > 0:
		what := "would merge"
		if merged {
			what = "merged"
		}
		detail := fmt.Sprintf(
			"local %s is %d commit(s) ahead of origin's PR head %s — those commits are NOT in what %s. If you just pushed through a pipeline (`git push no-mistakes`), the run has not reached origin yet; if you pushed nowhere, they are only on this machine.",
			h.Branch, h.Ahead, shortSHA(head), what)
		if h.Relation == RelationDiverged {
			detail += " The two have also DIVERGED — origin's head is not an ancestor of the local branch, so this is not a plain pending push."
		}
		if merged {
			// Past tense: the stale merge already happened, and the mechanic
			// contract's `git branch -r --contains <sha>` proof will pass for
			// the wrong commit. Nothing transient about it.
			block(v, "head-not-pushed", "%s", detail)
			return
		}
		blockAs(v, "head-not-pushed", ClassPending, "%s", detail)
	case h.Relation == RelationBehind:
		// Harmless for merging — origin simply has commits this checkout has
		// not fetched. Worth naming so it is not mistaken for the case above.
		v.Notes = append(v.Notes, fmt.Sprintf(
			"origin head: %s — local %s is BEHIND it (nothing local is at risk; this checkout is just stale).", label, h.Branch))
	default:
		v.Notes = append(v.Notes, fmt.Sprintf(
			"origin head: %s — local %s agrees, so this verdict is about the commit you have.", label, h.Branch))
	}
}

// detectHeadFreshness measures the local copy of the PR's head branch against
// origin's `headRefOid` (robots-bn5d).
//
// Every failure path returns Known=false with a Reason rather than a zero
// count: "could not tell" and "they agree" are different answers, and only one
// of them licenses reading READY as a verdict on the fix you just wrote.
//
// It is pinned to the repo the gate already resolved (robots-g4qz): a checkout
// whose `origin` is a different repository can hold a same-named branch, and
// comparing against that would invent a blocker out of an unrelated project.
// Refs are shared across every linked worktree of a repo, so this reads the
// same answer from a mechanic's isolated worktree as from the primary
// checkout — which matters, because the contract sends every mechanic into a
// worktree. It never checks anything out, never fetches, and never writes.
func detectHeadFreshness(repo, branch, originHead string) HeadFreshness {
	st := HeadFreshness{Branch: strings.TrimSpace(branch), Relation: RelationUnknown}
	unknown := func(format string, a ...any) HeadFreshness {
		st.Reason = fmt.Sprintf(format, a...)
		return st
	}

	if st.Branch == "" {
		return unknown("GitHub did not report a head branch name")
	}
	originHead = strings.ToLower(strings.TrimSpace(originHead))
	if originHead == "" {
		return unknown("GitHub did not report a head sha")
	}
	if res := sh("git", "rev-parse", "--git-dir"); !res.ok {
		return unknown("not inside a git work tree")
	}

	// Pin to the same repository every gh call was pinned to.
	local, ok := "", false
	if res := sh("git", "remote", "get-url", "origin"); res.ok {
		local, ok = repoFromRemoteURL(res.out)
	}
	switch {
	case !ok:
		return unknown("this checkout has no usable `origin` remote")
	case repo != "" && !strings.EqualFold(local, repo):
		return unknown("this checkout's origin is %s, not %s", local, repo)
	}

	res := sh("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+st.Branch)
	if !res.ok || strings.TrimSpace(res.out) == "" {
		return unknown("no local branch %q in this checkout", st.Branch)
	}
	st.LocalHead = strings.ToLower(strings.TrimSpace(res.out))

	if st.LocalHead == originHead {
		st.Known, st.Relation = true, RelationSame
		return st
	}
	// Counting needs origin's head in this object store. A branch that has
	// never been fetched cannot be compared — and saying so names the fix.
	if res := sh("git", "cat-file", "-e", originHead+"^{commit}"); !res.ok {
		return unknown("origin's head %s is not in this checkout's object store (run `git fetch origin`)", shortSHA(originHead))
	}

	// --left-right against origin's head: left = commits only origin has
	// (this checkout is behind), right = commits only the local branch has
	// (what a merge at origin's head would drop).
	counts := sh("git", "rev-list", "--count", "--left-right", originHead+"..."+st.LocalHead)
	if !counts.ok {
		return unknown("could not compare local %s against origin's head", st.Branch)
	}
	fields := strings.Fields(counts.out)
	if len(fields) != 2 {
		return unknown("could not parse the local/origin commit counts")
	}
	behind, err1 := strconv.Atoi(fields[0])
	ahead, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return unknown("could not parse the local/origin commit counts")
	}

	st.Known, st.Ahead = true, ahead
	switch {
	case ahead > 0 && behind > 0:
		st.Relation = RelationDiverged
	case ahead > 0:
		st.Relation = RelationAhead
	case behind > 0:
		st.Relation = RelationBehind
	default:
		st.Relation = RelationSame
	}
	return st
}
