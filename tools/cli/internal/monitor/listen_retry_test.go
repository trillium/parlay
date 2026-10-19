// robots-wsv8a: `parlay listen`'s enrollment POSTs were one 10s shot each, so
// a transiently busy server failed the whole enrollment — the agent came up
// with no reply channel and missed every steer — with a message that could
// not say whether the server was down or merely slow. Enrollment now retries
// a transport failure under a bounded policy and the verdict names which.
package monitor

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// startStallingListenHarness is startListenHarness with a register-agent
// handler that records the call and then blocks the first stallFirst
// requests until the test ends — the client's per-attempt wait expires
// exactly as it did on the captain's box. The release is a cleanup
// registered AFTER the server's own, so (LIFO) stalled handlers are let go
// before httptest.Server.Close waits on them.
func startStallingListenHarness(t *testing.T, stallFirst int) *listenHarness {
	t.Helper()
	h := &listenHarness{}
	handoffRegisteredAgent = ""
	t.Cleanup(func() { handoffRegisteredAgent = "" })
	release := make(chan struct{})
	mux := http.NewServeMux()
	answer := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": "reply-1"})
	}
	mux.HandleFunc("/api/chat/register-agent", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.mu.Lock()
		h.calls = append(h.calls, recordedCall{path: "/api/chat/register-agent", body: body})
		n := len(h.calls)
		h.mu.Unlock()
		if n <= stallFirst {
			<-release
			return
		}
		answer(w)
	})
	mux.HandleFunc("/api/chat/reply", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.mu.Lock()
		h.calls = append(h.calls, recordedCall{path: "/api/chat/reply", body: body})
		h.mu.Unlock()
		answer(w)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", srv.URL)
	stubPreflight(t, 0)
	origReap := ensureSingleListener
	ensureSingleListener = func(agent string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.reaped = append(h.reaped, agent)
		h.callsAtReap = append(h.callsAtReap, len(h.calls))
	}
	t.Cleanup(func() { ensureSingleListener = origReap })
	return h
}

// quickEnrollRetry shrinks the enrollment policy to unit-test scale: attempts
// wait tens of milliseconds and the pause between them is one millisecond.
func quickEnrollRetry(t *testing.T, attempts int) {
	t.Helper()
	orig := enrollRetry
	enrollRetry = httpc.Retry{Attempts: attempts, PerAttempt: 80 * time.Millisecond, Backoff: time.Millisecond, MaxBackoff: time.Millisecond, Budget: 10 * time.Second}
	t.Cleanup(func() { enrollRetry = orig })
}

// captureListenStderr runs fn with os.Stderr redirected and returns what it
// wrote — the Die message and the retry narration both land there.
func captureListenStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	func() {
		defer func() { os.Stderr = orig }()
		fn()
	}()
	_ = w.Close()
	return <-done
}

func countCalls(h *listenHarness, path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		if c.path == path {
			n++
		}
	}
	return n
}

func TestCmdListenRetriesEnrollmentWhenTheServerIsSlow(t *testing.T) {
	// The captain's incident: the server accepted the connection and did not
	// answer within the attempt's wait, then answered the next one promptly.
	// Enrollment must complete — registered, announced, monitor armed — with
	// the stall narrated rather than fatal.
	h := startStallingListenHarness(t, 1)
	monitorCalls := stubMonitor(t)
	trapExit(t)
	quickEnrollRetry(t, 3)

	msg := captureListenStderr(t, func() {
		CmdListen([]string{"--agent", "talon-phone"})
	})

	if n := countCalls(h, "/api/chat/register-agent"); n != 2 {
		t.Errorf("register-agent attempts = %d, want 2 (one stalled, one answered)", n)
	}
	if n := countCalls(h, "/api/chat/reply"); n != 1 {
		t.Errorf("announce calls = %d, want 1", n)
	}
	if len(*monitorCalls) != 1 || (*monitorCalls)[0][1] != "talon-phone" {
		t.Errorf("monitor calls = %v, want exactly one for talon-phone", *monitorCalls)
	}
	if !claimHandoffRegistration("talon-phone") {
		t.Error("a retried-then-successful registration must still arm the monitor handoff claim")
	}
	if !strings.Contains(msg, "register-agent attempt 1/3") || !strings.Contains(msg, "retrying in") {
		t.Errorf("stderr = %q, want the stalled attempt narrated with its retry", msg)
	}
	if strings.Contains(msg, "Cannot reach Parlay server") {
		t.Errorf("stderr = %q, must not report a failure for an enrollment that succeeded", msg)
	}
}

