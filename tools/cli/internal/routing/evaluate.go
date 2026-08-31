// The deterministic evaluation order (#128 §34–§36, docs/routing.md):
// explicit address → authored rules → learned (hardened) entries → none.
// Where a lookup table can answer, no model is asked; inference is the
// escalation, never the default router.
package routing

import (
	"fmt"
	"sort"
	"strings"
)

// ExplicitAddressSource is the Result.Source of the built-in rule that
// matches a lead signal against the caller-supplied target roster.
const ExplicitAddressSource = "explicit-address"

// Evaluate runs one input through the deterministic layers and classifies
// the answer under pol. roster is the caller's known-target list (agent
// ids / channel names); the engine holds no roster of its own.
//
// Ambiguity never auto-acts: two candidates that both qualify at the same
// layer produce OutcomeConfirm with both listed, mirroring Gas City's
// mailbox posture that delivering to the wrong session is worse than not
// delivering (~/code/gascity/internal/session/address_directory.go:22-25).
func Evaluate(input string, roster []string, rs Ruleset, pol Policy) Result {
	norm := NormalizeKey(input)
	signal := LeadSignal(input)
	res := Result{Signal: signal}
	res.trace("input normalized to %q, lead signal %q", norm, signal)

	if done := evalExplicitAddress(&res, signal, roster); done {
		return res
	}
	if done := evalAuthoredRules(&res, norm, rs.Rules); done {
		return res
	}
	if done := evalLearned(&res, signal, rs.Learned, pol); done {
		return res
	}

	res.Basis = BasisNone
	res.Outcome = OutcomeNeedsInference
	res.Confidence = 0
	res.trace("no deterministic answer: needs-inference")
	return res
}

// ClassifyInference records how the policy treats an external inference
// proposal for a decision the deterministic layers could not make (#128
// §36: insufficient confidence → invoke inference → record the decision).
// The confidence is clamped to [0,1]; the outcome is purely the policy's.
func ClassifyInference(target string, confidence float64, signal string, pol Policy) Result {
	c := confidence
	if c < 0 {
		c = 0
	}
	if c > 1 {
		c = 1
	}
	res := Result{
		Basis:      BasisInference,
		Target:     target,
		Confidence: c,
		Outcome:    pol.Outcome(c),
		Source:     "inference",
		Signal:     signal,
	}
	res.trace("inference proposed %q at confidence %.3f", target, c)
	res.trace("policy: act ≥ %.2f, refuse < %.2f → %s", pol.ActThreshold, pol.RefuseThreshold, res.Outcome)
	return res
}

func (r *Result) trace(format string, args ...any) {
	r.Trace = append(r.Trace, fmt.Sprintf(format, args...))
}

// evalExplicitAddress is layer 1: the input's lead signal names a known
// target (#128 §34, "Dave, check the thing we were discussing"). Two
// roster entries normalizing to the same signal is an ambiguity, not a
// race won by list order.
func evalExplicitAddress(res *Result, signal string, roster []string) bool {
	if signal == "" {
		res.trace("explicit-address: empty signal, skipped")
		return false
	}
	var matched []string
	for _, target := range roster {
		if NormalizeKey(target) == signal {
			matched = append(matched, target)
		}
	}
	sort.Strings(matched)
	switch len(matched) {
	case 0:
		res.trace("explicit-address: signal %q names no roster target (%d checked)", signal, len(roster))
		return false
	case 1:
		res.Basis = BasisRule
		res.Target = matched[0]
		res.Confidence = 1.0
		res.Outcome = OutcomeAct
		res.Source = ExplicitAddressSource
		res.trace("explicit-address: signal %q names target %q — confidence 1.0, act", signal, matched[0])
		return true
	default:
		res.Basis = BasisRule
		res.Confidence = 1.0
		res.Outcome = OutcomeConfirm
		res.Source = ExplicitAddressSource
		for _, t := range matched {
			res.Candidates = append(res.Candidates, Candidate{Target: t, Confidence: 1.0, Basis: BasisRule, Source: ExplicitAddressSource})
		}
		res.Target = matched[0]
		res.trace("explicit-address: signal %q names %d targets (%s) — ambiguous, confirm", signal, len(matched), strings.Join(matched, ", "))
		return true
	}
}

