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
	return newSendServerWithAgents(t, bodies, []string{"mayor"})
}

// newSendServerWithAgents is newSendServer with an explicit registry, so the
// pre-flight target check in requireRegisteredTarget can be exercised against a
// known agent list. A nil list makes /api/chat/agents fail outright, which is
// the "registry unreadable" path.
func newSendServerWithAgents(t *testing.T, bodies *[]map[string]any, agentIDs []string) *httptest.Server {
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
		if agentIDs == nil {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]string, 0, len(agentIDs))
		for _, id := range agentIDs {
			out = append(out, map[string]string{"id": id, "name": id, "color": "#fff"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
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

// robots-ngg5 regression. `--agent <id>` is the spelling every other parlay
// verb uses (`parlay listen --agent <id>`, the Monitor line `parlay claim`
// prints), so supervisors typed it here too. It used to fall through to the
// "any unrecognized --flag is the target" catch-all and route to a phantom
// channel literally named `agent`, folding the intended recipient into the
// message body — while still returning ok:true and a message id, so the
// caller had no signal the steer was lost.
func TestSendAgentFlagTargetsTheNamedAgentNotAChannelCalledAgent(t *testing.T) {
	for _, flag := range []string{"--agent", "--to"} {
		t.Run(flag, func(t *testing.T) {
			var bodies []map[string]any
			srv := newSendServer(t, &bodies)
			t.Setenv("PARLAY_SERVER", srv.URL)
			t.Setenv("PARLAY_AGENT_ID", "")

			out := captureStdout(t, func() { Send([]string{flag, "mayor", "heads", "up"}) })

			if len(bodies) != 1 {
				t.Fatalf("send calls = %d, want 1", len(bodies))
			}
			if got := bodies[0]["toAgent"]; got != "mayor" {
				t.Errorf("toAgent = %v, want mayor (a channel named %q is the lost-steer bug)", got, got)
			}
			if bodies[0]["text"] != "heads up" {
				t.Errorf("text = %v, want %q — the target id must not leak into the body", bodies[0]["text"], "heads up")
			}
			if !strings.Contains(out, "sent to mayor") {
				t.Errorf("Send output = %q, want a sent-to-mayor confirmation", out)
			}
		})
	}
}

func TestSendAgentFlagCombinesWithFrom(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServer(t, &bodies)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "")

	captureStdout(t, func() { Send([]string{"--agent", "mayor", "--from", "firstmate", "steer"}) })

	if len(bodies) != 1 {
		t.Fatalf("send calls = %d, want 1", len(bodies))
	}
	if bodies[0]["toAgent"] != "mayor" {
		t.Errorf("toAgent = %v, want mayor", bodies[0]["toAgent"])
	}
	if bodies[0]["from"] != "firstmate" {
		t.Errorf("from = %v, want firstmate", bodies[0]["from"])
	}
	if bodies[0]["text"] != "steer" {
		t.Errorf("text = %v, want %q", bodies[0]["text"], "steer")
	}
}

// The other half of robots-ngg5: a send to a channel nobody polls must fail
// loudly instead of returning a message id for an undeliverable message.
func TestSendRefusesUnregisteredTarget(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServerWithAgents(t, &bodies, []string{"mayor", "shepherd"})
	t.Setenv("PARLAY_SERVER", srv.URL)

	code, exited := withExitTrap(t, func() { Send([]string{"--nobody-home", "steer"}) })

	if !exited || code != config.ExitUsage {
		t.Errorf("send to unregistered agent exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
	if len(bodies) != 0 {
		t.Errorf("send calls = %d, want 0 (must not post an undeliverable message)", len(bodies))
	}
}

func TestSendRefusalSuggestsNearMatches(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServerWithAgents(t, &bodies, []string{"mc-robots-ngg5", "mayor"})
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStderr(t, func() {
		withExitTrap(t, func() { Send([]string{"--robots", "steer"}) })
	})

	if !strings.Contains(out, "mc-robots-ngg5") {
		t.Errorf("refusal stderr = %q, want a near-match suggestion naming mc-robots-ngg5", out)
	}
}

func TestSendForceBypassesRegistryCheck(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServerWithAgents(t, &bodies, []string{"mayor"})
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "")

	captureStdout(t, func() { Send([]string{"--not-yet-registered", "--force", "seed"}) })

	if len(bodies) != 1 {
		t.Fatalf("send calls = %d, want 1 (--force skips the registry check)", len(bodies))
	}
	if bodies[0]["toAgent"] != "not-yet-registered" {
		t.Errorf("toAgent = %v, want not-yet-registered", bodies[0]["toAgent"])
	}
	if bodies[0]["text"] != "seed" {
		t.Errorf("text = %v, want %q (--force must not land in the body)", bodies[0]["text"], "seed")
	}
}

// The check fails OPEN: an unreadable registry must not become a new way to
// lose a message. Warn, then send.
func TestSendProceedsWhenRegistryUnreadable(t *testing.T) {
	var bodies []map[string]any
	srv := newSendServerWithAgents(t, &bodies, nil)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "")

	var out string
	stderr := captureStderr(t, func() {
		out = captureStdout(t, func() { Send([]string{"--mayor", "hi"}) })
	})

	if len(bodies) != 1 {
		t.Fatalf("send calls = %d, want 1 (registry failure must not block the send)", len(bodies))
	}
	if !strings.Contains(stderr, "unverified") {
		t.Errorf("stderr = %q, want an unverified-target warning", stderr)
	}
	if !strings.Contains(out, "sent to mayor") {
		t.Errorf("stdout = %q, want the send confirmation", out)
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
