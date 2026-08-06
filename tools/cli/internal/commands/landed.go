// parlay landed — the truthful "did this fix ACTUALLY land?" proof
// (robots-0a77).
//
// The mechanic contract's whole point is that a premature "FIXED" is itself a
// defect, so it names a two-part proof of landing: `git branch -r --contains
// <sha>` must list origin/main, AND `gh pr view <n> --json state,mergedAt`
// must say MERGED. The second half was written as a bare `gh` command, and a
// bare `gh` command does not mean what it reads like.
//
// gh's base-repo resolution deliberately prefers a remote named `upstream`
// over `origin`. Every clone the fleet works in is a fork — origin=
// trillium/<repo> with an `upstream` remote pointing at the parent project —
// so `gh pr view 14 --json state,mergedAt` reads UPSTREAM's PR #14. PR numbers
// collide freely between the two repositories and the answer is silent and
// well-formed. Observed live on robots-8bao: gh reported state=MERGED,
// mergedAt=2026-04-12 for an unrelated upstream PR from months earlier, while
// trillium/no-mistakes#14 — the actual PR — was still OPEN at a different
// head. That is the single outcome the guardrail exists to prevent, produced
// by the command the guardrail told the mechanic to run. Only the paired
// `git branch -r --contains` check caught it; alone, the gh half was a false
// FIXED claim.
//
// `parlay merge-gate` already resolves the repository correctly (robots-g4qz:
// explicit --repo > the `origin` remote > gh's pick, and only with no usable
// origin). This verb folds the landing proof into the same discipline so the
// mechanic never hand-runs a bare gh command whose repo resolution is
// ambiguous. It asserts, in order:
//
//  1. the local checkout has a remote pointing at the repository being asked
//     about — otherwise the git half of the proof describes a different
//     repository than the gh half, which is the defect itself;
//  2. GitHub answered about that same repository (not a same-numbered PR
//     somewhere else);
//  3. the PR state is MERGED;
//  4. the commit it produced is REACHABLE FROM THE REMOTE BASE BRANCH — the
//     `git branch -r --contains` half, run against the resolved remote rather
//     than a hardcoded `origin/main`.
//
// Both halves are required. Neither is sufficient: (3) alone is the fork
// defect above, and (4) alone cannot distinguish a merged PR from a commit
// pushed straight to main. Exit codes are fail-closed: 0 only when every
// assertion holds, 3 when the proof genuinely fails, 1 when gh or git could
// not answer at all, 2 on usage. A caller that branches on non-zero refuses to
// claim FIXED in all three failure modes.
package commands

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// ExitNotLanded is distinct from 1 (gh/git could not answer) so a scripted
// caller can tell "the proof ran and failed" from "the proof could not run".
// Both are non-zero — a landing proof must fail closed either way. It shares
// the value of ExitMergeBlocked deliberately: to every caller in the mechanic
// contract, 3 already means "the verb answered and the answer is no".
const ExitNotLanded = 3

// ghMergeCommit is `gh pr view --json mergeCommit`'s shape. It is the commit
// GitHub actually put on the base branch, which for a squash or rebase merge
// is NOT the PR's head — so this, not headRefOid, is what containment must be
// proven for.
type ghMergeCommit struct {
	OID string `json:"oid"`
}

// ghLandedPR is the subset of `gh pr view --json …` this proof reads.
type ghLandedPR struct {
	Number      int           `json:"number"`
	URL         string        `json:"url"`
	State       string        `json:"state"`
	MergedAt    string        `json:"mergedAt"`
	MergeCommit ghMergeCommit `json:"mergeCommit"`
	HeadRefOid  string        `json:"headRefOid"`
	BaseRefName string        `json:"baseRefName"`
}

