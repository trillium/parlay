package routing

import (
	"math"
	"strings"
	"testing"
)

func TestNormalizeKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Parlay, authentication needs to be edited", "parlay authentication needs to be edited"},
		{"  HELLO   World ", "hello world"},
		{"dave-check_this!", "dave check this"},
		{"", ""},
		{"?!.,", ""},
		{"Émile règle", "émile règle"},
	}
	for _, c := range cases {
		if got := NormalizeKey(c.in); got != c.want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLeadSignal(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Parlay, authentication needs to be edited", "parlay"},
		{"Dave check the thing we were discussing", "dave"},
		{"hey dave: look at this", "hey dave"},
		// A long left segment is a clause, not an address — fall back to
		// the first token.
		{"after we talked about it all yesterday, ship it", "after"},
		{"", ""},
		{"...", ""},
		{"task: research breakfast recipes", "task"},
	}
	for _, c := range cases {
		if got := LeadSignal(c.in); got != c.want {
			t.Errorf("LeadSignal(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHasWordBoundaryPrefix(t *testing.T) {
	cases := []struct {
		norm, key string
		want      bool
	}{
		{"parlay auth is broken", "parlay auth", true},
		{"parlay auth", "parlay auth", true},
		{"parlay authentication is broken", "parlay auth", false},
		{"parlay auth is broken", "", false},
		{"other parlay auth", "parlay auth", false},
	}
	for _, c := range cases {
		if got := hasWordBoundaryPrefix(c.norm, c.key); got != c.want {
			t.Errorf("hasWordBoundaryPrefix(%q, %q) = %v, want %v", c.norm, c.key, got, c.want)
		}
	}
}

func TestEvidenceConfidenceBetaPosterior(t *testing.T) {
	cases := []struct {
		confirms, corrections int
		want                  float64
	}{
		{0, 0, 0.5},        // no evidence: uniform prior mean
		{3, 0, 0.8},        // three clean confirmations hit the act default
		{3, 1, 4.0 / 6.0},  // one correction demotes below act
		{0, 1, 1.0 / 3.0},  // a lone correction lands below refuse
		{15, 0, 16.0 / 17}, // approaches 1, never reaches it
	}
	for _, c := range cases {
		e := Evidence{Confirms: c.confirms, Corrections: c.corrections}
		if got := e.Confidence(); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Confidence(%d confirms, %d corrections) = %v, want %v", c.confirms, c.corrections, got, c.want)
		}
	}
}

func TestAgentEventsNeverEnterConfidence(t *testing.T) {
	// The captain-authority boundary (VISION.md:21): agent events are
	// recorded but must not move the confidence at all.
	clean := Evidence{Confirms: 3}
	noisy := Evidence{Confirms: 3, AgentEvents: 500}
	if clean.Confidence() != noisy.Confidence() {
		t.Fatalf("agent events changed confidence: %v vs %v", clean.Confidence(), noisy.Confidence())
	}
}

func TestPolicyOutcomeBands(t *testing.T) {
	pol := DefaultPolicy()
	cases := []struct {
		c    float64
		want Outcome
	}{
		{1.0, OutcomeAct},
		{0.80, OutcomeAct},
		{0.799, OutcomeConfirm},
		{0.50, OutcomeConfirm},
		{0.499, OutcomeRefuse},
		{0.0, OutcomeRefuse},
	}
	for _, c := range cases {
		if got := pol.Outcome(c.c); got != c.want {
			t.Errorf("Outcome(%v) = %v, want %v", c.c, got, c.want)
		}
	}
}

func TestPolicyValidate(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatalf("default policy invalid: %v", err)
	}
	bad := []Policy{
		{ActThreshold: 0.4, RefuseThreshold: 0.6}, // inverted
		{ActThreshold: 1.2, RefuseThreshold: 0.5}, // act > 1
		{ActThreshold: 0.8, RefuseThreshold: -0.1},
	}
	for _, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", p)
		}
	}
}

