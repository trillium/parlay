package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/routing"
)

func testRouteStore(t *testing.T) *routing.Store {
	t.Helper()
	return routing.NewStore(t.TempDir())
}

const testNow = "2026-08-30T12:00:00Z"

func TestRouteDecideAuthoredRuleActs(t *testing.T) {
	st := testRouteStore(t)
	if err := st.SaveRuleset(routing.Ruleset{
		Rules: []routing.Rule{{ID: "r1", Key: "parlay", Target: "parlay-dev"}},
	}); err != nil {
		t.Fatal(err)
	}
	ev, err := routeDecideRun(st, "parlay auth is broken", nil, testNow)
	if err != nil {
		t.Fatalf("routeDecideRun: %v", err)
	}
	if ev.Result.Outcome != routing.OutcomeAct || ev.Result.Target != "parlay-dev" {
		t.Fatalf("expected act→parlay-dev, got %+v", ev.Result)
	}
	if routeExitFor(ev.Result.Outcome) != 0 {
		t.Fatal("act must exit 0")
	}
	// The decision must be on the ledger, trace included.
	got, ok, err := st.FindEvent(ev.ID)
	if err != nil || !ok {
		t.Fatalf("decision not recorded: ok=%v err=%v", ok, err)
	}
	if got.Input != "parlay auth is broken" || got.Result == nil || len(got.Result.Trace) == 0 {
		t.Fatalf("ledger record incomplete: %+v", got)
	}
}

