package httpc

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
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// captureStderr runs fn with os.Stderr redirected and returns everything
// written to it — used to assert on Die()'s exact message text.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	w.Close()
	os.Stderr = orig
	return <-done
}

type pingResponse struct {
	OK bool `json:"ok"`
}

func withServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	testsupport.TempStateHome(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("PARLAY_SERVER", srv.URL)
}

func TestGetJSONDecodesSuccessResponse(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	got := GetJSON[pingResponse]("/api/ping")
	if !got.OK {
		t.Errorf("GetJSON() = %+v, want ok=true", got)
	}
}

func TestGetJSONDiesOnNon2xx(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	})

	oldExit := Exit
	Exit = testsupport.RecordingExit()
	defer func() { Exit = oldExit }()

	code, ok := testsupport.Capture(func() {
		GetJSON[pingResponse]("/api/ping")
	})
	if !ok {
		t.Fatal("expected Die to be called on a 500 response")
	}
	if code != config.ExitRuntime {
		t.Errorf("exit code = %d, want %d", code, config.ExitRuntime)
	}
}

func TestGetJSONDiesOnUnreachableServer(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")

	oldExit := Exit
	Exit = testsupport.RecordingExit()
	defer func() { Exit = oldExit }()

	code, ok := testsupport.Capture(func() {
		GetJSON[pingResponse]("/api/ping")
	})
	if !ok {
		t.Fatal("expected Die to be called for an unreachable server")
	}
	if code != config.ExitRuntime {
		t.Errorf("exit code = %d, want %d", code, config.ExitRuntime)
	}
}

// Regression: resp.Status is already "<code> <text>" (e.g. "404 Not
// Found") — prefixing resp.StatusCode too used to print the code twice
// ("404 404 Not Found"), diverging from http.ts's `${res.status}
// ${res.statusText}`. Caught by the ticket B10 TS-vs-Go parity harness.
func TestGetJSONDieMessageDoesNotDuplicateStatusCode(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})

	oldExit := Exit
	Exit = testsupport.RecordingExit()
	defer func() { Exit = oldExit }()

	msg := captureStderr(t, func() {
		testsupport.Capture(func() {
			GetJSON[pingResponse]("/api/ping")
		})
	})
	if strings.Count(msg, "404") != 1 {
		t.Errorf("Die message = %q, want exactly one occurrence of the status code", msg)
	}
}

func TestPostJSONDieMessageDoesNotDuplicateStatusCode(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})

	oldExit := Exit
	Exit = testsupport.RecordingExit()
	defer func() { Exit = oldExit }()

	msg := captureStderr(t, func() {
		testsupport.Capture(func() {
			PostJSON[pingResponse]("/api/ping", map[string]any{})
		})
	})
	if strings.Count(msg, "404") != 1 {
		t.Errorf("Die message = %q, want exactly one occurrence of the status code", msg)
	}
}

func TestPostJSONSendsBodyAndDecodesResponse(t *testing.T) {
	var gotBody map[string]any
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	got := PostJSON[pingResponse]("/api/echo", map[string]any{"text": "hi"})
	if !got.OK {
		t.Errorf("PostJSON() = %+v, want ok=true", got)
	}
	if gotBody["text"] != "hi" {
		t.Errorf("server saw body %+v, want text=hi", gotBody)
	}
}

// Regression for robots-gxlb: TryPostJSON used the shared, timeout-less
// Client, so a relay that accepted the TCP connection and then never
// answered hung the calling process forever — fatal on the supervision path,
// where `parlay supervise` posts the wake. It now takes an explicit timeout
// like TryGetJSON always has, and must give up on its own.
func TestTryPostJSONGivesUpOnAServerThatNeverAnswers(t *testing.T) {
	release := make(chan struct{})
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Accept the request, then hold it open past the client's timeout.
		<-release
	})
	// Registered AFTER withServer so it runs BEFORE the server's own Close
	// cleanup (t.Cleanup is LIFO) — httptest.Server.Close blocks on
	// outstanding requests, so the handler must be let go first.
	t.Cleanup(func() { close(release) })

	done := make(chan bool, 1)
	go func() {
		ok, _ := TryPostJSON("/api/chat/message", map[string]any{"text": "hi"}, 150*time.Millisecond)
		done <- ok
	}()

	select {
	case ok := <-done:
		if ok {
			t.Error("TryPostJSON() ok = true, want false when the server never answers")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TryPostJSON() never returned — its timeout is not being honored")
	}
}

// The shared one-shot client must stay bounded, and the long-poll client
// must stay unbounded. Both halves matter: an unbounded Client is the
// robots-gxlb hang, and a bounded UnboundedClient would sever every
// `parlay monitor` poll (the server holds one open for 25s).
func TestClientIsBoundedAndOnlyTheLongPollClientIsNot(t *testing.T) {
	if Client.Timeout <= 0 {
		t.Errorf("Client.Timeout = %v, want a positive bound (robots-gxlb)", Client.Timeout)
	}
	if UnboundedClient.Timeout != 0 {
		t.Errorf("UnboundedClient.Timeout = %v, want 0 — the long-poll caller must not be severed", UnboundedClient.Timeout)
	}
}

// ── Error bodies travel into Die messages (register-agent 400 fix) ──────────
// A bare "POST /api/chat/register-agent failed: 400 Bad Request" once cost a
// multi-hour hunt because the server's explanatory body was discarded. The
// body is where the actual reason lives; it rides along, bounded and
// flattened to one line.

func TestPostJSONDieMessageCarriesTheErrorBody(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"agent id is required"}`, http.StatusBadRequest)
	})

	oldExit := Exit
	Exit = testsupport.RecordingExit()
	defer func() { Exit = oldExit }()

	msg := captureStderr(t, func() {
		testsupport.Capture(func() {
			PostJSON[pingResponse]("/api/chat/register-agent", map[string]any{})
		})
	})
	if !strings.Contains(msg, "400 Bad Request") || !strings.Contains(msg, "agent id is required") {
		t.Errorf("Die message = %q, want the status AND the server's stated reason", msg)
	}
}

func TestGetJSONDieMessageCarriesTheErrorBodyBounded(t *testing.T) {
	flood := strings.Repeat("x", 64*1024)
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, flood, http.StatusBadGateway)
	})

	oldExit := Exit
	Exit = testsupport.RecordingExit()
	defer func() { Exit = oldExit }()

	msg := captureStderr(t, func() {
		testsupport.Capture(func() {
			GetJSON[pingResponse]("/api/ping")
		})
	})
	if !strings.Contains(msg, "502") || !strings.Contains(msg, "xxx") {
		t.Fatalf("Die message = %q, want the status plus a body excerpt", msg)
	}
	if len(msg) > errorBodyLimit+128 {
		t.Errorf("Die message is %d bytes; a misbehaving server must not flood stderr (cap %d + prefix)", len(msg), errorBodyLimit)
	}
}

func TestErrorDetailFlattensAndSkipsEmptyBodies(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("line one\n\tline two\n"))}
	if got := errorDetail(resp); got != " — line one line two" {
		t.Errorf("errorDetail(multi-line) = %q, want one flattened line with the separator prefix", got)
	}
	resp = &http.Response{Body: io.NopCloser(strings.NewReader("  \n \t "))}
	if got := errorDetail(resp); got != "" {
		t.Errorf("errorDetail(whitespace-only) = %q, want empty — no dangling separator", got)
	}
}
