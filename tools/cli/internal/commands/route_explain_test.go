package commands

import (
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/routing"
)

// The observability requirement (#128): ask why a message routed the way it
// did, and whether the answer came from a rule or an inference. explain must
// answer both from the ledger alone — the decision's recorded trace plus
// every feedback event that references it.

func TestRouteExplainReplaysRuleDecision(t *testing.T) {
	st := testRouteStore(t)
	rs := routing.Ruleset{}
	if _, err := rs.AddRule("rl-1", "parlay auth", "auth-dev", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRuleset(rs); err != nil {
		t.Fatal(err)
	}
	dec, err := routeDecideRun(st, "parlay auth: token refresh is broken", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	ev, related, err := routeExplainRun(st, dec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 0 {
		t.Fatalf("no feedback yet, but explain found %d related events", len(related))
	}
	out := renderRouteExplain(ev, related)
	if !strings.Contains(out, "answered by: a deterministic rule") || strings.Contains(out, "inference proposal") {
		t.Fatalf("a rule decision must say it was a rule, not an inference:\n%s", out)
	}
	if !strings.Contains(out, "trace:") || !strings.Contains(out, "rl-1") {
		t.Fatalf("explain must replay the recorded trace naming the matched rule:\n%s", out)
	}
	if !strings.Contains(out, `input: "parlay auth: token refresh is broken"`) {
		t.Fatalf("explain must show the routed input:\n%s", out)
	}
}

func TestRouteExplainCollectsFollowUps(t *testing.T) {
	st := testRouteStore(t)
	dec, err := routeDecideRun(st, "wharf: crane jammed again", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result.Outcome != routing.OutcomeNeedsInference {
		t.Fatalf("expected needs-inference, got %s", dec.Result.Outcome)
	}
	prop, err := routeProposeRun(st, dec.ID, "dockhand", 0.65, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := routeConfirmRun(st, prop.ID, routing.AuthorityCaptain, testNow); err != nil {
		t.Fatal(err)
	}

	// Explaining the decision shows the proposal that answered it.
	ev, related, err := routeExplainRun(st, dec.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := renderRouteExplain(ev, related)
	if !strings.Contains(out, "answered by: nothing") {
		t.Fatalf("a needs-inference decision had no deterministic answer; explain must say so:\n%s", out)
	}
	if !strings.Contains(out, prop.ID) || !strings.Contains(out, "dockhand") {
		t.Fatalf("explain must list the proposal recorded for this decision:\n%s", out)
	}

	// Explaining the proposal shows the confirm that followed it, with authority.
	pev, prelated, err := routeExplainRun(st, prop.ID)
	if err != nil {
		t.Fatal(err)
	}
	pout := renderRouteExplain(pev, prelated)
	if !strings.Contains(pout, "answered by: an external inference proposal") {
		t.Fatalf("a proposal must say it came from inference, not a rule:\n%s", pout)
	}
	if !strings.Contains(pout, "captain confirmed") {
		t.Fatalf("explain must show the captain confirm with its authority:\n%s", pout)
	}
	if !strings.Contains(pout, "for decision: "+dec.ID) {
		t.Fatalf("a proposal's explain must name the decision it answered:\n%s", pout)
	}
}

func TestRouteExplainUnknownIDIsLoud(t *testing.T) {
	st := testRouteStore(t)
	if _, _, err := routeExplainRun(st, "rt-deadbeef"); err == nil {
		t.Fatal("explaining an id not on the ledger must error, not fabricate history")
	}
}

func TestRouteRulesListsEverythingIncludingRetired(t *testing.T) {
	st := testRouteStore(t)
	rs := routing.Ruleset{}
	if _, err := rs.AddRule("rl-live", "parlay auth", "auth-dev", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.AddRule("rl-old", "old signal", "gone-agent", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rs.RetireEntry("rl-old", "superseded"); err != nil {
		t.Fatal(err)
	}
	ev := rs.RecordConfirmation("wharf", "dockhand", routing.AuthorityCaptain, "rt-11111111")
	rs.RecordConfirmation("wharf", "dockhand", routing.AuthorityCaptain, "rt-22222222")
	if err := st.SaveRuleset(rs); err != nil {
		t.Fatal(err)
	}
	pol, err := st.LoadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadRuleset()
	if err != nil {
		t.Fatal(err)
	}

	out := renderRouteRules(got, pol, st.Dir())
	for _, want := range []string{
		"authored rules (2)",
		"rl-live", "rl-old", "[RETIRED",
		"learned evidence (1)",
		ev.ID, "2 confirms",
		"provenance: rt-11111111, rt-22222222",
		"act ≥ 0.80",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rules listing missing %q:\n%s", want, out)
		}
	}
	// Retirement preserves history in the listing — it must never delete a row (#128 §79).
	if strings.Contains(out, "authored rules (1)") {
		t.Fatalf("retired rules must stay listed as tombstones:\n%s", out)
	}
}

func TestRouteRulesEmptyStoreStillReportsPolicy(t *testing.T) {
	st := testRouteStore(t)
	pol, err := st.LoadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	out := renderRouteRules(routing.Ruleset{}, pol, st.Dir())
	if !strings.Contains(out, "act ≥ 0.80") || !strings.Contains(out, "none") {
		t.Fatalf("an empty store must still show the active policy and say the tables are empty:\n%s", out)
	}
}

func TestRenderRouteEventAdvertisesExplain(t *testing.T) {
	res := routing.Result{Basis: routing.BasisRule, Outcome: routing.OutcomeAct, Target: "a", Confidence: 1.0, Source: "rl-1", Signal: "x"}
	ev := routing.Event{ID: "rt-0b0b0b0b", Kind: routing.EventDecision, Time: testNow, Result: &res}
	out := renderRouteEvent(ev)
	if !strings.Contains(out, "route explain rt-0b0b0b0b") {
		t.Fatalf("every recorded decision must advertise its explain command:\n%s", out)
	}
}
