package httpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

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
