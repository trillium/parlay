// Targets + no-target-rule tests (task-46ys9): Talon-name enumeration
// parsing (mocked repl readout with a windowless app and a focused app),
// the targetless refusal/named-mode path, and dry-run safety for both.
// All run against FakeTalon or a mocked repl — the live REPL never runs.
package remoteinput

import (
	"strings"
	"testing"
)

// mockTargetsJSON is a Talon-shaped readout: WezTerm focused with one
// window, Terminal unfocused with two, and a launchable app with none.
const mockTargetsJSON = `{"active": "WezTerm", "apps": [` +
	`{"name": "WezTerm", "windows": ["macbookpro: coder"]}, ` +
	`{"name": "Terminal", "windows": ["first — 80×24", "second — 120×40"]}, ` +
	`{"name": "Raycast", "windows": []}]}`

func TestParseTargetsJSON(t *testing.T) {
	targets, err := parseTargetsJSON(mockTargetsJSON)
	if err != nil {
		t.Fatalf("parseTargetsJSON: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d: %+v", len(targets), targets)
	}
	// Talon's ordering is preserved exactly.
	if targets[0].Name != "WezTerm" || targets[1].Name != "Terminal" || targets[2].Name != "Raycast" {
		t.Fatalf("ordering lost: %+v", targets)
	}
	// Names are exact Talon names (round-trip through verification).
	if targets[0].WindowTitle != "macbookpro: coder" {
		t.Fatalf("relevant window title wrong: %+v", targets[0])
	}
	// Exactly one app is focused.
	if !targets[0].Focused || targets[1].Focused || targets[2].Focused {
		t.Fatalf("focused flags wrong: %+v", targets)
	}
	if targets[1].WindowCount != 2 || !targets[1].HasWindows {
		t.Fatalf("window count/marker wrong: %+v", targets[1])
	}
	// The windowless app stays listed with an explicit marker.
	if targets[2].WindowCount != 0 || targets[2].HasWindows {
		t.Fatalf("windowless app must carry hasWindows:false: %+v", targets[2])
	}
	if targets[2].WindowTitle != "" {
		t.Fatalf("windowless app must have no title: %+v", targets[2])
	}
}

func TestParseTargetsJSONRejectsBadInput(t *testing.T) {
	if _, err := parseTargetsJSON(`not json`); err == nil {
		t.Fatal("expected an error for non-JSON readout")
	}
}

func TestTargetsSnippetIsOnePrintStatement(t *testing.T) {
	snip := targetsSnippet()
	if !strings.HasPrefix(snip, "print(") || !strings.HasSuffix(snip, ")") {
		t.Fatalf("targets read must be one print(...) call: %q", snip)
	}
	if strings.Contains(snip, "\n") {
		t.Fatalf("the repl compiles ONE statement; no newlines: %q", snip)
	}
	for _, want := range []string{"ui.apps()", "ui.active_app()", ".windows()", "json"} {
		if !strings.Contains(snip, want) {
			t.Fatalf("snippet missing %q: %q", want, snip)
		}
	}
}

func TestTargetsReadsThroughMockRepl(t *testing.T) {
	talon := mockRepl(t, mockTargetsJSON+"\n")
	targets, err := talon.Targets()
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 3 || !targets[0].Focused || targets[2].HasWindows {
		t.Fatalf("unexpected targets: %+v", targets)
	}
}

func TestTargetsIsDryRunSafe(t *testing.T) {
	fake := &FakeTalon{TargetsList: []Target{{Name: "WezTerm", Focused: true, WindowCount: 1, HasWindows: true}}}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	targets, err := svc.Targets()
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 1 || targets[0].Name != "WezTerm" {
		t.Fatalf("unexpected targets: %+v", targets)
	}
	if len(fake.Inserts) != 0 || len(fake.FocusAppCalls) != 0 || len(fake.FocusWindowCalls) != 0 {
		t.Fatalf("targets must type and focus nothing: %+v", fake)
	}
}

func TestTargetlessLiveSubmitIsRefused(t *testing.T) {
	fake := &FakeTalon{}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "d1", Text: "blind?", Trigger: "send it"})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusFocusFailed {
		t.Fatalf("expected focus_failed, got %+v", o)
	}
	if o.InjectAttempted || len(fake.Inserts) != 0 {
		t.Fatalf("refused submit must inject nothing: %+v", o)
	}
	if !o.PreserveText || !o.StripTrigger {
		t.Fatalf("refused submit must preserve text and strip trigger: %+v", o)
	}
	if !strings.Contains(o.Error, "allowUnfocused") {
		t.Fatalf("typed refusal must name the flag: %+v", o)
	}
}

func TestAllowUnfocusedInjectsWithModeSurfaced(t *testing.T) {
	fake := &FakeTalon{}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "d1", Text: "explicit", AllowUnfocused: true})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusInjected {
		t.Fatalf("expected injected, got %+v", o)
	}
	if o.Focus != FocusAllowedUnfocused {
		t.Fatalf("named mode must surface as focus %q: %+v", FocusAllowedUnfocused, o)
	}
	if !o.AllowUnfocused {
		t.Fatalf("named mode must echo allowUnfocused: %+v", o)
	}
	if len(fake.Inserts) != 1 || fake.Inserts[0] != "explicit" {
		t.Fatalf("expected one literal insert, got %q", fake.Inserts)
	}
}

func TestAllowUnfocusedDryRunStaysSafe(t *testing.T) {
	fake := &FakeTalon{}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	text := "dry ✓ explicit\nline"
	id := svc.Submit(Submission{Device: "d1", Text: text, AllowUnfocused: true, DryRun: true})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusDryRunPassed {
		t.Fatalf("expected dry_run_passed, got %+v", o)
	}
	if o.Focus != FocusAllowedUnfocused || !o.AllowUnfocused {
		t.Fatalf("dry run must surface the named mode: %+v", o)
	}
	if o.WouldInsert != text {
		t.Fatalf("wouldInsert = %q, want %q", o.WouldInsert, text)
	}
	if len(fake.Inserts) != 0 {
		t.Fatalf("dry run typed %d texts", len(fake.Inserts))
	}
}
