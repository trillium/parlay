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
