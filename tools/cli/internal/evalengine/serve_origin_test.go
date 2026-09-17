package evalengine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Origin gate for POST /eval (phase-2 voice identity, task-9nldh): no Origin
// (server-side callers) passes, same-origin and local hostnames pass, an
// allowlisted tunnel origin passes, and everything else is 403.

func originRequest(origin, host string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/eval", strings.NewReader(`{"streamId":"s1"}`))
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func TestEvalOriginAllowed(t *testing.T) {
	// Not parallel with the allowlist test: mutates process env.
	os.Unsetenv("PARLAY_EVAL_ALLOWED_ORIGINS")
	defer os.Unsetenv("PARLAY_EVAL_ALLOWED_ORIGINS")

	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"no origin passes (go-server relay, curl)", "", "127.0.0.1:4343", true},
		{"same origin passes", "http://127.0.0.1:4343", "127.0.0.1:4343", true},
		{"localhost page passes", "http://localhost:3000", "127.0.0.1:4343", true},
		{"lan page passes", "http://192.168.1.20:3000", "192.168.1.20:4343", true},
		{"lan host to loopback bind passes", "http://192.168.1.20:3000", "127.0.0.1:4343", true},
		{"mdns page passes", "http://macbook.local:3000", "127.0.0.1:4343", true},
		{"public page rejected", "https://evil.example:3000", "127.0.0.1:4343", false},
		{"public page to lan host rejected", "https://evil.example", "192.168.1.20:4343", false},
		{"null origin rejected", "null", "127.0.0.1:4343", false},
		{"non-http scheme rejected", "file://localhost", "127.0.0.1:4343", false},
		{"origin with path rejected", "https://evil.example/x", "127.0.0.1:4343", false},
		{"empty origin value rejected", "://", "127.0.0.1:4343", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := evalOriginAllowed(originRequest(c.origin, c.host)); got != c.want {
				t.Errorf("evalOriginAllowed(origin=%q host=%q) = %v, want %v", c.origin, c.host, got, c.want)
			}
		})
	}
}

func TestEvalOriginAllowlist(t *testing.T) {
	// Not parallel: mutates process env.
	os.Setenv("PARLAY_EVAL_ALLOWED_ORIGINS", "https://page.example, https://tunnel.example:443")
	defer os.Unsetenv("PARLAY_EVAL_ALLOWED_ORIGINS")

	if !evalOriginAllowed(originRequest("https://page.example", "127.0.0.1:4343")) {
		t.Error("allowlisted origin rejected")
	}
	if evalOriginAllowed(originRequest("https://other.example", "127.0.0.1:4343")) {
		t.Error("non-allowlisted public origin allowed")
	}
	// No-Origin callers still pass with a list configured.
	if !evalOriginAllowed(originRequest("", "127.0.0.1:4343")) {
		t.Error("no-origin caller rejected while allowlist configured")
	}

	os.Setenv("PARLAY_EVAL_ALLOWED_ORIGINS", "*")
	if !evalOriginAllowed(originRequest("https://anything.example", "127.0.0.1:4343")) {
		t.Error("'*' escape hatch did not allow a public origin")
	}
}

// testEvalMux builds the real route mux with a live engine and no push URL
// (submit fires drop to the log, as in production without PARLAY_EVAL_PUSH_URL).
func testEvalMux(t *testing.T) *http.ServeMux {
	t.Helper()
	engine := NewEngine()
	return newEvalMux(engine, &PushClient{http: &http.Client{}})
}

func postEval(t *testing.T, mux *http.ServeMux, origin, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/eval", strings.NewReader(`{"streamId":"s1","version":1,"text":"hello","reason":"input"}`))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestEvalHotPathOriginEnforced: cross-origin POSTs are 403 with a JSON
// error; no-Origin (the go-server relay path) and same-origin still eval.
func TestEvalHotPathOriginEnforced(t *testing.T) {
	os.Unsetenv("PARLAY_EVAL_ALLOWED_ORIGINS")
	defer os.Unsetenv("PARLAY_EVAL_ALLOWED_ORIGINS")
	mux := testEvalMux(t)

	rec := postEval(t, mux, "https://evil.example", "127.0.0.1:4343")
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST status = %d, want 403", rec.Code)
	}
	var errBody map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil || errBody["error"] == nil {
		t.Errorf("cross-origin POST body = %q, want JSON {error:...}", rec.Body.String())
	}
	if v := rec.Header().Get("Vary"); !strings.Contains(v, "Origin") {
		t.Errorf("403 missing Vary: Origin (got %q)", v)
	}

	rec = postEval(t, mux, "", "127.0.0.1:4343")
	if rec.Code != http.StatusOK {
		t.Errorf("no-origin POST status = %d, want 200 (go-server relay path)", rec.Code)
	}

	rec = postEval(t, mux, "http://127.0.0.1:4343", "127.0.0.1:4343")
	if rec.Code != http.StatusOK {
		t.Errorf("same-origin POST status = %d, want 200", rec.Code)
	}
}
