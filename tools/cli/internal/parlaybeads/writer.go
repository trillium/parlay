// The write-side fold for one status write (status-lift unit 3): how a
// `parlay status <verb>` lands on the agent's crew bead. This is the first
// consumer of the unit-2 schema; docs/crew-bead-schema.md stays normative.
//
// Ownership note (report §6.1 point 4): the crew bead this file creates is
// the STATUS ATTACHMENT, not the agent record. The spawn seam owns the agent
// record (the gc session bead minted by gc-spawn); when identity.md carries a
// gc_session stamp the caller passes it through so the attachment points at
// that record (KeyGCSession). This file never mints anything in the city's
// session store.
package parlaybeads

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// DecisionTransitions returns the decision.* metadata merge one keyed status
// write implies (docs/crew-bead-schema.md "Keyed decisions"): needs-decision
// and blocked open the key, resolved closes it, everything else (or a keyless
// write) touches nothing. Both openers match firstmate's fold, which treats
// a keyed `blocked` exactly like a keyed `needs-decision` (report §4.3).
func DecisionTransitions(verb, key string) map[string]string {
	if key == "" {
		return nil
	}
	switch verb {
	case VerbNeedsDecision, VerbBlocked:
		return map[string]string{DecisionKeyPrefix + key: DecisionOpen}
	case VerbResolved:
		return map[string]string{DecisionKeyPrefix + key: DecisionResolved}
	}
	return nil
}

// FindCrewBead locates agentID's crew bead by LabelCrew + agent_id metadata.
// found is false when no crew bead exists yet (first write creates one); an
// error means the store could not answer, which per the fail-open doctrine is
// NOT evidence of absence.
//
// Two beads claiming the same agent means a past create race; rather than
// wedging every future write on ambiguity, the pick is deterministic (lowest
// numeric id suffix, then lexical) so all writers converge on one bead and
// the duplicate reads as inert history.
func FindCrewBead(ctx context.Context, c Client, agentID string) (Bead, bool, error) {
	beads, err := c.ListByLabel(ctx, LabelCrew)
	if err != nil {
		return Bead{}, false, err
	}
	var match *Bead
	for i := range beads {
		if beads[i].Metadata[KeyAgentID] != agentID {
			continue
		}
		if match == nil || beadIDLess(beads[i].ID, match.ID) {
			match = &beads[i]
		}
	}
	if match == nil {
		return Bead{}, false, nil
	}
	return *match, true, nil
}

// beadIDLess orders bead ids numerically by suffix (crew-2 < crew-10),
// falling back to lexical for anything that does not parse.
func beadIDLess(a, b string) bool {
	an, aok := beadIDNum(a)
	bn, bok := beadIDNum(b)
	if aok && bok {
		return an < bn
	}
	return a < b
}

func beadIDNum(id string) (int, bool) {
	i := strings.LastIndexByte(id, '-')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	return n, err == nil
}

// ApplyStatus records one status write on agentID's crew bead, creating the
// bead on the agent's first write (the writer is the one place a crew bead
// may come into existence — libclient.go's "unit-3 decision"). extraMeta
// merges alongside the status keys — the caller uses it for the gc_session
// attachment pointer. Returns the bead id.
//
// Ordering per write: metadata first (verb/key/note/at + decision
// transitions + extraMeta), then the workflow-status projection. Any verb
// may follow any verb, exactly like the status file (report §4.4.4): a
// non-terminal verb on a closed bead sets its status back, rather than
// erroring on "impossible" sequences the file has always allowed.
func ApplyStatus(ctx context.Context, c Client, st CrewStatus, extraMeta map[string]string) (string, error) {
	beadStatus, closeReason, terminal, ok := VerbSpec(st.Verb)
	if !ok {
		return "", fmt.Errorf("parlaybeads: %q is not a writer verb", st.Verb)
	}

	meta := st.StatusMetadata()
	for k, v := range DecisionTransitions(st.Verb, st.Key) {
		meta[k] = v
	}
	for k, v := range extraMeta {
		meta[k] = v
	}

	bead, found, err := FindCrewBead(ctx, c, st.AgentID)
	if err != nil {
		return "", fmt.Errorf("parlaybeads: finding crew bead for %s: %w", st.AgentID, err)
	}
	if !found {
		createMeta := map[string]string{KeyAgentID: st.AgentID}
		for k, v := range meta {
			createMeta[k] = v
		}
		id, err := c.Create(ctx, Bead{
			Title:    "agent " + st.AgentID,
			Status:   StatusOpen,
			Type:     BeadTypeAgent,
			Assignee: st.AgentID,
			Labels:   []string{LabelCrew},
			Metadata: createMeta,
		})
		if err != nil {
			return "", fmt.Errorf("parlaybeads: creating crew bead for %s: %w", st.AgentID, err)
		}
		bead = Bead{ID: id, Status: StatusOpen}
	} else if err := c.MergeMetadata(ctx, bead.ID, meta); err != nil {
		return "", fmt.Errorf("parlaybeads: merging status onto %s: %w", bead.ID, err)
	}

	if terminal {
		if !bead.Closed() {
			if err := c.CloseBead(ctx, bead.ID, closeReason); err != nil {
				return "", fmt.Errorf("parlaybeads: closing %s: %w", bead.ID, err)
			}
		}
		return bead.ID, nil
	}
	if err := c.SetStatus(ctx, bead.ID, beadStatus); err != nil {
		return "", fmt.Errorf("parlaybeads: setting %s status: %w", bead.ID, err)
	}
	return bead.ID, nil
}
