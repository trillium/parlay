// Feedback application — how confirmations and corrections become (and
// unmake) hardened routes (#128 §35, §90; docs/routing.md "Hardening" /
// "Un-hardening").
//
// The captain-authority invariant lives here: only AuthorityCaptain events
// touch Confirms/Corrections — the numbers Evidence.Confidence is computed
// from. AuthorityAgent events increment AgentEvents only, so an agent
// confirming its own routing guesses moves nothing but an observability
// counter (VISION.md:21 — an agent may speak; only the captain decides).
package routing

import "fmt"

// Authority names who issued a piece of feedback.
type Authority string

const (
	AuthorityCaptain Authority = "captain"
	AuthorityAgent   Authority = "agent"
)

// ParseAuthority validates a --authority value. Empty defaults to agent:
// unlabeled feedback must fail TOWARD not-hardening, never toward it.
func ParseAuthority(s string) (Authority, error) {
	switch Authority(s) {
	case AuthorityCaptain, AuthorityAgent:
		return Authority(s), nil
	case "":
		return AuthorityAgent, nil
	default:
		return "", fmt.Errorf("authority %q is not captain or agent", s)
	}
}

// findLiveEvidence returns the index of the live (non-retired) entry for
// (signal, target), or -1. Retired entries never accrue new feedback —
// re-teaching a retired pairing starts a fresh entry, preserving the
// tombstone's history (#128 §79).
func (rs *Ruleset) findLiveEvidence(signal, target string) int {
	for i, e := range rs.Learned {
		if !e.Retired && e.Signal == signal && e.Target == target {
			return i
		}
	}
	return -1
}

// ensureEvidence returns a pointer to the live entry for (signal, target),
// creating one if none exists.
func (rs *Ruleset) ensureEvidence(signal, target string) *Evidence {
	if i := rs.findLiveEvidence(signal, target); i >= 0 {
		return &rs.Learned[i]
	}
	rs.Learned = append(rs.Learned, Evidence{ID: NewEvidenceID(), Signal: signal, Target: target})
	return &rs.Learned[len(rs.Learned)-1]
}

// RecordConfirmation applies one confirmation of (signal → target) and
// returns the updated entry. provenance is the ledger id of the decision or
// proposal the feedback answered.
func (rs *Ruleset) RecordConfirmation(signal, target string, auth Authority, provenance string) *Evidence {
	e := rs.ensureEvidence(signal, target)
	if auth == AuthorityCaptain {
		e.Confirms++
	} else {
		e.AgentEvents++
	}
	e.Provenance = appendUniqueString(e.Provenance, provenance)
	return e
}

// RecordCorrection applies one correction: the route for signal should have
// been rightTarget, not wrongTarget. Two entries move:
//
//   - (signal → wrongTarget) takes a correction — automatic demotion. The
//     entry is created if absent: a recorded "this pairing was wrong" is
//     real Bayesian evidence even when nothing had been learned yet.
//   - (signal → rightTarget) takes a confirmation — a captain correction is
//     the strongest possible evidence for where the input actually belonged
//     (docs/routing.md gap-fill 4), so the signal re-hardens toward the
//     right target rather than merely un-hardening from the wrong one.
//
// wrongTarget may be "" (the decision had no target — a needs-inference
// decision being taught directly); then only the confirmation side applies
// and demoted is nil.
func (rs *Ruleset) RecordCorrection(signal, wrongTarget, rightTarget string, auth Authority, provenance string) (demoted, taught *Evidence) {
	if wrongTarget != "" {
		demoted = rs.ensureEvidence(signal, wrongTarget)
		if auth == AuthorityCaptain {
			demoted.Corrections++
		} else {
			demoted.AgentEvents++
		}
		demoted.Provenance = appendUniqueString(demoted.Provenance, provenance)
	}
	taught = rs.RecordConfirmation(signal, rightTarget, auth, provenance)
	return demoted, taught
}

// RetireEntry tombstones the authored rule or learned entry with the given
// id. The entry never matches again but keeps its history and provenance.
// Returns what was retired ("rule" or "evidence") and its description.
func (rs *Ruleset) RetireEntry(id, note string) (kind, desc string, err error) {
	for i := range rs.Rules {
		if rs.Rules[i].ID != id {
			continue
		}
		if rs.Rules[i].Retired {
			return "", "", fmt.Errorf("rule %s is already retired", id)
		}
		rs.Rules[i].Retired = true
		if note != "" {
			rs.Rules[i].Note = note
		}
		return "rule", fmt.Sprintf("%q → %s", rs.Rules[i].Key, rs.Rules[i].Target), nil
	}
	for i := range rs.Learned {
		if rs.Learned[i].ID != id {
			continue
		}
		if rs.Learned[i].Retired {
			return "", "", fmt.Errorf("evidence %s is already retired", id)
		}
		rs.Learned[i].Retired = true
		if note != "" {
			rs.Learned[i].Note = note
		}
		return "evidence", fmt.Sprintf("%q → %s", rs.Learned[i].Signal, rs.Learned[i].Target), nil
	}
	return "", "", fmt.Errorf("no rule or learned entry %q (ids come from 'route rules')", id)
}

// AddRule appends an authored rule. The key is stored normalized; an empty
// normalized key or a duplicate id is rejected.
func (rs *Ruleset) AddRule(id, key, target, note string) (*Rule, error) {
	norm := NormalizeKey(key)
	if norm == "" {
		return nil, fmt.Errorf("key %q normalizes to nothing — an empty key would match every input", key)
	}
	if target == "" {
		return nil, fmt.Errorf("rule needs a target")
	}
	if id == "" {
		id = NewRuleID()
	}
	for _, r := range rs.Rules {
		if r.ID == id {
			return nil, fmt.Errorf("rule id %q already exists", id)
		}
	}
	rs.Rules = append(rs.Rules, Rule{ID: id, Key: norm, Target: target, Note: note})
	return &rs.Rules[len(rs.Rules)-1], nil
}

// NewEvidenceID / NewRuleID mint ids for learned entries and authored rules
// ("ev-"/"rl-" + 8 hex chars, same generator as ledger event ids).
func NewEvidenceID() string { return "ev-" + NewEventID()[len("rt-"):] }
func NewRuleID() string     { return "rl-" + NewEventID()[len("rt-"):] }

func appendUniqueString(list []string, s string) []string {
	if s == "" {
		return list
	}
	for _, have := range list {
		if have == s {
			return list
		}
	}
	return append(list, s)
}