// LandedSnapshot is everything the proof needs, already gathered. Keeping it a
// plain struct is what lets ComputeLanded stay pure and unit-testable with no
// network, no gh binary, and no git repository.
type LandedSnapshot struct {
	PR ghLandedPR
	// Repo is the "owner/name" every gh call in this run was pinned to, and
	// RepoSource says how it was chosen — the same resolution merge-gate uses.
	Repo       string
	RepoSource string
	// Remote is the name of the local git remote whose URL points at Repo
	// (""  when no remote does). This is the link between the two halves of
	// the proof: without it, the containment check is about some other
	// repository and proves nothing about the PR.
	Remote string
	// RemoteRepo is what that remote actually points at, reported so a
	// mismatch is visible rather than inferred.
	RemoteRepo string
	// InRepo is false when this is not a git work tree at all.
	InRepo bool
	// Branch is the remote-tracking ref containment is required on, e.g.
	// "origin/main". BranchSource says how it was chosen.
	Branch       string
	BranchSource string
	// ProofSHA is the commit whose reachability was checked, and ProofSHAKind
	// says which commit that is ("merge commit" or "head").
	ProofSHA     string
	ProofSHAKind string
	// ContainingRefs is `git branch -r --contains <sha>`, one ref per entry,
	// normalized. ContainsKnown is false when git could not answer — the proof
	// then reports the gap instead of silently reading it as "not contained"
	// or as "contained".
	ContainingRefs []string
	ContainsKnown  bool
	ContainsErr    string
	// Fetched records that the remote was fetched because the commit was not
	// present locally, so the output can say why the check took a moment.
	Fetched bool
}

// LandedBlocker is one reason the fix cannot be called landed. Code is a
// stable machine-readable slug; Detail is the human sentence.
type LandedBlocker struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// LandedVerdict is the proof's answer.
type LandedVerdict struct {
	Landed   bool            `json:"landed"`
	Blockers []LandedBlocker `json:"blockers"`
	Notes    []string        `json:"notes"`
	ExitCode int             `json:"exitCode"`
}

func landedBlock(v *LandedVerdict, code, format string, a ...any) {
	v.Blockers = append(v.Blockers, LandedBlocker{Code: code, Detail: fmt.Sprintf(format, a...)})
}

