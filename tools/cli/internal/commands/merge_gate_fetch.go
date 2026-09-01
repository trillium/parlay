package commands

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// fetchMergeGateSnapshot reads the PR. `repo` is the already-resolved
// "owner/name" from resolveMergeGateRepo and is passed to EVERY gh call, so
// the three sources that make up one verdict can never disagree about which
// repository they describe.
func fetchMergeGateSnapshot(repo, repoSource string, pr int) (MergeGateSnapshot, error) {
	var s MergeGateSnapshot
	s.Repo, s.RepoSource = repo, repoSource

	viewArgs := []string{"pr", "view", strconv.Itoa(pr), "--json", prViewFields}
	if repo != "" {
		viewArgs = append(viewArgs, "--repo", repo)
	}
	res := sh("gh", viewArgs...)
	if !res.ok {
		return s, fmt.Errorf("could not read PR #%d in %s: %s", pr, repoLabel(repo), firstLine(res.err))
	}
	if err := json.Unmarshal([]byte(res.out), &s.PR); err != nil {
		return s, fmt.Errorf("could not parse `gh pr view` output: %w", err)
	}

	// `gh pr checks` exits non-zero whenever any check is failing or pending,
	// which is a normal input to this gate — read stdout regardless of code,
	// and only treat unparseable output as an error.
	checkArgs := []string{"pr", "checks", strconv.Itoa(pr), "--json", "name,state,bucket,description,link"}
	if repo != "" {
		checkArgs = append(checkArgs, "--repo", repo)
	}
	cres := sh("gh", checkArgs...)
	if strings.TrimSpace(cres.out) != "" {
		if err := json.Unmarshal([]byte(cres.out), &s.Checks); err != nil {
			return s, fmt.Errorf("could not parse `gh pr checks` output: %w", err)
		}
	}
	// No stdout means gh reported "no checks reported" — leave Checks empty
	// so the no-checks blocker fires, rather than erroring out.

	// How far the base has moved since this PR's checks ran (robots-1hs5).
	// `gh pr view` cannot answer this — mergeStateStatus only reports BEHIND
	// on a protected branch that requires up-to-date branches — so ask the
	// compare API, which is true on any repo. Best-effort: a failure leaves
	// BehindKnown false and the gate discloses the gap instead of assuming
	// the branch is current. Pinned to the resolved repo like every other gh
	// call here (robots-g4qz).
	if s.PR.BaseRefName != "" && s.PR.HeadRefOid != "" && repo != "" {
		c := sh("gh", "api", fmt.Sprintf("repos/%s/compare/%s...%s", repo, s.PR.BaseRefName, s.PR.HeadRefOid),
			"--jq", ".behind_by")
		if c.ok {
			if n, err := strconv.Atoi(strings.TrimSpace(c.out)); err == nil {
				s.BehindBy, s.BehindKnown = n, true
			}
		}
	}

	// Only failing checks need annotations, and only they pay for the extra
	// API call — a green PR still costs exactly the same three requests it
	// always did.
	for i := range s.Checks {
		switch strings.ToLower(s.Checks[i].Bucket) {
		case "fail", "cancel":
			loadCheckAnnotations(repo, &s.Checks[i])
		}
	}

	// Local, read-only, and never fatal: a checkout that cannot answer this
	// leaves Live.Known false and the gate simply says nothing about liveness.
	s.Live = detectLiveBranchDrift(s.PR.BaseRefName)
	// Local, read-only, and never fatal: a checkout that cannot answer this
	// leaves Head.Known false, and the gate says so instead of implying the
	// local branch agrees.
	s.Head = detectHeadFreshness(repo, s.PR.HeadRefName, s.PR.HeadRefOid)

	if owner, name, ok := splitRepo(repo); ok {
		q := sh("gh", "api", "graphql", "-f", "query="+reviewThreadsQuery,
			"-F", "o="+owner, "-F", "r="+name, "-F", "n="+strconv.Itoa(pr),
			"--jq", "[.data.repository.pullRequest.reviewThreads.nodes[]|select(.isResolved|not)]|length")
		if q.ok {
			if n, err := strconv.Atoi(strings.TrimSpace(q.out)); err == nil {
				s.UnresolvedThreads, s.ThreadsKnown = n, true
			}
		}
	}
	return s, nil
}

// annotationPageSize is what loadCheckAnnotations asks for, and also its
// truncation tripwire: a full page might have a second page behind it holding
// the one annotation that would have proved the check code-class, so a full
// page is treated as unreadable rather than paginated. Real jobs annotate a
// handful of lines; this only ever fires on pathological output.
const annotationPageSize = 100

// loadCheckAnnotations fills in a failing check's annotations in place, which
// is what lets classifyFailedCheck tell a GitHub-side death from a real
// failure (robots-6mw2). Every failure path leaves AnnotationsKnown false, so
// an unreachable API, an unparseable body, a non-Actions check, or a
// suspiciously full page all keep the check code-class.
func loadCheckAnnotations(repo string, c *ghCheck) {
	if repo == "" {
		return
	}
	m := checkRunIDRe.FindStringSubmatch(c.Link)
	if m == nil {
		// Not a GitHub Actions check (CodeRabbit's link is empty, third-party
		// checks point at their own dashboards) — there is no annotations
		// endpoint to ask, so this stays a code-class failure.
		return
	}
	res := sh("gh", "api", fmt.Sprintf("repos/%s/check-runs/%s/annotations?per_page=%d", repo, m[1], annotationPageSize))
	if !res.ok {
		return
	}
	var anns []ghAnnotation
	if err := json.Unmarshal([]byte(res.out), &anns); err != nil {
		return
	}
	if len(anns) >= annotationPageSize {
		return
	}
	c.Annotations, c.AnnotationsKnown = anns, true
}
