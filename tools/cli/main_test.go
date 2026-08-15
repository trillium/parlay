package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/commandreport"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// reportRecorder is a stub registry server that keeps every report it got.
type reportRecorder struct {
	mu   sync.Mutex
	hits []map[string]any
}

func (rc *reportRecorder) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if payload == nil {
			payload = map[string]any{}
		}
		payload["_path"] = r.URL.Path
		rc.mu.Lock()
		rc.hits = append(rc.hits, payload)
		rc.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("PARLAY_STATE_HOME", t.TempDir())
	t.Setenv("PARLAY_SERVER", srv.URL)

	prev := httpc.Exit
	t.Cleanup(func() { httpc.Exit = prev })
}

func (rc *reportRecorder) end(t *testing.T) map[string]any {
	t.Helper()
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, h := range rc.hits {
		if h["_path"] == "/api/chat/command-end" {
			return h
		}
	}
	return nil
}

// TestPanicIsReportedAsAFailedInvocation pins the one case a plain
// `defer finish(ExitOK)` gets wrong: a command that panics produced no result,
// so its registry record must not read finished/ok/exit 0. The panic itself
// must still reach the runtime, or the stack trace is swallowed.
func TestPanicIsReportedAsAFailedInvocation(t *testing.T) {
	rc := &reportRecorder{}
	rc.start(t)

	finish := commandreport.Begin("doctor", nil)

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		defer reportEnd(finish)
		panic("verb blew up")
	}()

	if recovered != "verb blew up" {
		t.Fatalf("recovered = %v, want the original panic to propagate", recovered)
	}

	end := rc.end(t)
	if end == nil {
		t.Fatalf("a panicking invocation reported no end at all")
	}
	if end["exitCode"] == float64(0) {
		t.Errorf("end payload = %v, want a non-zero exit code", end)
	}
	if end["state"] != "failed" || end["outcome"] != "error" {
		t.Errorf("end payload = %v, want failed/error — a panic must never read green", end)
	}
}

// A normal return still closes the record as a success; the recover path must
// not have changed the ordinary case.
func TestNormalReturnStillReportsSuccess(t *testing.T) {
	rc := &reportRecorder{}
	rc.start(t)

	finish := commandreport.Begin("doctor", nil)
	func() { defer reportEnd(finish) }()

	end := rc.end(t)
	if end == nil {
		t.Fatalf("no end report; hits = %v", rc.hits)
	}
	if end["state"] != "finished" || end["outcome"] != "ok" || end["exitCode"] != float64(0) {
		t.Errorf("end payload = %v, want finished/ok/exit 0", end)
	}
}
