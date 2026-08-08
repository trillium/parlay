// parlay branch-audit — the truthful "what does this branch actually change,
// and did anything on it drop merged work?" verb (robots-d988).
//
// The defect this exists to fix: `git diff origin/main <branch>` is not a
// question about the branch. Two-dot diff renders the SYMMETRIC difference
// between two tips, so every file that exists only on origin/main comes back
// as a `D` (deleted) line, and every line main gained since the branch was cut
// comes back as a `-`. A branch that is merely N commits behind therefore
// reports as having deleted work it never touched.
//
// Observed on ~/code/firstmate (robots-90i7). The branch fm/fork-provenance-gaps
// was 16 commits behind main, and:
//
//	git diff --stat origin/main fm/fork-provenance-gaps
//	  -> 75 files changed, 1607 insertions(+), 2990 deletions(-)
//	git diff --diff-filter=D --name-only origin/main fm/fork-provenance-gaps
//	  -> bin/fm-agent-axi.sh, bin/fm-pool-reclaim.sh,
//	     tests/fm-pool-reclaim.test.sh, tests/fm-test-parlay-guard.test.sh
//
// That reads as "this branch reverted PR #101 and PR #92". None of those four
// files existed at the branch's merge-base; all four landed on main AFTER it.
// The branch's real contribution was 21 files, all additions, 1214 insertions,
// zero deletions. The false positive escalated to "do NOT merge, consider
// discarding the branch and redoing the work" — an artifact of diff direction
// nearly threw away sound work.
//
// So this verb never diffs tip against tip. It asserts, separately:
//
//  1. TRUE CONTRIBUTION — `git diff <merge-base> <branch>`. Three-dot
//     `<base>...<branch>` is the same range and is what bin/fm-review-diff.sh
//     already uses correctly; branch-audit spells the merge-base out because
//     it needs the sha for the report and for step 3.
//  2. STALENESS — "N commits behind" as its own line. That is the actual
//     condition a stale branch is in, and it is not alarming: being behind
//     removes nothing. It is reported, never counted as a deletion.
//  3. MERGE STRIPS — for a merge commit, the only honest test is against its
//     OWN parents, because a merge's job is to combine them. For every merge
//     in <base>..<branch>, for every parent, a file present in that parent and
//     absent from the merge is examined against the parents' common ancestor:
//     absent there too means that parent ADDED the file and the merge dropped
//     it, which is a real content strip (the union-merge shape that produced
//     robots-l0ev). Present in the ancestor means some side deliberately
//     deleted it, which is ordinary merge resolution and only a note.
//
// Exit codes: 0 when nothing on the branch dropped merged work — including
// when the branch is badly behind, and including when the branch's own
// commits delete files, since deleting a file is ordinary work. 3 ONLY for a
// step-3 strip, the narrow case where a merge dropped a file no commit on the
// branch ever authored a delete for. 1 when git could not answer, 2 on usage.
//
// Deliberately out of scope: line-level reverts inside a file the merge
// modified rather than deleted (the other half of robots-l0ev, where a
// union-merge lifted a `--*)` catch-all above named flag arms). Detecting that
// needs semantic review, not a diff direction, and claiming to catch it here
// would be the same overreach this verb exists to remove.
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

// ExitBranchStripped is returned when a merge on the branch dropped a file
// one of its parents had added. Same value as ExitMergeBlocked and for the
// same reason: a real defect in the branch's content, fixable on the branch.
const ExitBranchStripped = 3

// BranchStrip is one file a merge dropped, and the parent it came from.
type BranchStrip struct {
	Merge  string `json:"merge"`
	Parent string `json:"parent"`
	File   string `json:"file"`
}

// BranchMergeAudit is one merge commit in <base>..<branch>, decomposed
// against its own parents.
type BranchMergeAudit struct {
	SHA     string   `json:"sha"`
	Subject string   `json:"subject"`
	Parents []string `json:"parents"`
	// Strips are files a parent ADDED that the merge dropped — a real strip.
	Strips []BranchStrip `json:"strips"`
	// Resolved are files absent from the merge that also predate both parents,
	// so some side deleted them deliberately. Ordinary merge resolution.
	Resolved []BranchStrip `json:"resolved"`
}

// BranchAuditSnapshot is everything read from git, before any judgement.
// Separated from the verdict so the decision layer is testable without a
// repository, matching MergeGateSnapshot.
type BranchAuditSnapshot struct {
	Repo      string `json:"repo"`
	Branch    string `json:"branch"`
	BranchSHA string `json:"branchSha"`
	Base      string `json:"base"`
	BaseSHA   string `json:"baseSha"`
	MergeBase string `json:"mergeBase"`

	// Behind/Ahead are commit counts, not change counts.
	Behind int `json:"behind"`
	Ahead  int `json:"ahead"`

	// The branch's TRUE contribution, merge-base to branch.
	FilesChanged int      `json:"filesChanged"`
	Insertions   int      `json:"insertions"`
	Deletions    int      `json:"deletions"`
	AddedFiles   []string `json:"addedFiles"`
	DeletedFiles []string `json:"deletedFiles"`

	Merges []BranchMergeAudit `json:"merges"`
}