func TestEvaluateExplicitAddressHit(t *testing.T) {
	res := Evaluate("Dave, check the thing we were discussing", []string{"amy", "dave"}, Ruleset{}, DefaultPolicy())
	if res.Basis != BasisRule || res.Target != "dave" || res.Outcome != OutcomeAct || res.Confidence != 1.0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Source != ExplicitAddressSource {
		t.Errorf("Source = %q, want %q", res.Source, ExplicitAddressSource)
	}
}

func TestEvaluateExplicitAddressAmbiguityConfirms(t *testing.T) {
	// Two roster entries normalizing to the same signal must confirm, not
	// race on list order.
	res := Evaluate("Dave: hello", []string{"DAVE", "dave"}, Ruleset{}, DefaultPolicy())
	if res.Outcome != OutcomeConfirm {
		t.Fatalf("Outcome = %v, want confirm; result %+v", res.Outcome, res)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("Candidates = %+v, want 2", res.Candidates)
	}
}

func TestEvaluateAuthoredRuleHitAndLongestKeyWins(t *testing.T) {
	rs := Ruleset{Rules: []Rule{
		{ID: "r1", Key: "parlay", Target: "parlay-dev"},
		{ID: "r2", Key: "parlay auth", Target: "auth-dev"},
	}}
	res := Evaluate("Parlay auth is broken again", nil, rs, DefaultPolicy())
	if res.Basis != BasisRule || res.Target != "auth-dev" || res.Outcome != OutcomeAct {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Source != "r2" {
		t.Errorf("Source = %q, want r2 (longest key)", res.Source)
	}
	// The shorter rule still decides inputs the longer one does not match.
	res = Evaluate("Parlay, the panel is stuck", nil, rs, DefaultPolicy())
	if res.Target != "parlay-dev" || res.Outcome != OutcomeAct {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestEvaluateAuthoredRuleConflictConfirms(t *testing.T) {
	rs := Ruleset{Rules: []Rule{
		{ID: "r1", Key: "parlay", Target: "a"},
		{ID: "r2", Key: "parlay", Target: "b"},
	}}
	res := Evaluate("parlay hello", nil, rs, DefaultPolicy())
	if res.Outcome != OutcomeConfirm || len(res.Candidates) != 2 {
		t.Fatalf("equal-length conflicting rules must confirm: %+v", res)
	}
}

func TestEvaluateRetiredRuleNeverMatches(t *testing.T) {
	rs := Ruleset{Rules: []Rule{{ID: "r1", Key: "parlay", Target: "a", Retired: true}}}
	res := Evaluate("parlay hello", nil, rs, DefaultPolicy())
	if res.Outcome != OutcomeNeedsInference {
		t.Fatalf("retired rule matched: %+v", res)
	}
}

func TestEvaluateHardenedEntryActs(t *testing.T) {
	rs := Ruleset{Learned: []Evidence{{Signal: "parlay", Target: "parlay-dev", Confirms: 3}}}
	res := Evaluate("Parlay, authentication needs to be edited", nil, rs, DefaultPolicy())
	if res.Basis != BasisHardened || res.Target != "parlay-dev" || res.Outcome != OutcomeAct {
		t.Fatalf("unexpected result: %+v", res)
	}
	if math.Abs(res.Confidence-0.8) > 1e-9 {
		t.Errorf("Confidence = %v, want 0.8", res.Confidence)
	}
}

func TestEvaluateDualHardenedSignalConfirms(t *testing.T) {
	// Two entries both at/above act for the same signal: the engine must
	// not pick a winner (Gas City's refusal posture).
	rs := Ruleset{Learned: []Evidence{
		{Signal: "parlay", Target: "a", Confirms: 10},
		{Signal: "parlay", Target: "b", Confirms: 10},
	}}
	res := Evaluate("parlay hello", nil, rs, DefaultPolicy())
	if res.Outcome != OutcomeConfirm || len(res.Candidates) != 2 {
		t.Fatalf("dual-hardened signal must confirm: %+v", res)
	}
}

func TestEvaluateConfirmBandProposes(t *testing.T) {
	// 1 confirm, 0 corrections → (1+1)/(1+2) ≈ 0.667: confirm band.
	rs := Ruleset{Learned: []Evidence{{Signal: "parlay", Target: "a", Confirms: 1}}}
	res := Evaluate("parlay hello", nil, rs, DefaultPolicy())
	if res.Outcome != OutcomeConfirm || res.Target != "a" || res.Basis != BasisHardened {
		t.Fatalf("confirm-band entry must propose: %+v", res)
	}
}

func TestEvaluateDemotedEntryFallsThrough(t *testing.T) {
	// 0 confirms, 2 corrections → 1/4 = 0.25: below refuse, no proposal.
	rs := Ruleset{Learned: []Evidence{{Signal: "parlay", Target: "a", Corrections: 2}}}
	res := Evaluate("parlay hello", nil, rs, DefaultPolicy())
	if res.Outcome != OutcomeNeedsInference {
		t.Fatalf("dead entry must fall through to inference: %+v", res)
	}
}

func TestEvaluateEmptySignalNeverMatchesEvidence(t *testing.T) {
	rs := Ruleset{Learned: []Evidence{{Signal: "", Target: "a", Confirms: 50}}}
	res := Evaluate("?!.,", nil, rs, DefaultPolicy())
	if res.Outcome != OutcomeNeedsInference {
		t.Fatalf("empty signal must stay on the inference path: %+v", res)
	}
}

func TestEvaluateNoMatchNeedsInference(t *testing.T) {
	res := Evaluate("it's still broken", []string{"dave"}, Ruleset{}, DefaultPolicy())
	if res.Basis != BasisNone || res.Outcome != OutcomeNeedsInference {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Trace) == 0 || !strings.Contains(strings.Join(res.Trace, "\n"), "needs-inference") {
		t.Errorf("trace missing needs-inference step: %v", res.Trace)
	}
}

func TestEvaluateIsOrderIndependent(t *testing.T) {
	rules := []Rule{
		{ID: "r2", Key: "parlay auth", Target: "auth-dev"},
		{ID: "r1", Key: "parlay", Target: "parlay-dev"},
	}
	learned := []Evidence{
		{Signal: "gas", Target: "b", Confirms: 4},
		{Signal: "gas", Target: "a", Confirms: 4},
	}
	a := Evaluate("gas leak in the kitchen", nil, Ruleset{Rules: rules, Learned: learned}, DefaultPolicy())
	// Reverse both slices; the answer must not change.
	rules[0], rules[1] = rules[1], rules[0]
	learned[0], learned[1] = learned[1], learned[0]
	b := Evaluate("gas leak in the kitchen", nil, Ruleset{Rules: rules, Learned: learned}, DefaultPolicy())
	if a.Outcome != b.Outcome || a.Target != b.Target || len(a.Candidates) != len(b.Candidates) {
		t.Fatalf("evaluation depends on slice order: %+v vs %+v", a, b)
	}
	for i := range a.Candidates {
		if a.Candidates[i] != b.Candidates[i] {
			t.Fatalf("candidate order depends on slice order: %+v vs %+v", a.Candidates, b.Candidates)
		}
	}
}

func TestClassifyInference(t *testing.T) {
	pol := DefaultPolicy()
	if res := ClassifyInference("dave", 0.9, "sig", pol); res.Outcome != OutcomeAct || res.Basis != BasisInference {
		t.Fatalf("high-confidence inference must act: %+v", res)
	}
	if res := ClassifyInference("dave", 0.6, "sig", pol); res.Outcome != OutcomeConfirm {
		t.Fatalf("mid-confidence inference must confirm: %+v", res)
	}
	if res := ClassifyInference("dave", 0.2, "sig", pol); res.Outcome != OutcomeRefuse {
		t.Fatalf("low-confidence inference must refuse: %+v", res)
	}
	// Confidence is clamped, and the clamped value drives the outcome.
	if res := ClassifyInference("dave", 1.7, "sig", pol); res.Confidence != 1.0 || res.Outcome != OutcomeAct {
		t.Fatalf("confidence not clamped high: %+v", res)
	}
	if res := ClassifyInference("dave", -3, "sig", pol); res.Confidence != 0 || res.Outcome != OutcomeRefuse {
		t.Fatalf("confidence not clamped low: %+v", res)
	}
}