func TestCmdListenGivesUpWithASlowVerdictWhenTheServerNeverAnswers(t *testing.T) {
	h := startStallingListenHarness(t, 1000)
	monitorCalls := stubMonitor(t)
	trapExit(t)
	quickEnrollRetry(t, 2)

	var code int
	var died bool
	msg := captureListenStderr(t, func() {
		code, died = testsupport.Capture(func() {
			CmdListen([]string{"--agent", "talon-phone"})
		})
	})

	if !died || code != config.ExitRuntime {
		t.Fatalf("died=%v code=%d, want Die with %d once every attempt timed out", died, code, config.ExitRuntime)
	}
	if n := countCalls(h, "/api/chat/register-agent"); n != 2 {
		t.Errorf("register-agent attempts = %d, want exactly Attempts=2 — the sequence is bounded", n)
	}
	if n := countCalls(h, "/api/chat/reply"); n != 0 {
		t.Errorf("announce calls = %d, want 0 — nothing is announced for an agent that never registered", n)
	}
	if len(*monitorCalls) != 0 {
		t.Errorf("monitor must not be armed after a failed enrollment, got %v", *monitorCalls)
	}
	if claimHandoffRegistration("talon-phone") {
		t.Error("a failed registration must not arm the monitor handoff claim")
	}
	if !strings.Contains(msg, "Cannot reach Parlay server at ") || !strings.Contains(msg, "SLOW, not down") || !strings.Contains(msg, "/api/chat/register-agent") {
		t.Errorf("stderr = %q, want the SLOW verdict naming register-agent under the grep-stable prefix", msg)
	}
}

func TestCmdListenGivesUpWithAnUnreachableVerdictWhenNothingListens(t *testing.T) {
	startListenHarness(t)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	monitorCalls := stubMonitor(t)
	trapExit(t)
	quickEnrollRetry(t, 3)

	var code int
	var died bool
	msg := captureListenStderr(t, func() {
		code, died = testsupport.Capture(func() {
			CmdListen([]string{"--agent", "talon-phone"})
		})
	})

	if !died || code != config.ExitRuntime {
		t.Fatalf("died=%v code=%d, want Die with %d on a refused connection", died, code, config.ExitRuntime)
	}
	if !strings.Contains(msg, "UNREACHABLE") || !strings.Contains(msg, "connection refused") || !strings.Contains(msg, "3 attempts over") {
		t.Errorf("stderr = %q, want the UNREACHABLE verdict with the attempt tally", msg)
	}
	if len(*monitorCalls) != 0 {
		t.Errorf("monitor must not be armed when the server is unreachable, got %v", *monitorCalls)
	}
	if claimHandoffRegistration("talon-phone") {
		t.Error("an unreachable server must not arm the monitor handoff claim")
	}
}

// The announce is the second half of the same enrollment: a stall there used
// to leave the agent registered but never armed. It rides the same policy.
func TestCmdListenRetriesTheAnnounceToo(t *testing.T) {
	h := &listenHarness{}
	handoffRegisteredAgent = ""
	t.Cleanup(func() { handoffRegisteredAgent = "" })
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/register-agent", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.calls = append(h.calls, recordedCall{path: "/api/chat/register-agent"})
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/api/chat/reply", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.calls = append(h.calls, recordedCall{path: "/api/chat/reply"})
		n := countCallsLocked(h, "/api/chat/reply")
		h.mu.Unlock()
		if n == 1 {
			<-release
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"id":"reply-2"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", srv.URL)
	stubPreflight(t, 0)
	origReap := ensureSingleListener
	ensureSingleListener = func(string) {}
	t.Cleanup(func() { ensureSingleListener = origReap })
	monitorCalls := stubMonitor(t)
	trapExit(t)
	quickEnrollRetry(t, 3)

	msg := captureListenStderr(t, func() {
		CmdListen([]string{"--agent", "talon-phone"})
	})

	if n := countCalls(h, "/api/chat/register-agent"); n != 1 {
		t.Errorf("register-agent attempts = %d, want 1 — registration answered first time", n)
	}
	if n := countCalls(h, "/api/chat/reply"); n != 2 {
		t.Errorf("announce attempts = %d, want 2 (one stalled, one answered)", n)
	}
	if len(*monitorCalls) != 1 {
		t.Errorf("monitor calls = %v, want exactly one after the announce recovered", *monitorCalls)
	}
	if !strings.Contains(msg, "/api/chat/reply attempt 1/3") {
		t.Errorf("stderr = %q, want the stalled announce narrated", msg)
	}
}

func countCallsLocked(h *listenHarness, path string) int {
	n := 0
	for _, c := range h.calls {
		if c.path == path {
			n++
		}
	}
	return n
}