// ComputeLanded is the whole decision, as a pure function of a snapshot.
//
// Every assertion is evaluated — the proof does not short-circuit on the first
// failure — because a mechanic reading the output should see everything that
// is wrong at once rather than one blocker per re-run.
func ComputeLanded(s LandedSnapshot) LandedVerdict {
	v := LandedVerdict{Blockers: []LandedBlocker{}, Notes: []string{}}

	// Which repository this answer is about comes FIRST. The whole defect is
	// an answer that reads perfectly while describing a different repository.
	if s.Repo != "" {
		src := s.RepoSource
		if src == "" {
			src = "unspecified"
		}
		v.Notes = append(v.Notes, fmt.Sprintf("repo: %s (from %s)", s.Repo, src))
	}

	// --- the two halves must be about the same repository ---------------
	switch {
	case !s.InRepo:
		landedBlock(&v, "no-git-checkout",
			"not inside a git work tree, so the `git branch -r --contains` half of the proof cannot run. Run this from the checkout the fix was pushed from.")
	case s.Remote == "":
		landedBlock(&v, "no-remote-for-repo",
			"no git remote in this checkout points at %s, so a containment check here proves nothing about that PR. Run this from a clone of %s, or pass --repo for the repository this checkout actually pushes to.",
			repoLabel(s.Repo), repoLabel(s.Repo))
	case s.RemoteRepo != "" && !strings.EqualFold(s.RemoteRepo, s.Repo):
		// Defensive: fetchLandedSnapshot only selects a matching remote, so
		// this is unreachable from the real gatherer. It exists so a
		// hand-built or future snapshot cannot smuggle a mismatch past the
		// proof silently.
		landedBlock(&v, "remote-mismatch",
			"remote %q points at %s but this proof was asked about %s — the git and gh halves would describe different repositories.",
			s.Remote, s.RemoteRepo, s.Repo)
	}

	if got, ok := repoFromPRURL(s.PR.URL); ok && s.Repo != "" && !strings.EqualFold(got, s.Repo) {
		landedBlock(&v, "repo-mismatch",
			"asked GitHub about %s but it answered for %s (%s) — this is the fork/upstream number collision, not a landing.",
			s.Repo, got, s.PR.URL)
	}

	// --- half one: GitHub says MERGED -----------------------------------
	state := strings.ToUpper(strings.TrimSpace(s.PR.State))
	switch state {
	case "MERGED":
		if strings.TrimSpace(s.PR.MergedAt) != "" {
			v.Notes = append(v.Notes, fmt.Sprintf("merged at %s", s.PR.MergedAt))
		}
	case "":
		landedBlock(&v, "pr-state-unknown", "GitHub reported no state for this PR.")
	default:
		landedBlock(&v, "pr-not-merged",
			"PR is %s, not MERGED. An open or closed PR is not a fix: signal `parlay status needs-decision` or `blocked`, never done.", state)
	}

	// --- half two: the commit is on the remote base branch ---------------
	switch {
	case s.ProofSHA == "":
		landedBlock(&v, "no-commit",
			"GitHub reported no merge commit and no head sha, so there is nothing to prove reachability for.")
	case s.Branch == "":
		landedBlock(&v, "no-base-branch",
			"could not determine which remote branch the fix must be on. Pass --branch <remote>/<branch>.")
	case !s.ContainsKnown:
		landedBlock(&v, "containment-unknown",
			"could not run `git branch -r --contains %s`: %s. Unproven is not proven.",
			shortSHA(s.ProofSHA), landedErrOr(s.ContainsErr))
	case !containsRef(s.ContainingRefs, s.Branch):
		landedBlock(&v, "not-on-branch",
			"%s %s is not reachable from %s (%s).",
			s.ProofSHAKind, shortSHA(s.ProofSHA), s.Branch, describeRefs(s.ContainingRefs))
	}

	if s.ProofSHA != "" && s.Branch != "" {
		src := s.BranchSource
		if src == "" {
			src = "unspecified"
		}
		v.Notes = append(v.Notes, fmt.Sprintf("proof: %s %s vs %s (from %s)",
			s.ProofSHAKind, shortSHA(s.ProofSHA), s.Branch, src))
	}
	if s.Fetched {
		v.Notes = append(v.Notes, fmt.Sprintf("fetched %s — the commit was not present locally.", s.Remote))
	}

	if len(v.Blockers) == 0 {
		v.Landed, v.ExitCode = true, config.ExitOK
		v.Notes = append(v.Notes,
			"Both halves hold: GitHub says MERGED for this repository, and the commit is on the remote base branch.")
		return v
	}
	v.ExitCode = ExitNotLanded
	v.Notes = append(v.Notes,
		"NOT landed — do not claim FIXED. Signal `parlay status needs-decision` or `blocked` with the blocker above.")
	return v
}

func landedErrOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "git reported no error text"
	}
	return strings.TrimSpace(s)
}

// containsRef reports whether want is exactly among the refs. Matching is
// deliberately exact rather than a prefix or suffix test — `origin/main` and
// `origin/main-2` are different branches, and a fuzzy match here would turn
// the containment half of the proof back into a guess. `--branch main` is
// normalized to `<remote>/main` before it ever reaches this function.
func containsRef(refs []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, r := range refs {
		if strings.EqualFold(r, want) {
			return true
		}
	}
	return false
}

// describeRefs summarizes what the commit IS on, which is usually the fastest
// way to see what went wrong (typically: only the feature branch).
func describeRefs(refs []string) string {
	if len(refs) == 0 {
		return "it is on no remote branch at all"
	}
	if len(refs) > 4 {
		return "on: " + strings.Join(refs[:4], ", ") + ", …"
	}
	return "on: " + strings.Join(refs, ", ")
}

