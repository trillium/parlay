// Dry-run acceptance tests: the success leg must be provable without
// typing. Dry runs execute the real focus gate (request, settle, verify)
// and report exactly what would insert; they never call Insert.
package remoteinput

import (
	"testing"
)

func TestDryRunReportsWouldInsertByteForByte(t *testing.T) {
	fake := &FakeTalon{ActiveAppName: "Terminal"}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	text := "dry ✓ proof\nsecond line\t\"quoted\""
	id := svc.Submit(Submission{Device: "d1", Text: text, App: "terminal", DryRun: true})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusDryRunPassed {
		t.Fatalf("expected dry_run_passed, got %+v", o)
	}
	if o.Focus != FocusVerified {
		t.Fatalf("dry run must verify focus, got %+v", o)
	}
	if !o.DryRun {
		t.Fatalf("dry run outcome must flag dryRun: %+v", o)
	}
	if o.InjectAttempted {
		t.Fatalf("dry run must type nothing: %+v", o)
	}
	if o.WouldInsert != text {
		t.Fatalf("wouldInsert mismatch:\nwant %q\ngot  %q", text, o.WouldInsert)
	}
	if len(fake.Inserts) != 0 {
		t.Fatalf("dry run typed %d texts", len(fake.Inserts))
	}
}

func TestDryRunFocusFailureInjectsNothing(t *testing.T) {
	fake := &FakeTalon{ActiveAppName: "SomeOtherApp", StickyActive: true}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "d1", Text: "keep me", App: "Terminal", DryRun: true})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusFocusFailed {
		t.Fatalf("expected focus_failed, got %+v", o)
	}
	if o.InjectAttempted || len(fake.Inserts) != 0 {
		t.Fatalf("focus failure must inject nothing: %+v", o)
	}
	if !o.PreserveText {
		t.Fatalf("focus failure must preserve text: %+v", o)
	}
}

func TestDryRunWithoutTargetSkipsFocus(t *testing.T) {
	fake := &FakeTalon{}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "d1", Text: "no target", DryRun: true})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusDryRunPassed {
		t.Fatalf("expected dry_run_passed, got %+v", o)
	}
	if o.Focus != FocusNotRequired {
		t.Fatalf("expected not_required focus, got %+v", o)
	}
	if o.WouldInsert != "no target" {
		t.Fatalf("wouldInsert = %q, want %q", o.WouldInsert, "no target")
	}
	if len(fake.Inserts) != 0 {
		t.Fatalf("dry run typed %d texts", len(fake.Inserts))
	}
}
