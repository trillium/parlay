package supersession

import "fmt"

// This file is the reprocessing half of the policy: what downstream work a
// supersession's severity requires (#128 §16, §18, §21–§22), surfaced as
// durable Requirements that stay pending until an actor resolves them with
// evidence.
//
// The engine deliberately does NOT own the dependency graph. Enumerating
// which dependent beads a supersession touches is staleness propagation's
// side of the seam (internal/staleness, task-4cfpv.14 — its
// docs/staleness-model.md names the seam): when a requirement here carries
// StalenessSource, the wiring layer calls
// staleness.Bump(supersededID, newVersion, "superseded:<severity>") and the
// staleness engine derives which dependents went stale. One requirement per
// supersession, many stale dependents per requirement. Only major
// supersessions are staleness-inducing: a minor's outputs are presumed
// valid (revalidation is owed, invalidation is not), and pushing presumed-
// valid work into the staleness graph would erase that distinction.

// Action is the downstream work a severity class requires.
type Action string

const (
	// ActionNone: no requirement is emitted. Patch severity, nothing the
	// captain acted on: nothing consumer-visible changed and no authority
	// relied on the superseded record, so silence is honest.
	ActionNone Action = "none"
	// ActionNotice: visibility only, no work mandated. Emitted instead of
	// ActionNone when the captain acted on the superseded record — the
	// captain-authority rule below.
	ActionNotice Action = "notice"
	// ActionRevalidate: minor severity. Downstream outputs are presumed
	// valid but the presumption must be confirmed by whatever workflow
	// owns them (#128 §19: "we aren't sure this previous result remains
	// valid; check it").
	ActionRevalidate Action = "revalidate"
	// ActionReprocess: major severity. Downstream outputs are presumed
	// invalid; dependent work must be redone under the new head. This is
	// the staleness source (#128 §21–§22).
	ActionReprocess Action = "reprocess"
)

// RequiredAction maps an effective severity to the action it mandates:
//
//	patch → none, minor → revalidate, major → reprocess.
//
// The severity that drives this is the DECLARED one (see classify.go): the
// author may escalate, never understate.
func RequiredAction(s Severity) (Action, error) {
	switch s {
	case SeverityPatch:
		return ActionNone, nil
	case SeverityMinor:
		return ActionRevalidate, nil
	case SeverityMajor:
		return ActionReprocess, nil
	}
	return "", fmt.Errorf("required action: unknown severity %q", s)
}

// Requirement is the durable reprocessing obligation one supersession
// created. It stays pending until resolved with evidence — enqueued, not
// implied: a consumer asks PendingRequirements, does the work, and resolves.
type Requirement struct {
	// ID is deterministic: "req-<seq>" of the supersede event that
	// created it. No randomness in the engine.
	ID string `json:"id"`
	// SupersessionSeq is that supersede event's Seq — the join key back
	// into the log for Explain.
	SupersessionSeq int `json:"supersessionSeq"`
	// SupersededID / NewHeadID: the record that was replaced and the
	// record that replaced it.
	SupersededID string `json:"supersededId"`
	NewHeadID    string `json:"newHeadId"`
	// Severity is the effective (declared) severity that mandated Action.
	Severity Severity `json:"severity"`
	Action   Action   `json:"action"`
	// CaptainVisible: the superseded record carried a captain acted-on
	// mark, so this requirement must surface to the captain and only the
	// captain may resolve it (VISION.md:21).
	CaptainVisible bool `json:"captainVisible,omitempty"`
	// StalenessSource: Action is reprocess — this requirement is an input
	// signal for staleness propagation (task-4cfpv.14's seam).
	StalenessSource bool `json:"stalenessSource,omitempty"`

	// Resolution state. Zero Resolved means pending.
	Resolved   bool   `json:"resolved,omitempty"`
	ResolvedBy string `json:"resolvedBy,omitempty"`
	// ResolutionNote is the evidence: what was done to discharge the
	// requirement ("reran GWT suite green", "captain reviewed diff").
	ResolutionNote string `json:"resolutionNote,omitempty"`
	ResolvedAt     string `json:"resolvedAt,omitempty"`
}

