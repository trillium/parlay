package commands

import (
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/routing"
)

// confirmChain runs decide→propose→confirm once for the same input and
// returns the confirm's evidence state. The loop under test is the whole
// #128 §35 story: inference answers, the captain confirms, evidence accrues.
func confirmChain(t *testing.T, st *routing.Store, input, target string, auth routing.Authority) routing.Evidence {
	t.Helper()
	dec, err := routeDecideRun(st, input, nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	var refID string
	switch dec.Result.Outcome {
	case routing.OutcomeNeedsInference:
		prop, err := routeProposeRun(st, dec.ID, target, 0.65, testNow)
		if err != nil {
			t.Fatal(err)
		}
		refID = prop.ID
	case routing.OutcomeConfirm:
		refID = dec.ID
	default:
		t.Fatalf("confirmChain expected needs-inference or confirm, got %s", dec.Result.Outcome)
	}
	_, evidence, err := routeConfirmRun(st, refID, auth, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

// The required hardening test: three clean captain confirmations lift the
// route to the act threshold, and the NEXT decide acts silently on it.
func TestRouteHardensAfterRepeatedCaptainConfirmation(t *testing.T) {
	st := testRouteStore(t)
	input := "parlay, auth is broken again"

	for i := 1; i <= 3; i++ {
		e := confirmChain(t, st, input, "parlay-dev", routing.AuthorityCaptain)
		if i < 3 && e.Confidence() >= 0.80 {
			t.Fatalf("hardened after only %d confirms: %+v", i, e)
		}
		if i == 3 && e.Confidence() < 0.80 {
			t.Fatalf("3 captain confirms must reach the act threshold, got %.3f", e.Confidence())
		}
	}

	dec, err := routeDecideRun(st, input, nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result.Outcome != routing.OutcomeAct || dec.Result.Basis != routing.BasisHardened || dec.Result.Target != "parlay-dev" {
		t.Fatalf("after hardening, decide must act via the hardened entry, got %+v", dec.Result)
	}
}

// The required un-hardening test: one captain correction demotes a hardened
// route below the act threshold — the next decide asks instead of acting —
// and the correction simultaneously teaches the right target.
func TestRouteUnhardensAfterCorrection(t *testing.T) {
	st := testRouteStore(t)
	input := "notes, capture this idea"
	for i := 0; i < 3; i++ {
		confirmChain(t, st, input, "scribe", routing.AuthorityCaptain)
	}
	hardened, err := routeDecideRun(st, input, nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if hardened.Result.Outcome != routing.OutcomeAct {
		t.Fatalf("precondition: route must be hardened, got %+v", hardened.Result)
	}

	// The captain says this one actually belonged at archivist.
	_, demoted, taught, err := routeCorrectRun(st, hardened.ID, "archivist", routing.AuthorityCaptain, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if demoted == nil || demoted.Corrections != 1 {
		t.Fatalf("the wrong target must take a correction: %+v", demoted)
	}
	if demoted.Confidence() >= 0.80 {
		t.Fatalf("one correction against three confirms must demote below act: %.3f", demoted.Confidence())
	}
	if taught.Target != "archivist" || taught.Confirms != 1 {
		t.Fatalf("the correction must teach the right target: %+v", taught)
	}

	after, err := routeDecideRun(st, input, nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if after.Result.Outcome != routing.OutcomeConfirm {
		t.Fatalf("a demoted route must ask, not act: %+v", after.Result)
	}
}

// The captain-authority invariant end-to-end: agent confirmations move the
// observability counter and NOTHING else — the route never hardens.
func TestAgentConfirmationsNeverHarden(t *testing.T) {
	st := testRouteStore(t)
	input := "deploy, ship the release"
	var last routing.Evidence
	for i := 0; i < 5; i++ {
		last = confirmChain(t, st, input, "shipper", routing.AuthorityAgent)
	}
	if last.Confirms != 0 || last.AgentEvents != 5 {
		t.Fatalf("agent feedback must count only AgentEvents: %+v", last)
	}
	if last.Confidence() != 0.5 {
		t.Fatalf("evidence with no captain feedback must sit at the prior 0.5, got %.3f", last.Confidence())
	}
	dec, err := routeDecideRun(st, input, nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result.Outcome == routing.OutcomeAct {
		t.Fatalf("a route no captain ever confirmed must never act silently: %+v", dec.Result)
	}
}

func TestRouteCorrectTeachesOnNeedsInference(t *testing.T) {
	st := testRouteStore(t)
	dec, err := routeDecideRun(st, "budget, what did we spend", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result.Outcome != routing.OutcomeNeedsInference {
		t.Fatalf("precondition: %+v", dec.Result)
	}
	_, demoted, taught, err := routeCorrectRun(st, dec.ID, "ledger-keeper", routing.AuthorityCaptain, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if demoted != nil {
		t.Fatalf("nothing was decided, so nothing demotes: %+v", demoted)
	}
	if taught.Confirms != 1 || taught.Signal != "budget" {
		t.Fatalf("teaching must confirm (signal → right target): %+v", taught)
	}
}

func TestRouteConfirmRefusesUnkeyedDecision(t *testing.T) {
	st := testRouteStore(t)
	dec, err := routeDecideRun(st, "!!!", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result.Signal != "" {
		t.Fatalf("precondition: %q should have no signal", "!!!")
	}
	prop, err := routeProposeRun(st, dec.ID, "mayor", 0.9, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := routeConfirmRun(st, prop.ID, routing.AuthorityCaptain, testNow); err == nil {
		t.Fatal("an unkeyed input must never accrue evidence")
	}
}

func TestRouteConfirmRefusesTargetlessDecision(t *testing.T) {
	st := testRouteStore(t)
	dec, err := routeDecideRun(st, "mystery unrouted text", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := routeConfirmRun(st, dec.ID, routing.AuthorityCaptain, testNow); err == nil {
		t.Fatal("confirming a needs-inference decision with no proposal must error")
	}
}

func TestRouteCorrectSameTargetIsRejected(t *testing.T) {
	st := testRouteStore(t)
	if err := st.SaveRuleset(routing.Ruleset{
		Rules: []routing.Rule{{ID: "r1", Key: "parlay", Target: "parlay-dev"}},
	}); err != nil {
		t.Fatal(err)
	}
	dec, err := routeDecideRun(st, "parlay is down", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := routeCorrectRun(st, dec.ID, "parlay-dev", routing.AuthorityCaptain, testNow); err == nil {
		t.Fatal("correcting to the already-decided target must point at confirm instead")
	}
}

func TestRouteRetireLearnedEntryStopsMatching(t *testing.T) {
	st := testRouteStore(t)
	input := "standup, daily notes"
	for i := 0; i < 3; i++ {
		confirmChain(t, st, input, "scrum-bot", routing.AuthorityCaptain)
	}
	dec, err := routeDecideRun(st, input, nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result.Outcome != routing.OutcomeAct {
		t.Fatalf("precondition: hardened, got %+v", dec.Result)
	}
	rs, err := st.LoadRuleset()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Learned) != 1 || rs.Learned[0].ID == "" {
		t.Fatalf("learned entry must carry an id: %+v", rs.Learned)
	}
	kind, _, err := rs.RetireEntry(rs.Learned[0].ID, "wrong bot")
	if err != nil || kind != "evidence" {
		t.Fatalf("RetireEntry: kind=%q err=%v", kind, err)
	}
	if err := st.SaveRuleset(rs); err != nil {
		t.Fatal(err)
	}
	after, err := routeDecideRun(st, input, nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if after.Result.Outcome != routing.OutcomeNeedsInference {
		t.Fatalf("a retired entry must never match again: %+v", after.Result)
	}
	// History preserved: the tombstone is still in the ruleset.
	rs2, err := st.LoadRuleset()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs2.Learned) != 1 || !rs2.Learned[0].Retired || rs2.Learned[0].Confirms != 3 {
		t.Fatalf("retirement must preserve history: %+v", rs2.Learned)
	}
}

func TestRouteRuleAddAndRetire(t *testing.T) {
	st := testRouteStore(t)
	rs := routing.Ruleset{}
	rule, err := rs.AddRule("", "Parlay Auth!", "auth-dev", "")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Key != "parlay auth" {
		t.Fatalf("rule key must be stored normalized, got %q", rule.Key)
	}
	if _, err := rs.AddRule(rule.ID, "other", "x", ""); err == nil {
		t.Fatal("duplicate rule id must be rejected")
	}
	if _, err := rs.AddRule("", "?!", "x", ""); err == nil {
		t.Fatal("a key normalizing to nothing must be rejected")
	}
	if err := st.SaveRuleset(rs); err != nil {
		t.Fatal(err)
	}

	dec, err := routeDecideRun(st, "parlay auth is broken", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result.Outcome != routing.OutcomeAct || dec.Result.Target != "auth-dev" {
		t.Fatalf("authored rule must act: %+v", dec.Result)
	}

	rs, err = st.LoadRuleset()
	if err != nil {
		t.Fatal(err)
	}
	kind, _, err := rs.RetireEntry(rule.ID, "superseded")
	if err != nil || kind != "rule" {
		t.Fatalf("retire authored rule: kind=%q err=%v", kind, err)
	}
	if _, _, err := rs.RetireEntry(rule.ID, ""); err == nil {
		t.Fatal("double-retire must be rejected")
	}
	if err := st.SaveRuleset(rs); err != nil {
		t.Fatal(err)
	}
	after, err := routeDecideRun(st, "parlay auth is broken", nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if after.Result.Outcome == routing.OutcomeAct {
		t.Fatalf("a retired rule must never act: %+v", after.Result)
	}
}

func TestParseAuthorityDefaultsToAgent(t *testing.T) {
	auth, err := routing.ParseAuthority("")
	if err != nil || auth != routing.AuthorityAgent {
		t.Fatalf("unlabeled feedback must default to agent (fails toward not-hardening): %v %v", auth, err)
	}
	if _, err := routing.ParseAuthority("root"); err == nil {
		t.Fatal("unknown authority must be rejected")
	}
}

func TestFeedbackLedgerTrail(t *testing.T) {
	st := testRouteStore(t)
	e := confirmChain(t, st, "parlay, check auth", "parlay-dev", routing.AuthorityCaptain)
	events, err := st.Events()
	if err != nil {
		t.Fatal(err)
	}
	// decide + proposal + confirm = 3 events, each linked to its predecessor.
	if len(events) != 3 {
		t.Fatalf("expected 3 ledger events, got %d", len(events))
	}
	confirm := events[2]
	if confirm.Kind != routing.EventConfirm || confirm.Decision != events[1].ID || confirm.Entry != e.ID {
		t.Fatalf("confirm event must reference the proposal and the evidence entry: %+v", confirm)
	}
	if e.Provenance[0] != events[1].ID {
		t.Fatalf("evidence provenance must name the proposal it came from: %+v", e.Provenance)
	}
}
