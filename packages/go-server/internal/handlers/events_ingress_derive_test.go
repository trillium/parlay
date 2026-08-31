package handlers

import (
	"strings"
	"testing"

	"parlay/go-server/internal/sourcecontracts"
)

// TestDerivedAllowlistIsExactlyToolEvent pins the byte-identical property this
// change rides on: deriving from the enrolled contracts must reproduce the
// hand-written allowlist it replaced — tool_event, and nothing else.
func TestDerivedAllowlistIsExactlyToolEvent(t *testing.T) {
	if len(ingressEvents) != 1 || !ingressEvents["tool_event"] {
		t.Fatalf("derived ingressEvents = %v, want exactly {tool_event}", ingressEvents)
	}
}

// TestDeriveSkipsNonObservabilityPostures: content and control surfaces have
// their own routes (docs/source-contracts.md §5's closed route table); their
// emits never reach the ingress allowlist even if declared.
func TestDeriveSkipsNonObservabilityPostures(t *testing.T) {
	got := deriveIngressEvents([]sourcecontracts.Declared{
		{Name: "hook-tailer", Trust: "content", Emits: []string{"hook_event"}},
		{Name: "cursorless", Trust: "control", Emits: []string{"cursorless_rpc"}},
	})
	if len(got) != 0 {
		t.Fatalf("non-observability emits leaked into the allowlist: %v", got)
	}
}

// TestDeriveFailsClosedOnEmptyRegistry: a nil Enrolled() (missing or
// unparseable contract tree) must derive an empty allowlist, so every
// producer gets a 400 rather than the seam half-working.
func TestDeriveFailsClosedOnEmptyRegistry(t *testing.T) {
	if got := deriveIngressEvents(nil); len(got) != 0 {
		t.Fatalf("deriveIngressEvents(nil) = %v, want empty", got)
	}
}

// TestDerivePanicsOnForbiddenNames: the refused rosters are a hard boot gate
// — an enrollment emitting a panel-aiming or server-owned name, or the
// not-an-event system_update, must refuse to start the server, not join the
// allowlist.
func TestDerivePanicsOnForbiddenNames(t *testing.T) {
	cases := []struct {
		event string
		want  string
	}{
		{"reload", "panel-aiming"},
		{"navigate", "panel-aiming"},
		{"device_cmd", "panel-aiming"},
		{"input_action", "panel-aiming"},
		{"draft", "panel-aiming"},
		{"tts_event", "panel-aiming"},
		{"pages_patch", "panel-aiming"},
		{"cursorless_rpc", "panel-aiming"},
		{"connected", "server-owned"},
		{"history", "server-owned"},
		{"agents", "server-owned"},
		{"agent_register", "server-owned"},
		{"presence_map", "server-owned"},
		{"message", "server-owned"},
		{"message_received", "server-owned"},
		{"commands", "server-owned"},
		{"command_update", "server-owned"},
		{"system_update", "message type"},
	}
	for _, tc := range cases {
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Errorf("emitting %q did not panic", tc.event)
					return
				}
				msg, ok := r.(string)
				if !ok || !strings.Contains(msg, tc.want) || !strings.Contains(msg, tc.event) {
					t.Errorf("emitting %q panicked with %v, want mention of %q and %q", tc.event, r, tc.event, tc.want)
				}
			}()
			deriveIngressEvents([]sourcecontracts.Declared{
				{Name: "rogue", Trust: "observability", Emits: []string{tc.event}},
			})
		}()
	}
}