// FormatLanded renders the human report. Blockers are the point of the
// output, so they lead.
func FormatLanded(pr ghLandedPR, v LandedVerdict) string {
	var b strings.Builder
	head := fmt.Sprintf("PR #%d", pr.Number)
	if pr.URL != "" {
		head = pr.URL
	}
	if v.Landed {
		fmt.Fprintf(&b, "LANDED — %s\n", head)
	} else {
		fmt.Fprintf(&b, "NOT-LANDED (%d) — %s\n", len(v.Blockers), head)
	}
	for _, bl := range v.Blockers {
		fmt.Fprintf(&b, "  ✗ %-20s %s\n", bl.Code, bl.Detail)
	}
	for _, n := range v.Notes {
		fmt.Fprintf(&b, "  · %s\n", n)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Landed is the IO wrapper: gather, decide, print, exit.
func Landed(argv []string) {
	if helpWanted("landed", argv) {
		return
	}
	r := args.Parse("landed", argv, []string{"--json"}, []string{"--repo", "--branch"})

	if len(r.Positionals) == 0 {
		httpc.Die("parlay landed: need a PR number, e.g. landed 45 [--repo owner/name] [--branch origin/main]", config.ExitUsage)
		return
	}
	prNum, err := strconv.Atoi(strings.TrimPrefix(r.Positionals[0], "#"))
	if err != nil || prNum <= 0 {
		httpc.Die(fmt.Sprintf("parlay landed: %q is not a PR number", r.Positionals[0]), config.ExitUsage)
		return
	}
	repoFlag, _ := r.String("--repo")
	branchFlag, _ := r.String("--branch")

	// Same resolution as merge-gate, for the same reason (robots-g4qz): never
	// let gh pick, because its pick is `upstream` in every fork clone.
	repo, repoSource, err := resolveMergeGateRepo(repoFlag)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay landed: %v", err), config.ExitRuntime)
		return
	}

	snap, err := fetchLandedSnapshot(repo, repoSource, branchFlag, prNum)
	if err != nil {
		// Could not answer — exit 1, distinct from a real failed proof, still
		// non-zero.
		httpc.Die(fmt.Sprintf("parlay landed: %v", err), config.ExitRuntime)
		return
	}

	v := ComputeLanded(snap)

	if r.Bool("--json") {
		out, _ := json.MarshalIndent(struct {
			PR         int           `json:"pr"`
			URL        string        `json:"url"`
			Repo       string        `json:"repo"`
			RepoSource string        `json:"repoSource"`
			State      string        `json:"state"`
			MergedAt   string        `json:"mergedAt"`
			SHA        string        `json:"sha"`
			SHAKind    string        `json:"shaKind"`
			Branch     string        `json:"branch"`
			Verdict    LandedVerdict `json:"verdict"`
		}{snap.PR.Number, snap.PR.URL, snap.Repo, snap.RepoSource, snap.PR.State,
			snap.PR.MergedAt, snap.ProofSHA, snap.ProofSHAKind, snap.Branch, v}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Println(FormatLanded(snap.PR, v))
	}

	if v.ExitCode != config.ExitOK {
		httpc.Exit(v.ExitCode)
	}
}

// landedPRFields is the exact --json field set fetchLandedSnapshot requests.
const landedPRFields = "number,url,state,mergedAt,mergeCommit,headRefOid,baseRefName"

// fetchLandedSnapshot gathers both halves of the proof. `repo` is the
// already-resolved "owner/name" and is passed to the gh call explicitly, so
// this can never be the bare-`gh` reading of somebody else's PR.
func fetchLandedSnapshot(repo, repoSource, branchFlag string, pr int) (LandedSnapshot, error) {
	var s LandedSnapshot
	s.Repo, s.RepoSource = repo, repoSource

	viewArgs := []string{"pr", "view", strconv.Itoa(pr), "--json", landedPRFields}
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

	// The commit that actually went onto the base branch. For a squash or
	// rebase merge the head sha never appears on main, so checking headRefOid
	// would report NOT-LANDED for a perfectly landed fix; head is only the
	// fallback for a PR with no merge commit (i.e. one that never merged),
	// where naming the sha still makes the failure legible.
	if oid := strings.TrimSpace(s.PR.MergeCommit.OID); oid != "" {
		s.ProofSHA, s.ProofSHAKind = oid, "merge commit"
	} else if head := strings.TrimSpace(s.PR.HeadRefOid); head != "" {
		s.ProofSHA, s.ProofSHAKind = head, "head"
	}

	s.InRepo = sh("git", "rev-parse", "--is-inside-work-tree").ok
	if !s.InRepo {
		return s, nil
	}

	s.Remote, s.RemoteRepo = remoteForRepo(repo)
	if s.Remote == "" {
		return s, nil
	}

	switch {
	case strings.TrimSpace(branchFlag) != "":
		b := strings.TrimSpace(branchFlag)
		if !strings.Contains(b, "/") {
			b = s.Remote + "/" + b
		}
		s.Branch, s.BranchSource = b, "--branch"
	case strings.TrimSpace(s.PR.BaseRefName) != "":
		s.Branch = s.Remote + "/" + strings.TrimSpace(s.PR.BaseRefName)
		s.BranchSource = "PR base branch"
	}

	if s.ProofSHA == "" || s.Branch == "" {
		return s, nil
	}

	refs, errText, ok := containingRefs(s.ProofSHA)
	if !ok {
		// The commit is very likely just not fetched yet — a mechanic checking
		// straight after a merge is the normal case. Fetch once and retry;
		// still failing after that is a real answer, not a stale checkout.
		if f := sh("git", "fetch", "--quiet", s.Remote); f.ok {
			s.Fetched = true
			refs, errText, ok = containingRefs(s.ProofSHA)
		}
	}
	s.ContainingRefs, s.ContainsErr, s.ContainsKnown = refs, errText, ok
	return s, nil
}

// containingRefs runs the git half of the proof. A non-zero exit means git
// could not answer (typically an unknown commit), which is reported as
// unknown rather than as "not contained" — the two are different, and only
// one of them is safe to read as a failed proof.
//
// This is `git branch -r --contains` in substance, but NOT in form, and the
// difference is load-bearing: `git branch` is porcelain, and a user with
// `column.ui = always` in their gitconfig gets its output laid out in COLUMNS
// even when stdout is a pipe — three refs on one line, so a line-per-ref
// parser sees a single ref named "origin/HEAD -> origin/main   origin/…" and
// matches nothing. That reads as "not on any branch", i.e. this verb
// reporting NOT-LANDED for a fix that landed perfectly. Caught on this fix's
// own smoke test against a merged PR. `for-each-ref` is plumbing: one ref per
// line, no columns, no decoration, no config to honor.
func containingRefs(sha string) (refs []string, errText string, ok bool) {
	res := sh("git", "for-each-ref", "--contains", sha,
		"--format=%(refname:short)", "refs/remotes")
	if !res.ok {
		return nil, firstLine(res.err), false
	}
	return parseRefLines(res.out), "", true
}

// parseRefLines normalizes for-each-ref output, dropping blank lines and the
// symbolic `<remote>/HEAD` entries, which are pointers to another ref rather
// than a branch a commit can be said to have landed on.
func parseRefLines(out string) []string {
	refs := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "HEAD" || strings.HasSuffix(line, "/HEAD") {
			continue
		}
		refs = append(refs, line)
	}
	return refs
}

// remoteForRepo finds the local remote whose URL points at repo, preferring
// one literally named `origin` (the fleet's convention, and the remote
// resolveMergeGateRepo reads) but accepting any match so a differently-named
// fork remote still proves what it should. Returns ("", "") when none match —
// which is a blocker, not a fallback: a containment check against an unrelated
// repository is exactly the failure this verb exists to prevent.
func remoteForRepo(repo string) (name, remoteRepo string) {
	if strings.TrimSpace(repo) == "" {
		return "", ""
	}
	res := sh("git", "remote")
	if !res.ok {
		return "", ""
	}
	fallbackName, fallbackRepo := "", ""
	for _, r := range strings.Split(res.out, "\n") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		u := sh("git", "remote", "get-url", r)
		if !u.ok {
			continue
		}
		got, ok := repoFromRemoteURL(u.out)
		if !ok || !strings.EqualFold(got, repo) {
			continue
		}
		if r == "origin" {
			return r, got
		}
		if fallbackName == "" {
			fallbackName, fallbackRepo = r, got
		}
	}
	return fallbackName, fallbackRepo
}
