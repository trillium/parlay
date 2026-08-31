package supersession

import (
	"strings"
	"testing"
)

// The observability contract: "why was this record superseded and what did
// it trigger" is answerable for any record, from the ledger alone.
func TestExplain(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
	if _, err := l.MarkActedOn("wf-1", ActedOnMark{Actor: ActorCaptain, Note: "merged PR #7", At: "2026-08-30T01:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	mustSupersede(t, l, "wf-2", "wf-1", "2.0.0", ChangeBreaking, "removed manual approval stage")

	t.Run("superseded record: why and what it triggered", func(t *testing.T) {
		e, err := l.Explain("wf-1")
		if err != nil {
			t.Fatal(err)
		}
		if e.IsHead {
			t.Fatal("wf-1 is superseded, not head")
		}
		if e.Origin != nil {
			t.Fatal("wf-1 is a chain root; want nil Origin")
		}
		if len(e.ActedOn) != 1 || e.ActedOn[0].Actor != ActorCaptain {
			t.Fatalf("ActedOn = %v; want the captain mark", e.ActedOn)
		}
		d := e.Superseded
		if d == nil {
			t.Fatal("want Superseded detail")
		}
		if d.NewHeadID != "wf-2" || d.Reason != "removed manual approval stage" {
			t.Fatalf("detail = %+v; want successor wf-2 with the reason", d)
		}
		if d.DeclaredSeverity != SeverityMajor || d.ClassifiedSeverity != SeverityMajor {
			t.Fatalf("severities = %s/%s; want major/major", d.DeclaredSeverity, d.ClassifiedSeverity)
		}
		if d.Requirement == nil || d.Requirement.Action != ActionReprocess || !d.Requirement.CaptainVisible || !d.Requirement.StalenessSource {
			t.Fatalf("requirement = %+v; want captain-visible reprocess staleness source", d.Requirement)
		}
		if d.Requirement.Resolved {
			t.Fatal("requirement should still be pending")
		}
	})

	t.Run("head record: origin explains the supersession that created it", func(t *testing.T) {
		e, err := l.Explain("wf-2")
		if err != nil {
			t.Fatal(err)
		}
		if !e.IsHead || e.Superseded != nil {
			t.Fatalf("wf-2 should be head with no Superseded; got %+v", e)
		}
		if e.Origin == nil || e.Origin.SupersededID != "wf-1" {
			t.Fatalf("Origin = %+v; want the wf-1 supersession", e.Origin)
		}
	})

	t.Run("explanation tracks live resolution state", func(t *testing.T) {
		reqID := l.PendingRequirements()[0].ID
		if _, err := l.ResolveRequirement(reqID, Resolution{Actor: ActorCaptain, Note: "reviewed; dependents requeued", At: "2026-08-30T04:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		e, err := l.Explain("wf-1")
		if err != nil {
			t.Fatal(err)
		}
		r := e.Superseded.Requirement
		if !r.Resolved || r.ResolvedBy != ActorCaptain || r.ResolutionNote != "reviewed; dependents requeued" {
			t.Fatalf("requirement after resolve = %+v; want resolved with evidence", r)
		}
	})

	t.Run("render carries the whole story", func(t *testing.T) {
		e, _ := l.Explain("wf-1")
		out := e.Render()
		for _, want := range []string{
			"record wf-1", `workflow "triage" v1.0.0`,
			"superseded by wf-2",
			"acted on by captain (merged PR #7)",
			"registered as chain root",
			"declared major, classified major",
			"reason: removed manual approval stage",
			"change [breaking]",
			"req-3 → reprocess [captain-visible] [staleness-source]",
			"resolved by captain (reviewed; dependents requeued)",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("Render missing %q in:\n%s", want, out)
			}
		}
	})

	t.Run("unknown record fails loudly", func(t *testing.T) {
		if _, err := l.Explain("wf-99"); err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("patch with no requirement renders as nothing owed", func(t *testing.T) {
		l2 := NewLedger()
		mustRegister(t, l2, "ct-1", "contract", "notes", "1.0.0")
		mustSupersede(t, l2, "ct-2", "ct-1", "1.0.1", ChangeAnnotation, "typo")
		e, err := l2.Explain("ct-1")
		if err != nil {
			t.Fatal(err)
		}
		if e.Superseded.Requirement != nil {
			t.Fatalf("patch requirement = %+v; want nil", e.Superseded.Requirement)
		}
		if out := e.Render(); !strings.Contains(out, "triggered: nothing") {
			t.Errorf("Render missing the nothing-owed line:\n%s", out)
		}
	})
}