// requirementFor derives the requirement a supersede at log position seq
// mandates, or nil when none is owed. The captain-authority rule lives
// here: severity maps to an action, but a captain acted-on mark on the
// superseded record both forces visibility and upgrades an emit-nothing
// patch to an explicit notice — a supersession must never silently rewrite
// something the captain already acted on.
func (l *Ledger) requirementFor(seq int, severity Severity, newHeadID, supersededID string) (*Requirement, error) {
	action, err := RequiredAction(severity)
	if err != nil {
		return nil, err
	}
	captainActed := false
	for _, m := range l.actedOn[supersededID] {
		if m.Actor == ActorCaptain {
			captainActed = true
			break
		}
	}
	if action == ActionNone {
		if !captainActed {
			return nil, nil
		}
		action = ActionNotice
	}
	return &Requirement{
		ID:              fmt.Sprintf("req-%d", seq),
		SupersessionSeq: seq,
		SupersededID:    supersededID,
		NewHeadID:       newHeadID,
		Severity:        severity,
		Action:          action,
		CaptainVisible:  captainActed,
		StalenessSource: action == ActionReprocess,
	}, nil
}

// Requirement returns the requirement with the given id.
func (l *Ledger) Requirement(id string) (Requirement, bool) {
	r, ok := l.requirements[id]
	if !ok {
		return Requirement{}, false
	}
	return *r, true
}

// PendingRequirements returns every unresolved requirement, oldest first.
// This is the queue: reprocessing work is surfaced by asking, consumed by
// resolving.
func (l *Ledger) PendingRequirements() []Requirement {
	var out []Requirement
	for _, id := range l.requirementOrder {
		if r := l.requirements[id]; !r.Resolved {
			out = append(out, *r)
		}
	}
	return out
}

// RequirementsFor returns every requirement created by superseding the
// given record, oldest first (normally at most one — chains are linear).
func (l *Ledger) RequirementsFor(supersededID string) []Requirement {
	var out []Requirement
	for _, id := range l.requirementOrder {
		if r := l.requirements[id]; r.SupersededID == supersededID {
			out = append(out, *r)
		}
	}
	return out
}

// Resolution discharges a requirement with evidence.
type Resolution struct {
	Actor string
	// Note is the evidence — required, so "what did this trigger and how
	// was it satisfied" is always answerable.
	Note string
	At   string
}

// ResolveRequirement discharges a pending requirement. A captain-visible
// requirement may only be resolved by the captain — that is the whole point
// of the mark; anyone else discharging it would be the silent rewrite the
// rule exists to prevent.
func (l *Ledger) ResolveRequirement(id string, res Resolution) (Event, error) {
	r, ok := l.requirements[id]
	if !ok {
		return Event{}, fmt.Errorf("resolve %s: requirement does not exist", id)
	}
	if r.Resolved {
		return Event{}, fmt.Errorf("resolve %s: already resolved by %s", id, r.ResolvedBy)
	}
	if res.Actor == "" {
		return Event{}, fmt.Errorf("resolve %s: actor must not be empty", id)
	}
	if res.Note == "" {
		return Event{}, fmt.Errorf("resolve %s: note must not be empty — a resolution must carry evidence", id)
	}
	if r.CaptainVisible && res.Actor != ActorCaptain {
		return Event{}, fmt.Errorf("resolve %s: requirement is captain-visible; only %q may resolve it (got %q)", id, ActorCaptain, res.Actor)
	}
	r.Resolved = true
	r.ResolvedBy = res.Actor
	r.ResolutionNote = res.Note
	r.ResolvedAt = res.At
	return l.append(Event{Kind: EventRequirementResolved, RequirementID: id, Actor: res.Actor, Note: res.Note, At: res.At}), nil
}
