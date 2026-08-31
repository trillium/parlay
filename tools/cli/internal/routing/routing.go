// Package routing is the deterministic-first router with confidence and
// progressive hardening from issue #128 (§34–§37, §81, §89–§92), the
// representation-plane feature the Q16 plane split keeps on parlay's side
// (docs/gascity-plane-boundary.md §2.7). Full model: docs/routing.md.
//
// The engine is deliberately pure: it evaluates an input against a ruleset
// and a policy and returns a Result with a basis, a confidence, an outcome,
// and a human-readable trace. It never calls a model — parlay has no
// inherent inference (#128 §81). When nothing deterministic decides, the
// outcome is OutcomeNeedsInference and whatever workflow owns the input may
// run inference and report the result back through ClassifyInference.
//
// Two invariants guard the captain-authority boundary (VISION.md:21, "An
// agent may speak; only the captain decides what happens next"):
//
//  1. Only captain-authority feedback enters the confidence formula — see
//     Evidence.Confidence. Agent events are recorded but never counted.
//  2. A route decision only ever picks a delivery target. It carries no
//     side effects and grants the target nothing it did not already have.
package routing

import "fmt"

// Basis names where a routing answer came from.
type Basis string

const (
	// BasisRule: an authored deterministic rule (or the built-in
	// explicit-address rule) decided. Confidence 1.0 by construction.
	BasisRule Basis = "rule"
	// BasisHardened: a learned entry, promoted by accumulated captain
	// confirmations, decided or proposed.
	BasisHardened Basis = "hardened"
	// BasisInference: a recorded external inference proposal decided.
	BasisInference Basis = "inference"
	// BasisNone: nothing decided — the input needs inference.
	BasisNone Basis = "none"
)

// Outcome is what the confidence policy says to do with a decision.
type Outcome string

const (
	// OutcomeAct: route silently.
	OutcomeAct Outcome = "act"
	// OutcomeConfirm: propose the target; require confirmation before acting.
	OutcomeConfirm Outcome = "confirm"
	// OutcomeRefuse: do not route.
	OutcomeRefuse Outcome = "refuse"
	// OutcomeNeedsInference: no deterministic answer; an inference workflow
	// may propose one.
	OutcomeNeedsInference Outcome = "needs-inference"
)

// Policy holds the thresholds that turn a confidence into an Outcome.
// Defaults are the proposed #128 gap-fill recorded in docs/routing.md:
// three clean captain confirmations harden a route under Beta(1,1).
type Policy struct {
	// ActThreshold: confidence at or above this routes silently.
	ActThreshold float64 `json:"actThreshold"`
	// RefuseThreshold: confidence below this is refused; between the two
	// thresholds the route is proposed and must be confirmed.
	RefuseThreshold float64 `json:"refuseThreshold"`
}

// DefaultPolicy returns the documented default thresholds.
func DefaultPolicy() Policy {
	return Policy{ActThreshold: 0.80, RefuseThreshold: 0.50}
}

// Validate rejects a policy whose thresholds cannot classify every
// confidence into exactly one outcome. A corrupt policy must fail loudly:
// silently falling back to defaults would change act/refuse behavior with
// nothing telling the operator.
func (p Policy) Validate() error {
	if p.RefuseThreshold < 0 || p.ActThreshold > 1 || p.RefuseThreshold > p.ActThreshold {
		return fmt.Errorf("routing policy invalid: need 0 <= refuseThreshold (%v) <= actThreshold (%v) <= 1",
			p.RefuseThreshold, p.ActThreshold)
	}
	return nil
}

// Outcome classifies a confidence under this policy.
func (p Policy) Outcome(confidence float64) Outcome {
	switch {
	case confidence >= p.ActThreshold:
		return OutcomeAct
	case confidence >= p.RefuseThreshold:
		return OutcomeConfirm
	default:
		return OutcomeRefuse
	}
}

