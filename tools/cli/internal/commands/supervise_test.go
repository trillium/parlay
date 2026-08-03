// Mirrors packages/cli/src/commands-supervise.test.ts's cases (which mostly
// re-assert the verb-set constants and file-format assumptions rather than
// exercising cmdSupervise end-to-end) plus integration coverage of Supervise
// itself: terminal-verb wake, routine-verb absorption, and unattended-mode
// enqueue.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSuperviseServer(t *testing.T, bodies *[]map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*bodies = append(*bodies, body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeStatus(t *testing.T, home, agentID, content string) {
	t.Helper()
	dir := filepath.Join(home, agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSuperviseWakesOnTerminalVerb(t *testing.T) {
	var bodies []map[string]any
	srv := newSuperviseServer(t, &bodies)
	home := t.TempDir()
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	// Supervise reads its status file via statusSink() — here it IS the
	// target agent's own env, the common case (a self-supervising loop).
	t.Setenv("PARLAY_AGENT_ID", "agent-s")
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_UNATTENDED_FLAG", "")

	writeStatus(t, home, "agent-s", "working: on it\ndone: task complete\n")

	out := captureStdout(t, func() { Supervise([]string{"agent-s"}) })
	if !strings.Contains(out, "supervisor woken") || !strings.Contains(out, "done") {
		t.Errorf("Supervise() output = %q, want a wake confirmation for done", out)
	}
	if len(bodies) != 1 {
		t.Fatalf("relay posts = %d, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0]["text"].(string), "is done") {
		t.Errorf("posted text = %v, want it to mention 'is done'", bodies[0]["text"])
	}
}

func TestSuperviseAbsorbsRoutineVerb(t *testing.T) {
	var bodies []map[string]any
	srv := newSuperviseServer(t, &bodies)
	home := t.TempDir()
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_AGENT_ID", "agent-t")
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_UNATTENDED_FLAG", "")

	writeStatus(t, home, "agent-t", "working: still on it\n")

	// stderr message, not stdout; just assert no relay post happened.
	captureStdout(t, func() { Supervise([]string{"agent-t"}) })
	if len(bodies) != 0 {
		t.Errorf("relay posts = %d, want 0 (routine verb absorbed)", len(bodies))
	}
}

func TestSuperviseDoesNotRewakeOnSameTerminalLine(t *testing.T) {
	var bodies []map[string]any
	srv := newSuperviseServer(t, &bodies)
	home := t.TempDir()
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_AGENT_ID", "agent-u")
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_UNATTENDED_FLAG", "")

	writeStatus(t, home, "agent-u", "done: finished\n")

	captureStdout(t, func() { Supervise([]string{"agent-u"}) })
	if len(bodies) != 1 {
		t.Fatalf("relay posts after first run = %d, want 1", len(bodies))
	}

	captureStdout(t, func() { Supervise([]string{"agent-u"}) })
	if len(bodies) != 1 {
		t.Errorf("relay posts after second run = %d, want still 1 (marker suppresses re-wake)", len(bodies))
	}
}

func TestSuperviseUnattendedModeEnqueuesInsteadOfPosting(t *testing.T) {
	var bodies []map[string]any
	srv := newSuperviseServer(t, &bodies)
	home := t.TempDir()
	flagFile := filepath.Join(t.TempDir(), "away")
	if err := os.WriteFile(flagFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_AGENT_ID", "agent-v")
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_UNATTENDED_FLAG", flagFile)

	writeStatus(t, home, "agent-v", "blocked: waiting on ci\n")

	captureStdout(t, func() { Supervise([]string{"agent-v"}) })
	if len(bodies) != 0 {
		t.Errorf("relay posts = %d, want 0 in unattended mode (event queued, not posted)", len(bodies))
	}

	queued := ReadUnattendedQueue("agent-v")
	if len(queued) != 1 || queued[0].Verb != "blocked" {
		t.Errorf("unattended queue = %+v, want one entry with verb=blocked", queued)
	}
}

func TestSuperviseDrainDeliversAndClearsQueue(t *testing.T) {
	var bodies []map[string]any
	srv := newSuperviseServer(t, &bodies)
	home := t.TempDir()
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_AGENT_ID", "agent-w")
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_UNATTENDED_FLAG", "")

	EnqueueUnattended("agent-w", "done", "finished while away")

	out := captureStdout(t, func() { Supervise([]string{"agent-w", "--drain"}) })
	if !strings.Contains(out, "drained 1 buffered event") {
		t.Errorf("Supervise(--drain) output = %q, want a drained-1 confirmation", out)
	}
	if len(bodies) != 1 {
		t.Fatalf("relay posts = %d, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0]["text"].(string), "away-mode digest") {
		t.Errorf("posted text = %v, want an away-mode digest", bodies[0]["text"])
	}
	if got := ReadUnattendedQueue("agent-w"); len(got) != 0 {
		t.Errorf("queue after drain = %v, want empty", got)
	}
}

func TestSuperviseDrainWithNoBufferedEvents(t *testing.T) {
	srv := newSuperviseServer(t, &[]map[string]any{})
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	t.Setenv("PARLAY_AGENT_ID", "agent-x")
	t.Setenv("PARLAY_STATUS_FILE", "")

	out := captureStdout(t, func() { Supervise([]string{"agent-x", "--drain"}) })
	if !strings.Contains(out, "no buffered events") {
		t.Errorf("Supervise(--drain) with empty queue = %q, want a no-buffered-events message", out)
	}
}

func TestSuperviseRequiresAgentID(t *testing.T) {
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	code, exited := withExitTrap(t, func() { Supervise(nil) })
	if !exited || code != 2 {
		t.Errorf("Supervise(nil) exit = (%d, %v), want (2, true)", code, exited)
	}
}

func TestHashLineIsShortAndStable(t *testing.T) {
	a := hashLine("done: task complete")
	b := hashLine("done: task complete")
	if a != b {
		t.Errorf("hashLine() not stable: %q != %q", a, b)
	}
	if len(a) > 8 {
		t.Errorf("hashLine() = %q, want at most 8 chars", a)
	}
	if hashLine("something else") == a {
		t.Error("hashLine() collided for different input (may be a coincidence, but check)")
	}
}
