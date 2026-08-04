// Mirrors packages/cli/src/commands-crew-state.test.ts's cases, plus
// dedicated regression coverage for the ticket B5 fidelity fix (see
// status_verb.go's statusFileForAgent doc): crew-state must reconcile the
// TARGET agent's status file, not the caller's own.
package commands

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newCrewStateServer(t *testing.T, registeredIDs ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/subscribers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		agents := ""
		for i, id := range registeredIDs {
			if i > 0 {
				agents += ","
			}
			agents += `{"id":"` + id + `","name":"n","color":"#fff"}`
		}
		w.Write([]byte(`{"registered":{"agents":[` + agents + `]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCrewStateForAgentUnknownWhenNotEnrolled(t *testing.T) {
	srv := newCrewStateServer(t) // no agents registered
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())

	res := CrewStateForAgent("ghost-agent")
	if res.State != "unknown" || res.Source != "none" {
		t.Errorf("CrewStateForAgent() = %+v, want state=unknown source=none", res)
	}
}

func TestCrewStateForAgentUnknownWhenNoStatusRecorded(t *testing.T) {
	srv := newCrewStateServer(t, "agent-a")
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())

	res := CrewStateForAgent("agent-a")
	if res.State != "unknown" || res.Detail != "no status recorded" {
		t.Errorf("CrewStateForAgent() = %+v, want unknown/no status recorded", res)
	}
}

func TestCrewStateForAgentReadsLastStatusLine(t *testing.T) {
	home := t.TempDir()
	srv := newCrewStateServer(t, "agent-b")
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)

	dir := filepath.Join(home, "agent-b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte("working: starting task\ndone: task complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := CrewStateForAgent("agent-b")
	if res.State != "done" || res.Source != "status" || res.Detail != "task complete" {
		t.Errorf("CrewStateForAgent() = %+v, want state=done source=status detail=\"task complete\"", res)
	}
}

func TestCrewStateForAgentUnrecognizedVerb(t *testing.T) {
	home := t.TempDir()
	srv := newCrewStateServer(t, "agent-c")
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)

	dir := filepath.Join(home, "agent-c")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "status"), []byte("frobnicating: whatever\n"), 0o644)

	res := CrewStateForAgent("agent-c")
	if res.State != "unknown" || res.Source != "status" {
		t.Errorf("CrewStateForAgent() = %+v, want unknown/status for an unrecognized verb", res)
	}
}

// Regression for the ticket B5 fidelity fix: crew-state must read the
// TARGET agent's status file, not the caller's own PARLAY_AGENT_ID /
// PARLAY_STATUS_FILE. Before the fix, crew-state("agent-target") would
// silently reconcile "agent-caller"'s file instead.
func TestCrewStateForAgentIgnoresCallersOwnStatusFile(t *testing.T) {
	home := t.TempDir()
	srv := newCrewStateServer(t, "agent-target")
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_AGENT_ID", "agent-caller")
	t.Setenv("PARLAY_STATUS_FILE", "")

	// The caller's OWN status says "blocked" — must NOT leak into the result.
	callerDir := filepath.Join(home, "agent-caller")
	os.MkdirAll(callerDir, 0o755)
	os.WriteFile(filepath.Join(callerDir, "status"), []byte("blocked: caller's own problem\n"), 0o644)

	// The target has never written a status file.
	res := CrewStateForAgent("agent-target")
	if res.State != "unknown" || res.Detail != "no status recorded" {
		t.Errorf("CrewStateForAgent(target) = %+v, want unknown/no status recorded (must not read caller's file)", res)
	}

	// Now give the target its own status; it must be what's reconciled.
	targetDir := filepath.Join(home, "agent-target")
	os.MkdirAll(targetDir, 0o755)
	os.WriteFile(filepath.Join(targetDir, "status"), []byte("needs-decision: pick one\n"), 0o644)
	res = CrewStateForAgent("agent-target")
	if res.State != "needs-decision" || res.Source != "status" || res.Detail != "pick one" {
		t.Errorf("CrewStateForAgent(target, hyphenated verb) = %+v, want state=needs-decision source=status detail=\"pick one\"", res)
	}
}

// Regression for the follow-up fidelity fix to statusLineRe: hyphenated
// verbs must round-trip through CrewStateForAgent, not read back as
// "unknown / no status recorded".
func TestCrewStateForAgentHyphenatedVerbsRoundTrip(t *testing.T) {
	home := t.TempDir()
	srv := newCrewStateServer(t, "agent-hyphen")
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)

	dir := filepath.Join(home, "agent-hyphen")
	os.MkdirAll(dir, 0o755)

	os.WriteFile(filepath.Join(dir, "status"), []byte("needs-decision: pick a path\n"), 0o644)
	res := CrewStateForAgent("agent-hyphen")
	if res.State != "needs-decision" || res.Source != "status" || res.Detail != "pick a path" {
		t.Errorf("CrewStateForAgent() = %+v, want state=needs-decision source=status detail=\"pick a path\"", res)
	}

	os.WriteFile(filepath.Join(dir, "status"), []byte("captain-held: waiting on captain\n"), 0o644)
	res = CrewStateForAgent("agent-hyphen")
	if res.State != "captain-held" || res.Source != "status" || res.Detail != "waiting on captain" {
		t.Errorf("CrewStateForAgent() = %+v, want state=captain-held source=status detail=\"waiting on captain\"", res)
	}
}

func TestParseStatusLineHyphenatedVerbMatches(t *testing.T) {
	// Regression for the follow-up fidelity fix to statusLineRe: hyphenated
	// verbs in the code's own declared vocabulary (TERMINAL_VERBS /
	// ROUTINE_VERBS) must parse, not silently fail.
	p, ok := parseStatusLine("needs-decision: task foo")
	if !ok || p.verb != "needs-decision" || p.note != "task foo" {
		t.Errorf(`parseStatusLine("needs-decision: task foo") = %+v, %v, want verb=needs-decision note="task foo"`, p, ok)
	}
	p, ok = parseStatusLine("captain-held: waiting")
	if !ok || p.verb != "captain-held" || p.note != "waiting" {
		t.Errorf(`parseStatusLine("captain-held: waiting") = %+v, %v, want verb=captain-held note="waiting"`, p, ok)
	}
}

func TestParseStatusLineSimpleVerbs(t *testing.T) {
	p, ok := parseStatusLine("working: on it")
	if !ok || p.verb != "working" || p.note != "on it" {
		t.Errorf("parseStatusLine() = %+v, %v, want verb=working note=\"on it\"", p, ok)
	}

	p, ok = parseStatusLine("blocked [key=deploy]: waiting on ci")
	if !ok || p.verb != "blocked" || p.key != "deploy" || p.note != "waiting on ci" {
		t.Errorf("parseStatusLine(keyed) = %+v, %v, want verb=blocked key=deploy", p, ok)
	}
}
