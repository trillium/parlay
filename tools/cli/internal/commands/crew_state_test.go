// Mirrors packages/cli/src/commands-crew-state.test.ts's cases, plus
// dedicated regression coverage for the ticket B5 fidelity fix (see
// status_verb.go's statusFileForAgent doc): crew-state must reconcile the
// TARGET agent's status file, not the caller's own — and for robots-me7m: a
// FAILED relay lookup must never be reported as "not enrolled", and a
// stale-but-valid status file must never be discarded for "unknown".
package commands

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// noRetrySleep makes the relay-lookup backoff free in tests.
func noRetrySleep(t *testing.T) {
	t.Helper()
	orig := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { sleep = orig })
}

// writeStatus (<home>/<agent>/status) lives in supervise_test.go — same
// package, same fixture shape.

func TestCrewStateForAgentUnknownWhenNotEnrolled(t *testing.T) {
	noRetrySleep(t)
	srv := newCrewStateServer(t) // no agents registered
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())

	res := CrewStateForAgent("ghost-agent")
	if res.State != "unknown" || res.Source != "none" {
		t.Errorf("CrewStateForAgent() = %+v, want state=unknown source=none", res)
	}
	if res.Detail != "agent not registered with relay" || res.ExitCode != ExitCrewNotEnrolled {
		t.Errorf("CrewStateForAgent() = %+v, want the authoritative not-registered detail + exit %d", res, ExitCrewNotEnrolled)
	}
}

func TestCrewStateForAgentUnknownWhenNoStatusRecorded(t *testing.T) {
	noRetrySleep(t)
	srv := newCrewStateServer(t, "agent-a")
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())

	res := CrewStateForAgent("agent-a")
	if res.State != "unknown" || res.Detail != "no status recorded" {
		t.Errorf("CrewStateForAgent() = %+v, want unknown/no status recorded", res)
	}
	// "no news" must be distinguishable from "gone" without string-matching.
	if res.ExitCode != ExitCrewNoStatus {
		t.Errorf("CrewStateForAgent() exit = %d, want %d (no status ≠ not enrolled)", res.ExitCode, ExitCrewNoStatus)
	}
}

// THE robots-me7m regression: a failed relay lookup must never be reported
// as "agent not enrolled with relay". The agent is live and its status file
// is valid; the relay is simply unreachable, so the durable on-disk record
// is the answer — never "unknown".
func TestCrewStateFallsBackToStatusFileWhenRelayLookupFails(t *testing.T) {
	noRetrySleep(t)
	home := t.TempDir()
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1") // nothing listening
	t.Setenv("PARLAY_AGENT_HOME", home)
	writeStatus(t, home, "leg3", "working: reading fm-send.sh to add parlay send path\n")

	res := CrewStateForAgent("leg3")
	if res.State != "working" {
		t.Fatalf("CrewStateForAgent() = %+v, want state=working from the on-disk status file", res)
	}
	if res.Source != "status-degraded" {
		t.Errorf("source = %q, want status-degraded (relay unreachable, status is the fallback)", res.Source)
	}
	if !strings.Contains(res.Detail, "reading fm-send.sh") || !strings.Contains(res.Detail, "relay unreachable") {
		t.Errorf("detail = %q, want the status note plus a staleness caveat", res.Detail)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0 — a state WAS determined", res.ExitCode)
	}
	if strings.Contains(res.Detail, "not registered") || strings.Contains(res.Detail, "not enrolled") {
		t.Errorf("detail = %q must not claim the agent is unenrolled — the relay never answered", res.Detail)
	}
}

