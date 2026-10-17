package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireTailnetAuth(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	tok := func() string { return "s3cret" }
	none := func() string { return "" }
	cases := []struct {
		name  string
		tokFn func() string
		peer  string
		hdr   map[string]string
		want  int
	}{
		{"local loopback, no markers", tok, "127.0.0.1:5000", nil, 204},
		{"proxied with tailscale identity", tok, "127.0.0.1:5000", map[string]string{"Tailscale-User-Login": "a@b", "X-Forwarded-For": "100.1.1.1"}, 204},
		{"proxied, no identity, no token", tok, "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "100.1.1.1"}, 401},
		{"proxied, bearer ok", tok, "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "100.1.1.1", "Authorization": "Bearer s3cret"}, 204},
		{"proxied, wrong bearer", tok, "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "100.1.1.1", "Authorization": "Bearer nope"}, 401},
		{"proxied, no server token", none, "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "100.1.1.1"}, 503},
		{"non-loopback peer, no auth", tok, "100.92.102.49:5000", nil, 401},
		{"non-loopback peer forging identity", tok, "100.92.102.49:5000", map[string]string{"Tailscale-User-Login": "a@b"}, 401},
		{"non-loopback peer, bearer ok", tok, "100.92.102.49:5000", map[string]string{"Authorization": "Bearer s3cret"}, 204},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/remote-input/targets", nil)
		req.RemoteAddr = tc.peer
		for k, v := range tc.hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		requireTailnetAuth(tc.tokFn, ok)(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

func TestRequireTailnetAuthTrustOptOut(t *testing.T) {
	t.Setenv(TrustTailscaleHeaderEnv, "0")
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Tailscale-User-Login", "a@b")
	rec := httptest.NewRecorder()
	requireTailnetAuth(func() string { return "s" }, func(http.ResponseWriter, *http.Request) {})(rec, req)
	if rec.Code != 401 {
		t.Fatalf("identity header must not pass when trust is disabled: got %d", rec.Code)
	}
}
