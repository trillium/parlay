package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeOrigin(t *testing.T) {
	good := map[string]string{
		"http://100.64.0.7:8787":          "http://100.64.0.7:8787",
		"http://100.64.0.7:8787/some/p?x": "http://100.64.0.7:8787",
		"Box.Tail1.ts.net:8787":           "http://box.tail1.ts.net:8787",
		"https://App.Example.com/":        "https://app.example.com",
	}
	for in, want := range good {
		got, err := NormalizeOrigin(in)
		if err != nil || got != want {
			t.Errorf("NormalizeOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "*", "null", "ftp://x.example", "http://"} {
		if _, err := NormalizeOrigin(bad); err == nil {
			t.Errorf("NormalizeOrigin(%q) must be refused", bad)
		}
	}
}

func TestEditAdmitListKeepsCommentsAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "allowed-origins")
	if changed, err := editAdmitList(path, "http://a.example", true); err != nil || !changed {
		t.Fatalf("first admit: changed=%v err=%v", changed, err)
	}
	if changed, _ := editAdmitList(path, "http://a.example", true); changed {
		t.Fatal("second admit of the same origin must be a no-op")
	}
	data, _ := os.ReadFile(path)
	os.WriteFile(path, append([]byte("# herdr web\n"), data...), 0o644)
	editAdmitList(path, "http://b.example", true)
	if changed, _ := editAdmitList(path, "http://a.example", false); !changed {
		t.Fatal("revoke of a present origin must change the file")
	}
	if changed, _ := editAdmitList(path, "http://a.example", false); changed {
		t.Fatal("revoke of an absent origin must be a no-op")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "# herdr web\nhttp://b.example\n" {
		t.Fatalf("file = %q", got)
	}
}

// fakeServer scripts the four failure layers independently.
type fakeServer struct {
	refuse          bool // 403 the origin
	noACAO          bool // 200 but no CORS
	silent          bool // headers, then nothing
	preflightRefuse bool
}

func (f fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/client", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"contractVersion":1,"listen":"127.0.0.1:4242","exposure":"loopback","admitted":{"file":[],"env":[]}}`))
	})
	mux.HandleFunc("/api/chat/events", func(w http.ResponseWriter, r *http.Request) {
		if f.refuse {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !f.noACAO {
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		if f.silent {
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
			return
		}
		w.Write([]byte("event: connected\ndata: {}\n\n"))
		w.(http.Flusher).Flush()
	})
	pre := func(w http.ResponseWriter, r *http.Request) {
		if f.preflightRefuse {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.WriteHeader(204)
	}
	mux.HandleFunc("/api/chat/eval", pre)
	mux.HandleFunc("/api/chat/register-agent", pre)
	return mux
}

func TestClientCheckVerdicts(t *testing.T) {
	const origin = "http://100.64.0.7:8787"
	cases := []struct {
		name    string
		srv     fakeServer
		verdict string
		fixHas  string
	}{
		{"ok", fakeServer{}, VerdictOK, ""},
		{"origin refused", fakeServer{refuse: true}, VerdictOriginRefused, "parlay client admit " + origin},
		{"no cors", fakeServer{noACAO: true}, VerdictNoCORS, "parlay client admit"},
		{"stream blocked", fakeServer{silent: true}, VerdictStreamBlocked, "buffering"},
		{"preflight refused", fakeServer{preflightRefuse: true}, VerdictOriginRefused, "admit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := httptest.NewServer(c.srv.handler())
			defer ts.Close()
			res := ClientCheck(context.Background(), &http.Client{Timeout: 6 * time.Second}, ts.URL, origin)
			if res.Verdict != c.verdict {
				t.Fatalf("verdict = %q, want %q\n%s", res.Verdict, c.verdict, res.Render())
			}
			if !strings.Contains(res.Fix, c.fixHas) {
				t.Fatalf("fix %q lacks %q", res.Fix, c.fixHas)
			}
		})
	}
}

func TestClientCheckUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.NewServeMux())
	url := ts.URL
	ts.Close() // nothing listens there any more
	res := ClientCheck(context.Background(), &http.Client{Timeout: 3 * time.Second}, url, "http://x.example:1")
	if res.Verdict != VerdictUnreachable {
		t.Fatalf("verdict = %q\n%s", res.Verdict, res.Render())
	}
	if !strings.Contains(res.Fix, "PARLAY_EXPOSURE") {
		t.Fatalf("fix must point at the exposure choice: %q", res.Fix)
	}
}
