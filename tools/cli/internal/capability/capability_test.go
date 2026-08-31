package capability

import (
	"encoding/json"
	"reflect"
	"testing"
)

// declare builds a valid declaration accepting the given names.
func declare(t *testing.T, accepts ...string) *Declaration {
	t.Helper()
	m := map[string]json.RawMessage{}
	for _, n := range accepts {
		m[n] = json.RawMessage(`{}`)
	}
	d := &Declaration{Schema: "1.0.0", Surface: Surface{Kind: "panel", Instance: "dev-1"}, Accepts: m}
	if err := d.Validate(); err != nil {
		t.Fatalf("fixture declaration invalid: %v", err)
	}
	return d
}

func TestClassify(t *testing.T) {
	cases := map[string]Class{
		"connected":    ClassLifecycle,
		"navigate":     ClassPresentationCommand,
		"reload":       ClassPresentationCommand,
		"device_cmd":   ClassPresentationCommand,
		"input_action": ClassPresentationCommand,
		"draft":        ClassPresentationCommand,
		"message":      ClassStateReport,
		"history":      ClassStateReport,
		"presence_map": ClassStateReport,
		"tool_event":   ClassStateReport,
		// Unknown names are state reports by construction: a new event is
		// deliverable-to-everyone until deliberately admitted to the gated
		// class.
		"some_future_event": ClassStateReport,
	}
	for event, want := range cases {
		if got := Classify(event); got != want {
			t.Errorf("Classify(%q) = %v, want %v", event, got, want)
		}
	}
}

func TestPresentationCommandsRosterIsTheFiveQ2dNames(t *testing.T) {
	want := []string{"device_cmd", "draft", "input_action", "navigate", "reload"}
	if got := PresentationCommands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("PresentationCommands() = %v, want %v", got, want)
	}
}

// The gate table from docs/interface-capabilities.md, row by row.
func TestDecide(t *testing.T) {
	declared := declare(t, "navigate", "draft")
	cases := []struct {
		name    string
		decl    *Declaration
		event   string
		verdict Verdict
		reason  Reason
	}{
		{"legacy client gets commands", nil, "reload", VerdictDeliver, ReasonLegacy},
		{"legacy client gets reports", nil, "message", VerdictDeliver, ReasonLegacy},
		{"declared + accepted command", declared, "navigate", VerdictDeliver, ReasonAccepted},
		{"declared + unaccepted command suppresses", declared, "reload", VerdictSuppress, ReasonNotAccepted},
		{"declared client still gets reports", declared, "message", VerdictDeliver, ReasonUngated},
		{"declared client still gets lifecycle", declared, "connected", VerdictDeliver, ReasonUngated},
		{"unknown event delivers to declared client", declared, "some_future_event", VerdictDeliver, ReasonUngated},
	}
	for _, c := range cases {
		got := Decide(c.decl, c.event)
		if got.Verdict != c.verdict || got.Reason != c.reason {
			t.Errorf("%s: Decide → (%v, %v), want (%v, %v)", c.name, got.Verdict, got.Reason, c.verdict, c.reason)
		}
		if got.Event != c.event {
			t.Errorf("%s: decision echoes event %q, want %q", c.name, got.Event, c.event)
		}
	}
}

// The subtraction invariant: across every event name in every class, a
// declaration never causes a delivery the legacy client would not get —
// it can only remove them.
func TestDeclarationOnlySubtracts(t *testing.T) {
	declared := declare(t, "navigate")
	events := append(PresentationCommands(), "connected", "message", "history", "unheard_of")
	for _, ev := range events {
		legacy := Decide(nil, ev)
		if legacy.Verdict != VerdictDeliver {
			t.Fatalf("legacy client suppressed on %q — grandfathering broken", ev)
		}
		got := Decide(declared, ev)
		if got.Verdict == VerdictDeliver && legacy.Verdict != VerdictDeliver {
			t.Fatalf("declaration added a delivery on %q", ev)
		}
	}
}

func TestRecognize(t *testing.T) {
	d := declare(t, "navigate", "draft", "teleport", "hologram")
	recognized, unknown := d.Recognize()
	if want := []string{"draft", "navigate"}; !reflect.DeepEqual(recognized, want) {
		t.Errorf("recognized = %v, want %v", recognized, want)
	}
	if want := []string{"hologram", "teleport"}; !reflect.DeepEqual(unknown, want) {
		t.Errorf("unknown = %v, want %v", unknown, want)
	}

	// Empty accepts: both splits present and empty, never nil — the
	// connected-echo must serialize as [] not null.
	recognized, unknown = declare(t).Recognize()
	if recognized == nil || unknown == nil || len(recognized)+len(unknown) != 0 {
		t.Errorf("empty declaration Recognize = (%v, %v), want ([], [])", recognized, unknown)
	}
}
