// The Next hint must match the registry state: an empty registry has nobody
// to alert, so it points at enrolling an agent instead (2026-08-31 dogfood
// pass, docs/dogfood/2026-08-31-friction-log.md).
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/wire"
)

func newAgentsServer(t *testing.T, list []wire.AgentInfo) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(list)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAgentsEmptyRegistryHintsEnrollNotAlert(t *testing.T) {
	srv := newAgentsServer(t, []wire.AgentInfo{})
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStdout(t, func() { Agents(nil) })
	if !strings.Contains(out, "0 agents registered.") {
		t.Errorf("output %q missing empty-registry line", out)
	}
	if !strings.Contains(out, "Next: parlay listen --agent") {
		t.Errorf("output %q: empty registry should hint enrollment", out)
	}
	if strings.Contains(out, "parlay alert") {
		t.Errorf("output %q: empty registry hinted alerting nobody", out)
	}
}

func TestAgentsPopulatedRegistryHintsAlert(t *testing.T) {
	srv := newAgentsServer(t, []wire.AgentInfo{{ID: "helm", Name: "Helm", Color: "#6366f1"}})
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStdout(t, func() { Agents(nil) })
	if !strings.Contains(out, "Next: parlay alert --agent <id> <text...>") {
		t.Errorf("output %q: populated registry should keep the alert hint", out)
	}
}