// A failed lookup with nothing on disk is the one case crew-state has no
// opinion — and it must say so distinctly from "gone".
func TestCrewStateRelayUnreachableAndNoStatus(t *testing.T) {
	noRetrySleep(t)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())

	res := CrewStateForAgent("nobody")
	if res.State != "unknown" || res.Source != "none" {
		t.Errorf("CrewStateForAgent() = %+v, want unknown/none", res)
	}
	if res.Detail != "relay unreachable and no status recorded" || res.ExitCode != ExitCrewRelayUnreachable {
		t.Errorf("CrewStateForAgent() = %+v, want the relay-unreachable detail + exit %d", res, ExitCrewRelayUnreachable)
	}
}

// The observed failure was transient and cleared on retry, so a single
// hiccup must not degrade the answer at all.
func TestCrewStateRetriesTransientRelayFailure(t *testing.T) {
	noRetrySleep(t)
	home := t.TempDir()
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/subscribers", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"registered":{"agents":[{"id":"leg3","name":"n","color":"#fff"}]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	writeStatus(t, home, "leg3", "working: still going\n")

	res := CrewStateForAgent("leg3")
	if res.State != "working" || res.Source != "status" || res.Detail != "still going" || res.ExitCode != 0 {
		t.Errorf("CrewStateForAgent() = %+v, want a clean working/status result after the retry", res)
	}
	if calls < 2 {
		t.Errorf("subscribers called %d time(s), want a retry after the 503", calls)
	}
}

// An authoritative "not registered" still must not throw away a valid
// status line — the last known state is reported, with the source and exit
// code carrying the "gone" signal.
func TestCrewStateKeepsLastStatusWhenRelaySaysUnenrolled(t *testing.T) {
	noRetrySleep(t)
	home := t.TempDir()
	srv := newCrewStateServer(t) // relay answers: nobody registered
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	writeStatus(t, home, "retired", "done: shipped it\n")

	res := CrewStateForAgent("retired")
	if res.State != "done" {
		t.Errorf("CrewStateForAgent() = %+v, want the recorded terminal verb, not unknown", res)
	}
	if res.Source != "status-unenrolled" || res.ExitCode != ExitCrewNotEnrolled {
		t.Errorf("CrewStateForAgent() = %+v, want source=status-unenrolled + exit %d", res, ExitCrewNotEnrolled)
	}
}

// "status unreadable" is its own condition, distinct from "nothing
// recorded" — a supervisor must not read a corrupt file as "no news".
func TestCrewStateUnparseableStatusLine(t *testing.T) {
	noRetrySleep(t)
	home := t.TempDir()
	srv := newCrewStateServer(t, "agent-garbled")
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	writeStatus(t, home, "agent-garbled", "this line has no verb colon grammar\n")

	res := CrewStateForAgent("agent-garbled")
	if res.State != "unknown" || res.Source != "status" {
		t.Errorf("CrewStateForAgent() = %+v, want unknown/status", res)
	}
	if !strings.Contains(res.Detail, "unparseable") || res.ExitCode != ExitCrewStatusUnreadable {
		t.Errorf("CrewStateForAgent() = %+v, want an unparseable detail + exit %d", res, ExitCrewStatusUnreadable)
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

// enrollmentOf is the pure seam sweep's batched registry fetch goes through
// (robots-8783); the ok flag carries the robots-me7m distinction.
func TestEnrollmentOf(t *testing.T) {
	reg := map[string]bool{"agent-a": true}
	if got := enrollmentOf(reg, true, "agent-a"); got != enrolledYes {
		t.Errorf("enrollmentOf(present) = %v, want enrolledYes", got)
	}
	if got := enrollmentOf(reg, true, "ghost"); got != enrolledNo {
		t.Errorf("enrollmentOf(absent) = %v, want enrolledNo", got)
	}
	if got := enrollmentOf(map[string]bool{}, true, "ghost"); got != enrolledNo {
		t.Errorf("enrollmentOf(empty registry, real answer) = %v, want enrolledNo", got)
	}
	if got := enrollmentOf(nil, false, "agent-a"); got != enrollmentUnknown {
		t.Errorf("enrollmentOf(failed fetch) = %v, want enrollmentUnknown — never enrolledNo", got)
	}
}