func TestRouteDecideRosterTargets(t *testing.T) {
	st := testRouteStore(t)
	ev, err := routeDecideRun(st, "dave, check the thing", []string{"dave", "mayor"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Result.Outcome != routing.OutcomeAct || ev.Result.Target != "dave" {
		t.Fatalf("expected explicit-address act→dave, got %+v", ev.Result)
	}
}

// The required end-to-end: an unmatched input needs inference (exit 5), and
// a low-confidence proposal for it is REFUSED (exit 4) — the router asks for
// help, gets a weak answer, and declines to act on it.
func TestRouteLowConfidenceProposalIsRefused(t *testing.T) {
	st := testRouteStore(t)
	dec, err := routeDecideRun(st, "some totally unkeyed rambling", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result.Outcome != routing.OutcomeNeedsInference {
		t.Fatalf("expected needs-inference, got %+v", dec.Result)
	}
	if routeExitFor(dec.Result.Outcome) != routeExitNeedsInference {
		t.Fatal("needs-inference must exit 5")
	}
	prop, err := routeProposeRun(st, dec.ID, "mayor", 0.30, testNow)
	if err != nil {
		t.Fatalf("routeProposeRun: %v", err)
	}
	if prop.Result.Outcome != routing.OutcomeRefuse {
		t.Fatalf("confidence 0.30 must be refused under default policy, got %+v", prop.Result)
	}
	if routeExitFor(prop.Result.Outcome) != routeExitRefuse {
		t.Fatal("refuse must exit 4")
	}
	if prop.Kind != routing.EventProposal || prop.Decision != dec.ID {
		t.Fatalf("proposal must reference its decision: %+v", prop)
	}
}

func TestRouteProposeConfidenceBands(t *testing.T) {
	st := testRouteStore(t)
	cases := []struct {
		conf float64
		want routing.Outcome
		exit int
	}{
		{0.95, routing.OutcomeAct, 0},
		{0.65, routing.OutcomeConfirm, routeExitConfirm},
		{0.10, routing.OutcomeRefuse, routeExitRefuse},
	}
	for _, c := range cases {
		dec, err := routeDecideRun(st, "unkeyed mystery input", nil, testNow)
		if err != nil {
			t.Fatal(err)
		}
		prop, err := routeProposeRun(st, dec.ID, "mayor", c.conf, testNow)
		if err != nil {
			t.Fatal(err)
		}
		if prop.Result.Outcome != c.want || routeExitFor(prop.Result.Outcome) != c.exit {
			t.Fatalf("confidence %.2f: got %s (exit %d), want %s (exit %d)",
				c.conf, prop.Result.Outcome, routeExitFor(prop.Result.Outcome), c.want, c.exit)
		}
		if prop.Result.Basis != routing.BasisInference {
			t.Fatalf("a proposal's basis must be inference, got %s", prop.Result.Basis)
		}
	}
}

func TestRouteProposeRefusesDecidedDecision(t *testing.T) {
	st := testRouteStore(t)
	if err := st.SaveRuleset(routing.Ruleset{
		Rules: []routing.Rule{{ID: "r1", Key: "parlay", Target: "parlay-dev"}},
	}); err != nil {
		t.Fatal(err)
	}
	dec, err := routeDecideRun(st, "parlay is fine", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result.Outcome != routing.OutcomeAct {
		t.Fatalf("precondition: expected act, got %+v", dec.Result)
	}
	if _, err := routeProposeRun(st, dec.ID, "mayor", 0.99, testNow); err == nil {
		t.Fatal("a proposal must never override a deterministic answer")
	}
}

func TestRouteProposeUnknownDecision(t *testing.T) {
	st := testRouteStore(t)
	if _, err := routeProposeRun(st, "rt-deadbeef", "mayor", 0.9, testNow); err == nil {
		t.Fatal("proposing against an unknown decision id must error")
	}
}

func TestRouteProposeRefusesNonDecisionEvent(t *testing.T) {
	st := testRouteStore(t)
	dec, err := routeDecideRun(st, "unkeyed mystery input", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	prop, err := routeProposeRun(st, dec.ID, "mayor", 0.65, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routeProposeRun(st, prop.ID, "mayor", 0.9, testNow); err == nil {
		t.Fatal("a proposal id is not a decision id — chaining proposals must error")
	}
}

func TestRouteDecideCorruptPolicyIsLoud(t *testing.T) {
	st := testRouteStore(t)
	// An inverted policy on disk must stop decide, not silently default.
	if err := st.SaveRuleset(routing.Ruleset{}); err != nil {
		t.Fatal(err)
	}
	writeBadPolicy(t, st)
	if _, err := routeDecideRun(st, "anything", nil, testNow); err == nil {
		t.Fatal("decide must fail loudly on a corrupt policy")
	}
}

func TestRouteExitForUnknownOutcomeIsRuntime(t *testing.T) {
	if routeExitFor(routing.Outcome("gibberish")) != 1 {
		t.Fatal("an unknown outcome must exit 1, never 0")
	}
}

func TestSplitTargets(t *testing.T) {
	got := splitTargets(" dave, mayor ,,scribe ")
	if len(got) != 3 || got[0] != "dave" || got[1] != "mayor" || got[2] != "scribe" {
		t.Fatalf("splitTargets mangled the list: %v", got)
	}
	if splitTargets("") != nil {
		t.Fatal("empty --targets must yield an empty roster")
	}
}

func TestRenderRouteEventNeedsInferenceNamesFollowUp(t *testing.T) {
	res := routing.Result{Basis: routing.BasisNone, Outcome: routing.OutcomeNeedsInference, Signal: "x"}
	ev := routing.Event{ID: "rt-0a0a0a0a", Kind: routing.EventDecision, Time: testNow, Result: &res}
	out := renderRouteEvent(ev)
	if !strings.Contains(out, "route propose --decision rt-0a0a0a0a") {
		t.Fatalf("needs-inference output must name the exact follow-up command, got:\n%s", out)
	}
}

func TestRenderRouteResultListsCandidates(t *testing.T) {
	res := routing.Result{
		Basis: routing.BasisRule, Outcome: routing.OutcomeConfirm, Target: "a",
		Confidence: 1.0, Source: "explicit-address", Signal: "dup",
		Candidates: []routing.Candidate{
			{Target: "a", Confidence: 1.0, Basis: routing.BasisRule, Source: "explicit-address"},
			{Target: "b", Confidence: 1.0, Basis: routing.BasisRule, Source: "explicit-address"},
		},
	}
	out := renderRouteResult(res)
	if !strings.Contains(out, "candidate: a") || !strings.Contains(out, "candidate: b") {
		t.Fatalf("confirm output must list every candidate, got:\n%s", out)
	}
}

// writeBadPolicy plants an invalid policy file directly (SavePolicy refuses
// to write one, which is itself under test elsewhere).
func writeBadPolicy(t *testing.T, st *routing.Store) {
	t.Helper()
	bad := `{"actThreshold": 0.2, "refuseThreshold": 0.9}`
	if err := os.WriteFile(filepath.Join(st.Dir(), "policy.json"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
}
