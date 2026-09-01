package commands

import (
	"fmt"
	"strings"
)

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// FormatMergeGate renders the human report. Blockers are the point of the
// output, so they lead.
func FormatMergeGate(pr ghPRView, v MergeGateVerdict) string {
	var b strings.Builder
	head := fmt.Sprintf("PR #%d", pr.Number)
	if pr.URL != "" {
		head = pr.URL
	}
	switch {
	case v.Merged && v.OriginLagsLive:
		// The whole point of robots-oex0: "MERGED" alone is what a mechanic
		// converts into "FIXED", and here that conversion is invalid.
		fmt.Fprintf(&b, "MERGED — BUT NOT LIVE (origin lags the deployed branch) — %s\n", head)
	case v.Merged:
		fmt.Fprintf(&b, "MERGED — %s\n", head)
	case v.Ready && v.OriginLagsLive:
		fmt.Fprintf(&b, "READY TO MERGE — BUT MERGING WILL NOT MAKE IT LIVE — %s\n", head)
	case v.Ready:
		fmt.Fprintf(&b, "READY — %s\n", head)
	case v.NeedsDecision:
		fmt.Fprintf(&b, "NEEDS-DECISION (%d) — %s\n", len(v.Blockers), head)
	case v.Pending:
		fmt.Fprintf(&b, "PENDING (%d) — %s\n", len(v.Blockers), head)
	case v.Infra:
		fmt.Fprintf(&b, "INFRA (%d) — %s\n", len(v.Blockers), head)
	default:
		fmt.Fprintf(&b, "BLOCKED (%d) — %s\n", len(v.Blockers), head)
	}
	for _, bl := range v.Blockers {
		fmt.Fprintf(&b, "  ✗ %-20s %s\n", bl.Code, bl.Detail)
	}
	for _, n := range v.Notes {
		fmt.Fprintf(&b, "  · %s\n", n)
	}
	if v.Ready && !v.Merged {
		// Name the commit. READY is a verdict about origin's head and nothing
		// else, and the whole robots-bn5d failure was a mechanic reading it as
		// a verdict on the fix they had just pushed somewhere origin had not
		// caught up with. The sha is the one thing that makes the two
		// distinguishable at a glance.
		if v.BehindKnown {
			fmt.Fprintf(&b, "  · Checks green against the current base, a real review covered origin's head %s, no unresolved threads. Merging lands THAT commit — confirm it is the one you mean.\n",
				shortSHA(pr.HeadRefOid))
		} else {
			fmt.Fprintf(&b, "  · Checks green (base freshness unknown — could not compare branch against base), a real review covered origin's head %s, no unresolved threads. Merging lands THAT commit — confirm it is the one you mean.\n",
				shortSHA(pr.HeadRefOid))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
