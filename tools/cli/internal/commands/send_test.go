// Send's target-agent parsing is hand-rolled specifically because no
// generic flag parser expresses "any unrecognized --foo token is a value"
// (docs/scope-go-cli.md §5 item 1) — no TS counterpart test file exists,
// but this is exactly the sharp edge the ticket calls out, so it gets
// dedicated coverage here.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

func newSendServer(t *testing.T, bodies *[]map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/send", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*bodies = append(*bodies, body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": "msg-1"})
	})
	mux.HandleFunc("/api/chat/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]string{{"id": "mayor", "name": "Mayor", "color": "#fff"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSendParsesTargetFromDynamicFlag(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServer(t, &bodies)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "")

	out := captureStdout(t, func() { Send([]string{"--mayor", "heads", "up"}) })

	if len(bodies) != 1 {
		t.Fatalf("send calls = %d, want 1", len(bodies))
	}
	if bodies[0]["toAgent"] != "mayor" {
		t.Errorf("toAgent = %v, want mayor", bodies[0]["toAgent"])
	}
	if bodies[0]["text"] != "heads up" {
		t.Errorf("text = %v, want %q", bodies[0]["text"], "heads up")
	}
	if !strings.Contains(out, "sent to mayor") {
		t.Errorf("Send output = %q, want a sent-to-mayor confirmation", out)
	}
}

func TestSendAutoFillsFromPARLAYAgentID(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServer(t, &bodies)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "shepherd")

	captureStdout(t, func() { Send([]string{"--mayor", "done"}) })

	if bodies[0]["from"] != "shepherd" {
		t.Errorf("from = %v, want shepherd (auto-filled from PARLAY_AGENT_ID)", bodies[0]["from"])
	}
}

func TestSendFromFlagOverridesPARLAYAgentID(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServer(t, &bodies)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "shepherd")

	captureStdout(t, func() { Send([]string{"--mayor", "--from", "override", "done"}) })

	if bodies[0]["from"] != "override" {
		t.Errorf("from = %v, want override", bodies[0]["from"])
	}
}

func TestSendNoArgsListsTargetableAgents(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServer(t, &bodies)
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStdout(t, func() { Send(nil) })

	if len(bodies) != 0 {
		t.Errorf("send calls = %d, want 0 (bare send lists agents, sends nothing)", len(bodies))
	}
	if !strings.Contains(out, "send --mayor") {
		t.Errorf("Send(nil) output = %q, want it to list 'send --mayor'", out)
	}
}

func TestSendRequiresTextWhenTargetGiven(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServer(t, &bodies)
	t.Setenv("PARLAY_SERVER", srv.URL)

	code, exited := withExitTrap(t, func() { Send([]string{"--mayor"}) })
	if !exited || code != config.ExitUsage {
		t.Errorf("Send(--mayor) with no text exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
	if len(bodies) != 0 {
		t.Errorf("send calls = %d, want 0", len(bodies))
	}
}
