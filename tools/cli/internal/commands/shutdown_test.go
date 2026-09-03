// Exercises `parlay shutdown`'s server round-trip against a throwaway
// httptest.Server mimicking /api/chat/unregister's contract (200+undelivered
// on a known id, 404 on an unknown/already-gone one — see router-messages.ts
// and prune/sweep.ts's UnregisterResult). The local-listener-kill half
// (monitor.KillLocalListeners) is covered directly in the monitor package;
// on a clean test box it finds nothing to kill here, which these tests don't
// need to assert on since they only cover the server-facing contract.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

func newShutdownServer(t *testing.T, known map[string]int, calls *[]string) *httptest.Server {
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
		undelivered, ok := known[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "unknown channel: " + id})
			return
		}
		delete(known, id)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "undelivered": undelivered})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestShutdownDeregistersKnownAgent(t *testing.T) {
	var calls []string
	srv := newShutdownServer(t, map[string]int{"known-agent": 0}, &calls)
	t.Setenv("PARLAY_SERVER", srv.URL)

	var exited bool
	out := captureStdout(t, func() {
		_, exited = withExitTrap(t, func() { Shutdown([]string{"known-agent"}) })
	})
	if exited {
		t.Errorf("Shutdown(known-agent) exited unexpectedly, output: %q", out)
	}
	if len(calls) != 1 || calls[0] != "known-agent" {
		t.Errorf("unregister calls = %v, want [known-agent]", calls)
	}
	if !strings.Contains(out, "known-agent") || !strings.Contains(out, "shut down") {
		t.Errorf("Shutdown output = %q, want a shutdown confirmation", out)
	}
}

func TestShutdownReportsUndeliveredMessages(t *testing.T) {
	var calls []string
	srv := newShutdownServer(t, map[string]int{"chatty-agent": 3}, &calls)
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStdout(t, func() {
		withExitTrap(t, func() { Shutdown([]string{"chatty-agent"}) })
	})
	if !strings.Contains(out, "3") || !strings.Contains(out, "undelivered") {
		t.Errorf("Shutdown output = %q, want it to report 3 undelivered messages", out)
	}
}

// Idempotency: a second shutdown (or a shutdown of an agent that was never
// registered — the retiring-already-dead-agent case) must succeed, not fail
// loud like `parlay agent-down` does on the same 404.
func TestShutdownIsIdempotentOnAnAlreadyRetiredAgent(t *testing.T) {
	var calls []string
	srv := newShutdownServer(t, map[string]int{}, &calls)
	t.Setenv("PARLAY_SERVER", srv.URL)

	code, exited := withExitTrap(t, func() { Shutdown([]string{"already-gone"}) })
	if exited {
		t.Errorf("Shutdown(already-gone) exited (%d), want success on an already-retired id", code)
	}
	if len(calls) != 1 || calls[0] != "already-gone" {
		t.Errorf("unregister calls = %v, want [already-gone]", calls)
	}
}

func TestShutdownRequiresAgentID(t *testing.T) {
	var calls []string
	srv := newShutdownServer(t, map[string]int{}, &calls)
	t.Setenv("PARLAY_SERVER", srv.URL)

	code, exited := withExitTrap(t, func() { Shutdown(nil) })
	if !exited || code != config.ExitUsage {
		t.Errorf("Shutdown(nil) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
	if len(calls) != 0 {
		t.Errorf("unregister calls = %v, want none", calls)
	}
}

func TestShutdownFailsLoudOnAGenuineServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/unregister", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "boom"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("PARLAY_SERVER", srv.URL)

	code, exited := withExitTrap(t, func() { Shutdown([]string{"some-agent"}) })
	if !exited || code != config.ExitRuntime {
		t.Errorf("Shutdown exit on a 500 = (%d, %v), want (%d, true)", code, exited, config.ExitRuntime)
	}
}