// evalAuthoredRules is layer 2: operator-written key → target entries.
// Longest matching key wins; equal-length matches naming different targets
// are a conflict the engine refuses to resolve silently.
func evalAuthoredRules(res *Result, norm string, rules []Rule) bool {
	var matches []Rule
	live := 0
	for _, rule := range rules {
		if rule.Retired {
			continue
		}
		live++
		if hasWordBoundaryPrefix(norm, rule.Key) {
			matches = append(matches, rule)
		}
	}
	if len(matches) == 0 {
		res.trace("authored rules: 0 of %d live rules match", live)
		return false
	}
	// Deterministic total order: longest key first, then id.
	sort.Slice(matches, func(i, j int) bool {
		if len(matches[i].Key) != len(matches[j].Key) {
			return len(matches[i].Key) > len(matches[j].Key)
		}
		return matches[i].ID < matches[j].ID
	})
	best := matches[0]
	winners := []Rule{best}
	for _, m := range matches[1:] {
		if len(m.Key) == len(best.Key) {
			winners = append(winners, m)
		}
	}
	targets := map[string]bool{}
	for _, w := range winners {
		targets[w.Target] = true
	}
	if len(targets) == 1 {
		res.Basis = BasisRule
		res.Target = best.Target
		res.Confidence = 1.0
		res.Outcome = OutcomeAct
		res.Source = best.ID
		res.trace("authored rules: rule %s (key %q) matches → target %q — confidence 1.0, act", best.ID, best.Key, best.Target)
		return true
	}
	res.Basis = BasisRule
	res.Confidence = 1.0
	res.Outcome = OutcomeConfirm
	res.Source = best.ID
	for _, w := range winners {
		res.Candidates = append(res.Candidates, Candidate{Target: w.Target, Confidence: 1.0, Basis: BasisRule, Source: w.ID})
	}
	res.Target = winners[0].Target
	res.trace("authored rules: %d equal-length rules disagree on the target — conflict, confirm", len(winners))
	return true
}

// evalLearned is layer 3: evidence entries for this signal, hardened by
// captain feedback. An entry decides silently only while its Beta(1,1)
// confidence sits at or above the act threshold; two entries both at or
// above it are an ambiguity that must be confirmed, never raced.
func evalLearned(res *Result, signal string, learned []Evidence, pol Policy) bool {
	if signal == "" {
		res.trace("learned: empty signal accrues no evidence, skipped")
		return false
	}
	var entries []Evidence
	for _, e := range learned {
		if e.Retired || e.Signal != signal {
			continue
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		res.trace("learned: no live evidence for signal %q", signal)
		return false
	}
	// Deterministic order: confidence descending, then target.
	sort.Slice(entries, func(i, j int) bool {
		ci, cj := entries[i].Confidence(), entries[j].Confidence()
		if ci != cj {
			return ci > cj
		}
		return entries[i].Target < entries[j].Target
	})
	var hardened, proposable []Evidence
	for _, e := range entries {
		c := e.Confidence()
		res.trace("learned: %q → %q confidence %.3f (%d confirms, %d corrections)", signal, e.Target, c, e.Confirms, e.Corrections)
		switch {
		case c >= pol.ActThreshold:
			hardened = append(hardened, e)
		case c >= pol.RefuseThreshold:
			proposable = append(proposable, e)
		}
	}
	candidate := func(e Evidence) Candidate {
		return Candidate{Target: e.Target, Confidence: e.Confidence(), Basis: BasisHardened, Source: e.Signal}
	}
	switch {
	case len(hardened) == 1:
		e := hardened[0]
		res.Basis = BasisHardened
		res.Target = e.Target
		res.Confidence = e.Confidence()
		res.Outcome = OutcomeAct
		res.Source = e.Signal
		res.trace("learned: single hardened entry (confidence ≥ act %.2f) → target %q, act", pol.ActThreshold, e.Target)
		return true
	case len(hardened) > 1:
		res.Basis = BasisHardened
		res.Outcome = OutcomeConfirm
		res.Source = signal
		for _, e := range hardened {
			res.Candidates = append(res.Candidates, candidate(e))
		}
		res.Target = hardened[0].Target
		res.Confidence = hardened[0].Confidence()
		res.trace("learned: %d entries hardened for the same signal — ambiguous, confirm", len(hardened))
		return true
	case len(proposable) > 0:
		res.Basis = BasisHardened
		res.Outcome = OutcomeConfirm
		res.Source = signal
		for _, e := range proposable {
			res.Candidates = append(res.Candidates, candidate(e))
		}
		res.Target = proposable[0].Target
		res.Confidence = proposable[0].Confidence()
		res.trace("learned: best entry in confirm band (%.2f ≤ confidence < %.2f) → propose %q", pol.RefuseThreshold, pol.ActThreshold, res.Target)
		return true
	default:
		res.trace("learned: every entry below refuse threshold %.2f — no proposal", pol.RefuseThreshold)
		return false
	}
}
