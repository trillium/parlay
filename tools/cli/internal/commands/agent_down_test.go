// Mirrors packages/cli/src/commands-agent-down.test.ts's cases. Drives
// against a throwaway httptest.Server that mimics the real
// /api/chat/unregister fail-loud contract (ok on known id, 404+{error} on
// unknown).
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

func newAgentDownServer(t *testing.T, known map[string]bool, calls *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/unregister", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := strings.TrimSpace(body.ID)
		*calls = append(*calls, id)
		w.Header().Set("Content-Type", "application/json")
		if !known[id] {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "unknown channel: " + id})
			return
		}
		delete(known, id)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAgentDownDeregistersKnownAgent(t *testing.T) {
	var calls []string
	srv := newAgentDownServer(t, map[string]bool{"known-agent": true}, &calls)
	t.Setenv("PARLAY_SERVER", srv.URL)

	var exited bool
	out := captureStdout(t, func() {
		_, exited = withExitTrap(t, func() { AgentDown([]string{"known-agent"}) })
	})
	if exited {
		t.Errorf("AgentDown(known-agent) exited unexpectedly")
	}
	if len(calls) != 1 || calls[0] != "known-agent" {
		t.Errorf("unregister calls = %v, want [known-agent]", calls)
	}
	if !strings.Contains(out, "known-agent") || !strings.Contains(out, "deregistered") {
		t.Errorf("AgentDown output = %q, want a deregistration confirmation", out)
	}
}

func TestAgentDownFailsLoudOnUnknownID(t *testing.T) {
	var calls []string
	srv := newAgentDownServer(t, map[string]bool{}, &calls)
	t.Setenv("PARLAY_SERVER", srv.URL)

	code, exited := withExitTrap(t, func() { AgentDown([]string{"nonexistent-agent"}) })
	if !exited || code != config.ExitRuntime {
		t.Errorf("AgentDown(nonexistent-agent) exit = (%d, %v), want (%d, true)", code, exited, config.ExitRuntime)
	}
	if len(calls) != 1 || calls[0] != "nonexistent-agent" {
		t.Errorf("unregister calls = %v, want [nonexistent-agent]", calls)
	}
}

func TestAgentDownRequiresAgentID(t *testing.T) {
	var calls []string
	srv := newAgentDownServer(t, map[string]bool{}, &calls)
	t.Setenv("PARLAY_SERVER", srv.URL)

	code, exited := withExitTrap(t, func() { AgentDown(nil) })
	if !exited || code != config.ExitUsage {
		t.Errorf("AgentDown(nil) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
	if len(calls) != 0 {
		t.Errorf("unregister calls = %v, want none", calls)
	}
}
