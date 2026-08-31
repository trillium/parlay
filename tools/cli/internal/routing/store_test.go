package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPolicyMissingFileReturnsDefaults(t *testing.T) {
	s := NewStore(t.TempDir())
	p, err := s.LoadPolicy()
	if err != nil {
		t.Fatalf("LoadPolicy on empty store: %v", err)
	}
	if p != DefaultPolicy() {
		t.Fatalf("missing policy file should yield defaults, got %+v", p)
	}
}

func TestLoadPolicyCorruptJSONIsLoud(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policy.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir).LoadPolicy(); err == nil {
		t.Fatal("corrupt policy.json must be a loud error, not silent defaults")
	}
}

func TestLoadPolicyMissingFieldIsLoud(t *testing.T) {
	// {} decodes to act=0/refuse=0 — a policy under which everything acts
	// silently. Absent fields must be an error, not zero values.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{"actThreshold": 0.9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewStore(dir).LoadPolicy()
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("policy with a missing field must fail loudly, got %v", err)
	}
}

func TestLoadPolicyInvalidThresholdsAreLoud(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policy.json"),
		[]byte(`{"actThreshold": 0.4, "refuseThreshold": 0.6}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir).LoadPolicy(); err == nil {
		t.Fatal("inverted thresholds must fail Validate on load")
	}
}

func TestPolicyRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	want := Policy{ActThreshold: 0.9, RefuseThreshold: 0.3}
	if err := s.SavePolicy(want); err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	got, err := s.LoadPolicy()
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if got != want {
		t.Fatalf("round trip: got %+v want %+v", got, want)
	}
}

func TestSavePolicyRejectsInvalid(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.SavePolicy(Policy{ActThreshold: 0.2, RefuseThreshold: 0.8}); err == nil {
		t.Fatal("SavePolicy must refuse to persist an invalid policy")
	}
	if _, statErr := os.Stat(filepath.Join(s.Dir(), "policy.json")); !os.IsNotExist(statErr) {
		t.Fatal("a refused save must not leave a policy file behind")
	}
}

func TestLoadRulesetMissingFileIsEmpty(t *testing.T) {
	rs, err := NewStore(t.TempDir()).LoadRuleset()
	if err != nil {
		t.Fatalf("LoadRuleset on empty store: %v", err)
	}
	if len(rs.Rules) != 0 || len(rs.Learned) != 0 {
		t.Fatalf("missing rules file should yield empty ruleset, got %+v", rs)
	}
}

func TestLoadRulesetCorruptIsLoud(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rules.json"), []byte("]["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir).LoadRuleset(); err == nil {
		t.Fatal("corrupt rules.json must be a loud error, not an empty ruleset")
	}
}

func TestRulesetRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	want := Ruleset{
		Rules: []Rule{
			{ID: "r1", Key: "parlay", Target: "parlay-dev"},
			{ID: "r2", Key: "old", Target: "gone", Retired: true, Note: "superseded"},
		},
		Learned: []Evidence{
			{Signal: "notes", Target: "scribe", Confirms: 3, Corrections: 1,
				AgentEvents: 2, Provenance: []string{"rt-aaaa1111"}},
		},
	}
	if err := s.SaveRuleset(want); err != nil {
		t.Fatalf("SaveRuleset: %v", err)
	}
	got, err := s.LoadRuleset()
	if err != nil {
		t.Fatalf("LoadRuleset: %v", err)
	}
	if len(got.Rules) != 2 || len(got.Learned) != 1 {
		t.Fatalf("round trip lost entries: %+v", got)
	}
	if got.Rules[1].Retired != true || got.Learned[0].Confirms != 3 || got.Learned[0].AgentEvents != 2 {
		t.Fatalf("round trip mangled fields: %+v", got)
	}
}

func TestLedgerAppendAndFind(t *testing.T) {
	s := NewStore(t.TempDir())
	res := Result{Basis: BasisNone, Outcome: OutcomeNeedsInference, Signal: "mystery"}
	events := []Event{
		{ID: "rt-00000001", Kind: EventDecision, Time: "2026-08-30T10:00:00Z", Input: "mystery text", Result: &res},
		{ID: "rt-00000002", Kind: EventConfirm, Time: "2026-08-30T10:01:00Z", Decision: "rt-00000001", Authority: "captain"},
	}
	for _, ev := range events {
		if err := s.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent(%s): %v", ev.ID, err)
		}
	}
	all, err := s.Events()
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(all) != 2 || all[0].ID != "rt-00000001" || all[1].Authority != "captain" {
		t.Fatalf("ledger read back wrong: %+v", all)
	}
	got, ok, err := s.FindEvent("rt-00000001")
	if err != nil || !ok {
		t.Fatalf("FindEvent: ok=%v err=%v", ok, err)
	}
	if got.Result == nil || got.Result.Outcome != OutcomeNeedsInference {
		t.Fatalf("FindEvent dropped the recorded result: %+v", got)
	}
	if _, ok, _ := s.FindEvent("rt-nope"); ok {
		t.Fatal("FindEvent must not invent events")
	}
}

func TestLedgerMissingFileIsEmpty(t *testing.T) {
	all, err := NewStore(t.TempDir()).Events()
	if err != nil || len(all) != 0 {
		t.Fatalf("missing ledger should read as empty: events=%v err=%v", all, err)
	}
}

func TestLedgerMalformedLineIsLoud(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.AppendEvent(Event{ID: "rt-00000001", Kind: EventDecision, Time: "2026-08-30T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(s.LedgerPath(), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{torn line\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err = s.Events()
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("malformed ledger line must fail loudly naming the line, got %v", err)
	}
}

func TestAppendEventRejectsIncomplete(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.AppendEvent(Event{Kind: EventDecision, Time: "2026-08-30T10:00:00Z"}); err == nil {
		t.Fatal("an event without an id must be rejected")
	}
}

func TestNewEventIDShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := NewEventID()
		if !strings.HasPrefix(id, "rt-") || len(id) != len("rt-")+8 {
			t.Fatalf("unexpected id shape %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q in 100 draws", id)
		}
		seen[id] = true
	}
}
