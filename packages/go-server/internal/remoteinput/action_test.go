// Action-mode tests: allow-listed slide actions only. Dry runs report
// the constant snippet and call nothing. A name outside the list,
// including quit and a snippet-shaped string, is rejected before the
// adapter runs.
package remoteinput

import (
	"strings"
	"testing"
)

func TestActionDryRunReportsWouldRunAndCallsNothing(t *testing.T) {
	fake := &FakeTalon{}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	for _, name := range []string{"slide_next", "slide_previous"} {
		id := svc.Submit(Submission{
			Device: "phone-1", Mode: ModeAction, Action: name, DryRun: true,
			Text: "slide next",
		})
		o := waitOutcome(t, svc, id)
		if o.Status != StatusDryRunPassed || !o.DryRun {
			t.Fatalf("%s: expected dry_run_passed, got %+v", name, o)
		}
		want, ok := ActionSnippet(name)
		if !ok {
			t.Fatalf("%s missing from allow-list", name)
		}
		if o.WouldRun != want {
			t.Fatalf("%s wouldRun = %q, want %q", name, o.WouldRun, want)
		}
		if o.WouldInsert != "" || o.InjectAttempted || o.Mode != ModeAction {
			t.Fatalf("%s dry run must not look like an insert: %+v", name, o)
		}
	}
	if len(fake.RunActions) != 0 || len(fake.Inserts) != 0 || len(fake.FocusAppCalls) != 0 {
		t.Fatalf("dry run touched Talon: actions=%v inserts=%v focus=%v",
			fake.RunActions, fake.Inserts, fake.FocusAppCalls)
	}
}

func TestActionLiveRunsAllowlistedActionAndDoesNotInsert(t *testing.T) {
	fake := &FakeTalon{}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	id := svc.Submit(Submission{
		Device: "phone-1", Mode: ModeAction, Action: "slide_next",
		Text: "must not be typed",
	})
	o := waitOutcome(t, svc, id)
	if o.Status != StatusInjected {
		t.Fatalf("expected injected, got %+v", o)
	}
	if o.WouldRun != "actions.user.slide_next()" || o.Mode != ModeAction {
		t.Fatalf("outcome must name the snippet: %+v", o)
	}
	if !o.InjectAttempted || o.Focus != FocusNotRequired {
		t.Fatalf("live action: attempted, no focus gate: %+v", o)
	}
	if len(fake.RunActions) != 1 || fake.RunActions[0] != "slide_next" {
		t.Fatalf("RunActions = %v, want [slide_next]", fake.RunActions)
	}
	if len(fake.Inserts) != 0 {
		t.Fatalf("action mode inserted %v", fake.Inserts)
	}
}

func TestActionQuitRejectedBeforeTalon(t *testing.T) {
	fake := &FakeTalon{}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	for _, name := range []string{"quit", "", "actions.user.slide_next()", "slide_next(); quit()"} {
		id := svc.Submit(Submission{
			Device: "phone-1", Mode: ModeAction, Action: name, DryRun: true,
		})
		o := waitOutcome(t, svc, id)
		if o.Status != StatusInjectFailed {
			t.Fatalf("action %q: expected inject_failed, got %+v", name, o)
		}
		if !strings.Contains(o.Error, "not allowed") {
			t.Fatalf("action %q: error must say not allowed: %+v", name, o)
		}
		if o.WouldRun != "" || o.InjectAttempted || o.DryRun {
			t.Fatalf("action %q: rejection must not look like a run: %+v", name, o)
		}
	}
	if len(fake.RunActions) != 0 || len(fake.Inserts) != 0 {
		t.Fatalf("rejected action reached Talon: actions=%v inserts=%v", fake.RunActions, fake.Inserts)
	}
}
