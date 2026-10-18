package guard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rebindReq is the DNS-rebinding request: the page's origin is evil.example,
// the browser resolves it to this machine, and both Host and Origin carry it.
func rebindReq(method, path, origin string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(`{"text":"rebind-pwn","toAgent":"victim","from":"captain"}`))
	r.Host = "evil.example:45871"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestDNSRebindingSendIsRefused(t *testing.T) {
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "")
	t.Setenv("PARLAY_ALLOWED_HOSTS", "")
	w := httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(w, rebindReq(http.MethodPost, "/api/chat/send", "http://evil.example:45871"))
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("rebound POST /send = %d, want 421", w.Code)
	}
}

func TestDNSRebindingReadsAreRefusedWithAndWithoutOrigin(t *testing.T) {
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "")
	t.Setenv("PARLAY_ALLOWED_HOSTS", "")
	for _, path := range []string{"/api/chat/history", "/api/chat/events", "/api/chat/poll", "/"} {
		for _, origin := range []string{"http://evil.example:45871", ""} {
			w := httptest.NewRecorder()
			Wrap(pass()).ServeHTTP(w, rebindReq(http.MethodGet, path, origin))
			if w.Code != http.StatusMisdirectedRequest {
				t.Errorf("rebound GET %s (Origin %q) = %d, want 421", path, origin, w.Code)
			}
		}
	}
}

func TestHostAllowed(t *testing.T) {
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "https://panel.example.net")
	t.Setenv("PARLAY_ALLOWED_HOSTS", "named.example:4242,bare.example")
	cases := []struct {
		host string
		want bool
	}{
		{"", true},
		{"localhost:4242", true},
		{"127.0.0.1:4242", true},
		{"[::1]:4242", true},
		{"192.168.1.42:4242", true},
		{"captain.local:4242", true},
		{"100.101.102.103:4242", true},
		{"mac.tail1234.ts.net:4242", true},
		{"macbook:4242", true},
		{"panel.example.net", true},
		{"named.example:4242", true},
		{"bare.example:9", true},

		{"evil.example:4242", false},
		{"named.example:9", false},
		{"203.0.113.7:4242", false},
		{"localhost.evil.example:4242", false},
		{"x.ts.net.evil.com", false},
	}
	for _, c := range cases {
		if got := HostAllowed(c.host); got != c.want {
			t.Errorf("HostAllowed(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestHostAllowedWildcardAdmitListDisablesTheCheck(t *testing.T) {
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "*")
	if !HostAllowed("evil.example:4242") {
		t.Fatal("wildcard admit list should disable the Host check")
	}
}