// BranchAuditVerdict is the decision over a snapshot.
type BranchAuditVerdict struct {
	// Stripped is true only when a merge dropped a parent's added file.
	Stripped bool          `json:"stripped"`
	Strips   []BranchStrip `json:"strips"`
	Notes    []string      `json:"notes"`
	ExitCode int           `json:"exitCode"`
}

// ComputeBranchAudit decides over an already-read snapshot. The only thing
// that can make this non-zero is a merge strip; staleness and the branch's own
// deletions are reported as facts, never as blockers.
func ComputeBranchAudit(s BranchAuditSnapshot) BranchAuditVerdict {
	var v BranchAuditVerdict
	v.ExitCode = config.ExitOK
	v.Strips = []BranchStrip{}
	v.Notes = []string{}

	for _, m := range s.Merges {
		v.Strips = append(v.Strips, m.Strips...)
	}
	if len(v.Strips) > 0 {
		v.Stripped = true
		v.ExitCode = ExitBranchStripped
	}

	// Staleness is its own non-alarming line. Say plainly that it removes
	// nothing, because the whole point of robots-d988 is that a reader took a
	// behind-ness artifact for a reversion.
	if s.Behind > 0 {
		v.Notes = append(v.Notes, fmt.Sprintf(
			"branch is %s behind %s — that is staleness, not reversion; it removes nothing from %s",
			plural(s.Behind, "commit"), s.Base, s.Base))
	} else {
		v.Notes = append(v.Notes, fmt.Sprintf("branch is up to date with %s", s.Base))
	}

	if len(s.DeletedFiles) > 0 {
		v.Notes = append(v.Notes, fmt.Sprintf(
			"%s deleted by the branch's own commits (ordinary work, not a reversion): %s",
			plural(len(s.DeletedFiles), "file"), strings.Join(s.DeletedFiles, ", ")))
	}

	var resolved int
	for _, m := range s.Merges {
		resolved += len(m.Resolved)
	}
	if resolved > 0 {
		v.Notes = append(v.Notes, fmt.Sprintf(
			"%s dropped by a merge that predate both its parents — deliberate resolution on one side, not a strip",
			plural(resolved, "file")))
	}

	if len(s.Merges) > 0 {
		v.Notes = append(v.Notes, fmt.Sprintf("%s on the branch audited against their own parents",
			plural(len(s.Merges), "merge commit")))
	}

	return v
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// FormatBranchAudit renders the human report. The true contribution leads,
// because that is the number a reader came for and the one the two-dot diff
// was corrupting.
func FormatBranchAudit(s BranchAuditSnapshot, v BranchAuditVerdict) string {
	var b strings.Builder
	if v.Stripped {
		fmt.Fprintf(&b, "STRIPPED (%d) — %s vs %s\n", len(v.Strips), s.Branch, s.Base)
	} else {
		fmt.Fprintf(&b, "CLEAN — %s vs %s\n", s.Branch, s.Base)
	}
	fmt.Fprintf(&b, "  merge-base           %s\n", shortSHA(s.MergeBase))
	// Phrased like git's own --shortstat, pluralization included, so a reader
	// can compare it against a diff they ran by hand without wondering whether
	// the two numbers mean the same thing.
	fmt.Fprintf(&b, "  true contribution    %s changed, %s(+), %s(-)  [%s..%s]\n",
		plural(s.FilesChanged, "file"), plural(s.Insertions, "insertion"),
		plural(s.Deletions, "deletion"), shortSHA(s.MergeBase), shortSHA(s.BranchSHA))
	fmt.Fprintf(&b, "  commits              %d ahead, %d behind %s\n", s.Ahead, s.Behind, s.Base)

	for _, st := range v.Strips {
		fmt.Fprintf(&b, "  ✗ merge %s dropped %s, which parent %s had added\n",
			shortSHA(st.Merge), st.File, shortSHA(st.Parent))
	}
	for _, n := range v.Notes {
		fmt.Fprintf(&b, "  · %s\n", n)
	}
	if !v.Stripped {
		b.WriteString("  · No merge on this branch dropped work its parents had added.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// BranchAudit is the IO wrapper: read git, decide, print, exit.
func BranchAudit(argv []string) {
	if helpWanted("branch-audit", argv) {
		return
	}
	r := args.Parse("branch-audit", argv, []string{"--json"}, []string{"--base", "--repo"})

	dir, err := resolveBranchAuditRepo(mustFlag(r, "--repo"))
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay branch-audit: %v", err), config.ExitRuntime)
		return
	}

	branch := ""
	if len(r.Positionals) > 0 {
		branch = r.Positionals[0]
	}
	if branch == "" {
		branch = sh("git", "-C", dir, "symbolic-ref", "--quiet", "--short", "HEAD").out
	}
	if branch == "" {
		httpc.Die("parlay branch-audit: no branch given and HEAD is detached — pass a branch explicitly", config.ExitUsage)
		return
	}

	snap, err := readBranchAudit(dir, branch, mustFlag(r, "--base"))
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay branch-audit: %v", err), config.ExitRuntime)
		return
	}

	v := ComputeBranchAudit(snap)

	if r.Bool("--json") {
		out, _ := json.MarshalIndent(struct {
			Snapshot BranchAuditSnapshot `json:"snapshot"`
			Verdict  BranchAuditVerdict  `json:"verdict"`
		}{snap, v}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Println(FormatBranchAudit(snap, v))
	}

	if v.ExitCode != config.ExitOK {
		httpc.Exit(v.ExitCode)
	}
}

// mustFlag returns a value-flag's string, "" when absent.
func mustFlag(r args.Result, flag string) string {
	s, _ := r.String(flag)
	return s
}

// resolveBranchAuditRepo picks the repository to read: --repo when given,
// else the cwd's git toplevel. Same shape as guard's primary-checkout
// resolution.
func resolveBranchAuditRepo(explicit string) (string, error) {
	dir := explicit
	if dir == "" {
		dir = "."
	}
	top := sh("git", "-C", dir, "rev-parse", "--show-toplevel")
	if !top.ok || top.out == "" {
		if explicit != "" {
			return "", fmt.Errorf("%s is not a git repository", explicit)
		}
		return "", fmt.Errorf("not inside a git repository — pass --repo <path>")
	}
	return top.out, nil
}

// resolveBranchAuditBase picks the authoritative base ref: --base when given,
// else origin/<default> from origin/HEAD, else origin/main, origin/master, and
// only then the local main/master. Remote-tracking refs are preferred because
// a pooled clone's local default branch is routinely stale, which is the same
// reason bin/fm-review-diff.sh fetches before comparing.
func resolveBranchAuditBase(dir string) (string, error) {
	if head := sh("git", "-C", dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); head.ok && head.out != "" {
		return head.out, nil
	}
	for _, cand := range []string{"origin/main", "origin/master", "main", "master"} {
		if sh("git", "-C", dir, "rev-parse", "--verify", "--quiet", cand+"^{commit}").ok {
			return cand, nil
		}
	}
	return "", fmt.Errorf("cannot determine a default base branch — pass --base <ref>")
}

// readBranchAudit reads every fact the verdict needs. Read-only: nothing here
// fetches, checks out, or writes a ref.
func readBranchAudit(dir, branch, baseFlag string) (BranchAuditSnapshot, error) {
	var s BranchAuditSnapshot
	s.Repo, s.Branch = dir, branch

	base := baseFlag
	if base == "" {
		var err error
		if base, err = resolveBranchAuditBase(dir); err != nil {
			return s, err
		}
	}
	s.Base = base

	branchSHA := sh("git", "-C", dir, "rev-parse", "--verify", "--quiet", branch+"^{commit}")
	if !branchSHA.ok || branchSHA.out == "" {
		return s, fmt.Errorf("branch %q does not resolve in %s", branch, dir)
	}
	s.BranchSHA = branchSHA.out

	baseSHA := sh("git", "-C", dir, "rev-parse", "--verify", "--quiet", base+"^{commit}")
	if !baseSHA.ok || baseSHA.out == "" {
		return s, fmt.Errorf("base %q does not resolve in %s", base, dir)
	}
	s.BaseSHA = baseSHA.out

	mb := sh("git", "-C", dir, "merge-base", s.BaseSHA, s.BranchSHA)
	if !mb.ok || mb.out == "" {
		return s, fmt.Errorf("%s and %s share no common ancestor, so there is no honest base to compare against", base, branch)
	}
	s.MergeBase = mb.out

	// Ahead/behind in COMMITS. `rev-list --left-right --count base...branch`
	// prints "<behind>\t<ahead>".
	if lr := sh("git", "-C", dir, "rev-list", "--left-right", "--count", s.BaseSHA+"..."+s.BranchSHA); lr.ok {
		f := strings.Fields(lr.out)
		if len(f) == 2 {
			s.Behind, _ = strconv.Atoi(f[0])
			s.Ahead, _ = strconv.Atoi(f[1])
		}
	}

	// TRUE contribution: merge-base -> branch. Never base-tip -> branch.
	numstat := sh("git", "-C", dir, "diff", "--numstat", s.MergeBase, s.BranchSHA)
	if !numstat.ok {
		return s, fmt.Errorf("could not diff %s..%s: %s", shortSHA(s.MergeBase), shortSHA(s.BranchSHA), firstLine(numstat.err))
	}
	s.FilesChanged, s.Insertions, s.Deletions = parseNumstat(numstat.out)

	s.AddedFiles = namesByStatus(dir, "A", s.MergeBase, s.BranchSHA)
	s.DeletedFiles = namesByStatus(dir, "D", s.MergeBase, s.BranchSHA)

	merges, err := auditBranchMerges(dir, s.MergeBase, s.BranchSHA)
	if err != nil {
		return s, err
	}
	s.Merges = merges

	return s, nil
}

// parseNumstat sums `git diff --numstat` output. A binary file prints "-\t-"
// and contributes to the file count only.
func parseNumstat(out string) (files, insertions, deletions int) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		files++
		if n, err := strconv.Atoi(f[0]); err == nil {
			insertions += n
		}
		if n, err := strconv.Atoi(f[1]); err == nil {
			deletions += n
		}
	}
	return files, insertions, deletions
}

// namesByStatus lists paths with one --diff-filter status between two commits.
func namesByStatus(dir, status, from, to string) []string {
	r := sh("git", "-C", dir, "diff", "--diff-filter="+status, "--name-only", from, to)
	if !r.ok || r.out == "" {
		return nil
	}
	return strings.Split(r.out, "\n")
}

// auditBranchMerges decomposes every merge commit in <mergeBase>..<branch>
// against its own parents. A merge is the one commit shape where "what did
// this remove?" cannot be answered from a single diff, because combining two
// histories is the commit's entire job.
func auditBranchMerges(dir, mergeBase, branchSHA string) ([]BranchMergeAudit, error) {
	list := sh("git", "-C", dir, "rev-list", "--merges", mergeBase+".."+branchSHA)
	if !list.ok {
		return nil, fmt.Errorf("could not list merges in %s..%s: %s",
			shortSHA(mergeBase), shortSHA(branchSHA), firstLine(list.err))
	}
	if list.out == "" {
		return nil, nil
	}

	var out []BranchMergeAudit
	for _, sha := range strings.Split(list.out, "\n") {
		sha = strings.TrimSpace(sha)
		if sha == "" {
			continue
		}
		m := BranchMergeAudit{SHA: sha}
		m.Subject = sh("git", "-C", dir, "log", "-1", "--format=%s", sha).out

		pr := sh("git", "-C", dir, "rev-list", "--parents", "-1", sha)
		if !pr.ok {
			continue
		}
		f := strings.Fields(pr.out)
		if len(f) < 2 {
			continue
		}
		m.Parents = f[1:]

		for _, parent := range m.Parents {
			for _, file := range namesByStatus(dir, "D", parent, sha) {
				st := BranchStrip{Merge: sha, Parent: parent, File: file}
				if deletedDeliberatelyOnAnotherSide(dir, m.Parents, parent, file) {
					m.Resolved = append(m.Resolved, st)
				} else {
					// No side asked for this delete, yet the merge made it.
					m.Strips = append(m.Strips, st)
				}
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// deletedDeliberatelyOnAnotherSide reports whether some parent OTHER than
// `parent` actually asked for `file` to be gone, which makes the merge's
// deletion ordinary resolution rather than a strip.
//
// A side asked for it only when the file is absent from that side AND existed
// at the two sides' common ancestor — that is a real delete authored on that
// side. A file absent from the other side but absent from the ancestor too was
// simply never there: `parent` ADDED it after the split, and the merge dropping
// it is the union-merge strip shape. A file still present on every other side
// is unambiguous: nobody deleted it, so the merge did.
//
// Fails toward "deliberate" whenever git cannot answer. An unanswerable
// question must not be reported as a strip, because a false "this branch
// reverted merged work" is the exact defect this file exists to remove.
func deletedDeliberatelyOnAnotherSide(dir string, parents []string, parent, file string) bool {
	for _, other := range parents {
		if other == parent {
			continue
		}
		if sh("git", "-C", dir, "cat-file", "-e", other+":"+file).ok {
			// Still present on this side, so this side did not delete it.
			continue
		}
		anc := sh("git", "-C", dir, "merge-base", parent, other)
		if !anc.ok || anc.out == "" {
			return true
		}
		if sh("git", "-C", dir, "cat-file", "-e", anc.out+":"+file).ok {
			return true
		}
	}
	return false
}
