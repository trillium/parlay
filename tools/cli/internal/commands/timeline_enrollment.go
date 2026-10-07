// Turning the relay's control-plane audit trail into claim evidence.
//
// This is the caller's half of the rule `unhanded` obeys: the delivery trail
// says whether a hand-over line exists, and this says whether the relay was the
// delivery path for the channel in the first place. Both are files read off
// disk, so the verdict survives the relay as well as the server.
//
// The mapping from audit actions to intervals is deliberately narrow: only
// `register` and `register-denied` open a claim (a denied claim still proves
// the channel was held, by another caller), only `unregister` closes one, and
// an undated line poisons the interval it belongs to rather than being dropped
// — the verdict it guards must fall toward "not evidence", never toward an
// accusation.
package commands

import (
	"fmt"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/timeline"
)

// enrollmentEvidence builds the claim intervals from the audit trail read from
// the SAME runtime dir as the ledger. A trail that was not read reports why and
// supplies no claims at all, which suppresses the verdict rather than emptying
// it.
func enrollmentEvidence(a relayctl.Audit) timeline.EnrollmentEvidence {
	ev := timeline.EnrollmentEvidence{}
	if a.State != relayctl.TrailRead {
		ev.Reason = auditTrailState(a)
		return ev
	}
	ev.Read = true
	ev.Truncated = a.Truncated
	ev.Claims = map[string][]timeline.Claim{}

	// Read order is write order (append-only, never rotated), so an open claim
	// is simply the newest interval for that channel that no unregister closed.
	open := map[string]int{}
	for _, e := range a.Entries {
		if e.Agent == "" {
			continue
		}
		ts, dated := parseLedgerStamp(e.Ts)
		switch e.Action {
		case "register", "register-denied":
			if _, already := open[e.Agent]; already {
				continue // the channel is already claimed; the interval continues
			}
			c := timeline.Claim{From: ts, HasFrom: dated, Undated: !dated}
			ev.Claims[e.Agent] = append(ev.Claims[e.Agent], c)
			open[e.Agent] = len(ev.Claims[e.Agent]) - 1
		case "unregister":
			i, held := open[e.Agent]
			if !held {
				continue // a release with nothing open releases nothing
			}
			delete(open, e.Agent)
			claim := &ev.Claims[e.Agent][i]
			claim.To, claim.HasTo = ts, dated
			if !dated {
				claim.Undated = true
			}
		}
	}
	return ev
}

// auditTrailState names why the claim trail could not be used. It reads the
// same file the timeline's `audit log` source note reads, so the operator sees
// one story in two places rather than two vocabularies.
func auditTrailState(a relayctl.Audit) string {
	switch a.State {
	case relayctl.TrailAbsent:
		return "no audit trail at " + a.Path + " — this relay has never enrolled a channel here (an older relay build, or another runtime dir)"
	case relayctl.TrailUnreadable:
		if a.Err != nil {
			return fmt.Sprintf("could not read %s (%v)", a.Path, a.Err)
		}
		return "could not read " + a.Path
	default:
		return "the audit trail at " + a.Path + " could not be read"
	}
}
