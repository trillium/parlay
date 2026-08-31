package supersession

import (
	"reflect"
	"strings"
	"testing"
)

// Each severity class drives its distinct reprocessing consequence.
func TestSeverityDrivesReprocessing(t *testing.T) {
	t.Run("patch emits nothing when no authority relied on the record", func(t *testing.T) {
		l := NewLedger()
		mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
		mustSupersede(t, l, "wf-2", "wf-1", "1.0.1", ChangeAnnotation, "typo")
		if got := l.PendingRequirements(); len(got) != 0 {
			t.Fatalf("patch supersession emitted requirements: %v", got)
		}
		if got := l.RequirementsFor("wf-1"); len(got) != 0 {
			t.Fatalf("RequirementsFor(wf-1) = %v; want none", got)
		}
	})

	t.Run("minor emits a revalidate requirement, not a staleness source", func(t *testing.T) {
		l := NewLedger()
		mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
		mustSupersede(t, l, "wf-2", "wf-1", "1.1.0", ChangeAdditive, "new optional stage")
		pending := l.PendingRequirements()
		if len(pending) != 1 {
			t.Fatalf("want 1 requirement, got %v", pending)
		}
		r := pending[0]
		if r.Action != ActionRevalidate || r.Severity != SeverityMinor {
			t.Fatalf("minor requirement = %+v; want revalidate/minor", r)
		}
		if r.StalenessSource {
			t.Fatal("minor supersession must not be a staleness source — outputs are presumed valid")
		}
		if r.CaptainVisible {
			t.Fatal("no captain mark, so requirement must not be captain-visible")
		}
		if r.SupersededID != "wf-1" || r.NewHeadID != "wf-2" {
			t.Fatalf("requirement ids = %s→%s; want wf-1→wf-2", r.SupersededID, r.NewHeadID)
		}
	})

	t.Run("major emits a reprocess requirement and is the staleness source", func(t *testing.T) {
		l := NewLedger()
		mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
		mustSupersede(t, l, "wf-2", "wf-1", "2.0.0", ChangeBreaking, "removed stage")
		pending := l.PendingRequirements()
		if len(pending) != 1 {
			t.Fatalf("want 1 requirement, got %v", pending)
		}
		r := pending[0]
		if r.Action != ActionReprocess || r.Severity != SeverityMajor {
			t.Fatalf("major requirement = %+v; want reprocess/major", r)
		}
		if !r.StalenessSource {
			t.Fatal("major supersession must be a staleness source (the task-4cfpv.14 seam)")
		}
	})
}

// The captain-authority boundary: superseding a record the captain acted on
// is never silent, whatever the severity.
func TestCaptainVisibility(t *testing.T) {
	t.Run("patch of a captain-acted-on record upgrades to a visible notice", func(t *testing.T) {
		l := NewLedger()
		mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
		if _, err := l.MarkActedOn("wf-1", ActedOnMark{Actor: ActorCaptain, Note: "merged the run"}); err != nil {
			t.Fatal(err)
		}
		mustSupersede(t, l, "wf-2", "wf-1", "1.0.1", ChangeAnnotation, "typo")
		pending := l.PendingRequirements()
		if len(pending) != 1 {
			t.Fatalf("want 1 requirement, got %v", pending)
		}
		r := pending[0]
		if r.Action != ActionNotice || !r.CaptainVisible {
			t.Fatalf("requirement = %+v; want a captain-visible notice", r)
		}
		if r.StalenessSource {
			t.Fatal("a notice mandates visibility, not reprocessing")
		}
	})

	t.Run("major of a captain-acted-on record is captain-visible reprocess", func(t *testing.T) {
		l := NewLedger()
		mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
		if _, err := l.MarkActedOn("wf-1", ActedOnMark{Actor: ActorCaptain}); err != nil {
			t.Fatal(err)
		}
		mustSupersede(t, l, "wf-2", "wf-1", "2.0.0", ChangeBreaking, "removed stage")
		r := l.PendingRequirements()[0]
		if r.Action != ActionReprocess || !r.CaptainVisible || !r.StalenessSource {
			t.Fatalf("requirement = %+v; want captain-visible reprocess staleness source", r)
		}
	})

	t.Run("a non-captain acted-on mark does not trigger the rule", func(t *testing.T) {
		l := NewLedger()
		mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
		if _, err := l.MarkActedOn("wf-1", ActedOnMark{Actor: "agent-7"}); err != nil {
			t.Fatal(err)
		}
		mustSupersede(t, l, "wf-2", "wf-1", "1.0.1", ChangeAnnotation, "typo")
		if got := l.PendingRequirements(); len(got) != 0 {
			t.Fatalf("non-captain mark emitted requirements: %v", got)
		}
	})
}

