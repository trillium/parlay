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

// prViewFields is the exact --json field set fetchMergeGateSnapshot requests.
const prViewFields = "number,url,state,mergeable,mergeStateStatus,baseRefName,headRefOid,headRefName,author,reviews,comments"

// reviewThreadsQuery counts unresolved review threads. `gh pr view` has no
// field for thread resolution, so this is the one place GraphQL is needed.
const reviewThreadsQuery = `query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){pullRequest(number:$n){reviewThreads(first:100){nodes{isResolved}}}}}`

// MergeGate is the IO wrapper: fetch, decide, print, exit.
func MergeGate(argv []string) {
	if helpWanted("merge-gate", argv) {
		return
	}
	r := args.Parse("merge-gate", argv, []string{"--json"}, []string{"--repo"})

	if len(r.Positionals) == 0 {
		httpc.Die("parlay merge-gate: need a PR number, e.g. merge-gate 45 [--repo owner/name]", config.ExitUsage)
		return
	}
	prNum, err := strconv.Atoi(strings.TrimPrefix(r.Positionals[0], "#"))
	if err != nil || prNum <= 0 {
		httpc.Die(fmt.Sprintf("parlay merge-gate: %q is not a PR number", r.Positionals[0]), config.ExitUsage)
		return
	}
	repoFlag, _ := r.String("--repo")

	repo, repoSource, err := resolveMergeGateRepo(repoFlag)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay merge-gate: %v", err), config.ExitRuntime)
		return
	}

	snap, err := fetchMergeGateSnapshot(repo, repoSource, prNum)
	if err != nil {
		// Could not answer — exit 1, distinct from a real block, still non-zero.
		httpc.Die(fmt.Sprintf("parlay merge-gate: %v", err), config.ExitRuntime)
		return
	}

	v := ComputeMergeGate(snap)

	if r.Bool("--json") {
		out, _ := json.MarshalIndent(struct {
			PR         int              `json:"pr"`
			URL        string           `json:"url"`
			Repo       string           `json:"repo"`
			RepoSource string           `json:"repoSource"`
			Verdict    MergeGateVerdict `json:"verdict"`
			Checks     []ghCheck        `json:"checks"`
			Live       LiveBranchState  `json:"live"`
			HeadOid    string           `json:"headRefOid"`
			Head       HeadFreshness    `json:"headFreshness"`
			Unresolv   int              `json:"unresolvedThreads"`
			// behindBy is null, not 0, when the compare call could not be
			// made — "unknown" and "up to date" must not look identical to a
			// scripted caller.
			BehindBy *int `json:"behindBy"`
		}{snap.PR.Number, snap.PR.URL, snap.Repo, snap.RepoSource, v, snap.Checks, snap.Live, snap.PR.HeadRefOid, snap.Head, snap.UnresolvedThreads,
			behindByJSON(snap)}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Println(FormatMergeGate(snap.PR, v))
	}

	if v.ExitCode != config.ExitOK {
		httpc.Exit(v.ExitCode)
	}
}

// behindByJSON reports how far behind the base the branch is, or nil when the
// gate could not find out. A scripted caller must be able to tell "0 commits
// behind" from "never asked".
func behindByJSON(s MergeGateSnapshot) *int {
	if !s.BehindKnown {
		return nil
	}
	n := s.BehindBy
	return &n
}