// Rule is one authored deterministic entry: normalized key → target.
// A rule matches when its key is a word-boundary prefix of the normalized
// input (prefix-only on purpose — the routing-key position is the front of
// the message, #128 §34; contains-anywhere would false-positive on inputs
// that merely mention a project).
type Rule struct {
	ID     string `json:"id"`
	Key    string `json:"key"` // stored normalized (NormalizeKey)
	Target string `json:"target"`
	// Retired: tombstoned. Never matches again, kept for history (#128 §79).
	Retired bool `json:"retired,omitempty"`
	// Note: free-form operator annotation (why this rule exists / was retired).
	Note string `json:"note,omitempty"`
}

// Evidence is one learned (signal → target) entry — the substrate hardened
// rules are made of. There is no separate "hardened" flag: an entry is
// hardened exactly while Confidence() sits at or above the act threshold,
// so hardening and un-hardening are both just arithmetic over recorded
// captain feedback (#128 §35, §90).
type Evidence struct {
	// ID addresses this entry for `parlay route rule retire` — learned
	// entries need tombstoning exactly like authored rules do.
	ID     string `json:"id,omitempty"`
	Signal string `json:"signal"`
	Target string `json:"target"`
	// Confirms / Corrections: captain-authority events only.
	Confirms    int `json:"confirms"`
	Corrections int `json:"corrections"`
	// AgentEvents counts agent-authority confirm/correct events. Recorded
	// for observability; NEVER counted in Confidence — an agent must not be
	// able to harden a route by confirming its own guesses (VISION.md:21).
	AgentEvents int `json:"agentEvents,omitempty"`
	// Provenance: ids of the decisions whose feedback built this entry.
	Provenance []string `json:"provenance,omitempty"`
	// Retired: tombstoned by `parlay route rule retire`. Never matches
	// again, kept for history (#128 §79).
	Retired bool   `json:"retired,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Confidence is the Beta(1,1) posterior mean over captain feedback:
// (confirms+1)/(confirms+corrections+2) — the mathematically grounded
// estimate #128 §36 asks for: how often has this signal actually meant
// this target, under a uniform prior. With the default 0.80 act threshold,
// three clean confirmations harden ((3+1)/(3+0+2) = 0.8) and a single
// correction against three confirmations demotes ((3+1)/(3+1+2) ≈ 0.67).
func (e Evidence) Confidence() float64 {
	return float64(e.Confirms+1) / float64(e.Confirms+e.Corrections+2)
}

// Ruleset is everything the deterministic layer consults, in evaluation
// order: the caller-supplied target roster (explicit address), authored
// rules, then learned evidence.
type Ruleset struct {
	Rules   []Rule     `json:"rules"`
	Learned []Evidence `json:"learned"`
}

// Candidate is one possible target surfaced by an evaluation — used when
// the outcome is confirm and the caller must show what is on offer.
type Candidate struct {
	Target     string  `json:"target"`
	Confidence float64 `json:"confidence"`
	Basis      Basis   `json:"basis"`
	// Source: the rule id or evidence signal that produced this candidate.
	Source string `json:"source"`
}

// Result is one routing evaluation: the answer plus everything needed to
// explain it (#128's observability requirement — it must be possible to ask
// why a message routed the way it did, and whether the answer came from a
// rule or an inference).
type Result struct {
	Basis      Basis   `json:"basis"`
	Target     string  `json:"target,omitempty"`
	Confidence float64 `json:"confidence"`
	Outcome    Outcome `json:"outcome"`
	// Source: the rule id ("explicit-address" for the built-in, the rule id
	// for authored, the signal for learned) that decided. Empty for
	// BasisNone.
	Source string `json:"source,omitempty"`
	// Signal: the lead signal extracted from the input — the key any
	// hardening of this decision would accrue to.
	Signal string `json:"signal"`
	// Candidates: every target on offer when the outcome is confirm.
	Candidates []Candidate `json:"candidates,omitempty"`
	// Trace: human-readable evaluation steps, in order.
	Trace []string `json:"trace"`
}