func TestResolveRequirement(t *testing.T) {
	setup := func(withCaptainMark bool) (*Ledger, string) {
		l := NewLedger()
		mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
		if withCaptainMark {
			if _, err := l.MarkActedOn("wf-1", ActedOnMark{Actor: ActorCaptain}); err != nil {
				t.Fatal(err)
			}
		}
		mustSupersede(t, l, "wf-2", "wf-1", "2.0.0", ChangeBreaking, "removed stage")
		return l, l.PendingRequirements()[0].ID
	}

	t.Run("resolution with evidence discharges the requirement", func(t *testing.T) {
		l, id := setup(false)
		if _, err := l.ResolveRequirement(id, Resolution{Actor: "agent-7", Note: "reran dependents green", At: "2026-08-30T03:00:00Z"}); err != nil {
			t.Fatalf("ResolveRequirement: %v", err)
		}
		if got := l.PendingRequirements(); len(got) != 0 {
			t.Fatalf("resolved requirement still pending: %v", got)
		}
		r, ok := l.Requirement(id)
		if !ok || !r.Resolved || r.ResolvedBy != "agent-7" || r.ResolutionNote != "reran dependents green" {
			t.Fatalf("Requirement(%s) = %+v; want resolved with evidence", id, r)
		}
	})

	t.Run("rejections", func(t *testing.T) {
		l, id := setup(false)
		cases := []struct {
			name    string
			id      string
			res     Resolution
			wantErr string
		}{
			{"unknown requirement", "req-99", Resolution{Actor: "a", Note: "n"}, "does not exist"},
			{"empty actor", id, Resolution{Note: "n"}, "actor must not be empty"},
			{"empty note", id, Resolution{Actor: "a"}, "must carry evidence"},
		}
		for _, c := range cases {
			if _, err := l.ResolveRequirement(c.id, c.res); err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("%s: want error containing %q, got %v", c.name, c.wantErr, err)
			}
		}
		if _, err := l.ResolveRequirement(id, Resolution{Actor: "a", Note: "n"}); err != nil {
			t.Fatal(err)
		}
		if _, err := l.ResolveRequirement(id, Resolution{Actor: "a", Note: "n"}); err == nil || !strings.Contains(err.Error(), "already resolved") {
			t.Fatalf("double resolve: want already-resolved error, got %v", err)
		}
	})

	t.Run("captain-visible requirements are captain-only", func(t *testing.T) {
		l, id := setup(true)
		if _, err := l.ResolveRequirement(id, Resolution{Actor: "agent-7", Note: "I looked at it"}); err == nil || !strings.Contains(err.Error(), "only \"captain\"") {
			t.Fatalf("non-captain resolve of captain-visible requirement: want refusal, got %v", err)
		}
		if _, err := l.ResolveRequirement(id, Resolution{Actor: ActorCaptain, Note: "reviewed and re-approved"}); err != nil {
			t.Fatalf("captain resolve: %v", err)
		}
	})
}

func TestRequirementQueueOrderAndReplay(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
	mustRegister(t, l, "ct-1", "contract", "notes-source", "1.0.0")
	mustSupersede(t, l, "wf-2", "wf-1", "1.1.0", ChangeAdditive, "new stage")
	mustSupersede(t, l, "ct-2", "ct-1", "2.0.0", ChangeBreaking, "metadata field removed")

	pending := l.PendingRequirements()
	if len(pending) != 2 || pending[0].SupersededID != "wf-1" || pending[1].SupersededID != "ct-1" {
		t.Fatalf("pending queue = %v; want wf-1 then ct-1", pending)
	}
	if _, err := l.ResolveRequirement(pending[0].ID, Resolution{Actor: "agent-7", Note: "revalidated", At: "t"}); err != nil {
		t.Fatal(err)
	}
	pending = l.PendingRequirements()
	if len(pending) != 1 || pending[0].SupersededID != "ct-1" {
		t.Fatalf("after resolve, pending = %v; want just ct-1", pending)
	}

	// Requirements and their resolution state survive replay.
	l2, err := Replay(l.Events())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !reflect.DeepEqual(l.PendingRequirements(), l2.PendingRequirements()) {
		t.Fatal("replayed pending requirements differ")
	}
	for _, id := range []string{l.requirementOrder[0], l.requirementOrder[1]} {
		a, _ := l.Requirement(id)
		b, _ := l2.Requirement(id)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("replayed requirement %s differs: %+v vs %+v", id, b, a)
		}
	}
}
