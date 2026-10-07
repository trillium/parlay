package relayctl

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testRuntime makes a SHORT runtime dir and points the two env vars at it.
// Short matters: a unix socket path has a ~104-byte sun_path limit and
// t.TempDir() on macOS is already ~60 bytes before the test name.
func testRuntime(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "prx")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv(RuntimeEnv, dir)
	t.Setenv(SockEnv, "")
	return dir
}

// serveControl starts an HTTP server on <runtime>/relay.sock with the given
// routes, standing in for a live relay. Nothing here writes: the package under
// test is read-only by construction.
func serveControl(t *testing.T, runtime string, routes map[string]http.HandlerFunc) {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range routes {
		mux.HandleFunc(path, h)
	}
	ln, err := net.Listen("unix", filepath.Join(runtime, "relay.sock"))
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

func TestRuntimeDirPrefersEnvAndTrimsSlash(t *testing.T) {
	t.Setenv(RuntimeEnv, "/tmp/somewhere/")
	if got := RuntimeDir(); got != "/tmp/somewhere" {
		t.Errorf("RuntimeDir() = %q, want /tmp/somewhere", got)
	}
}

func TestRuntimeDirFallsBackToTmpdirParlay(t *testing.T) {
	t.Setenv(RuntimeEnv, "")
	t.Setenv("TMPDIR", "/tmp/xyz")
	if got := RuntimeDir(); got != "/tmp/xyz/parlay" {
		t.Errorf("RuntimeDir() = %q, want /tmp/xyz/parlay", got)
	}
}

func TestSockPathHonorsExplicitOverride(t *testing.T) {
	t.Setenv(RuntimeEnv, "/tmp/rt")
	t.Setenv(SockEnv, "/tmp/elsewhere.sock")
	if got := SockPath(); got != "/tmp/elsewhere.sock" {
		t.Errorf("SockPath() = %q", got)
	}
}

func TestReadHealthOverTheSocket(t *testing.T) {
	rt := testRuntime(t)
	serveControl(t, rt, map[string]http.HandlerFunc{
		"/health": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("method = %s, want GET", r.Method)
			}
			writeJSON(t, w, Health{OK: true, Server: "http://a:1", Runtime: rt})
		},
	})
	h, ok := ReadHealth()
	if !ok {
		t.Fatal("ReadHealth ok = false, want true")
	}
	if !h.OK || h.Server != "http://a:1" || h.Runtime != rt {
		t.Errorf("Health = %+v", h)
	}
}

// A live socket that answers 404 is not a live relay: "could not ask" must
// cover a non-2xx answer, because an older relay has no /delivery route and an
// empty trail would be invented from that 404.
func TestReadDeliveryTreats404AsCouldNotAsk(t *testing.T) {
	rt := testRuntime(t)
	serveControl(t, rt, map[string]http.HandlerFunc{
		"/delivery": func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		},
	})
	if d, ok := ReadDelivery(10, "a"); ok {
		t.Fatalf("ok = true with %+v, want false for a 404", d)
	}
}

func TestReadDeliveryPassesLimitAndAgentFilters(t *testing.T) {
	rt := testRuntime(t)
	got := ""
	serveControl(t, rt, map[string]http.HandlerFunc{
		"/delivery": func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.RawQuery
			writeJSON(t, w, Delivery{OK: true, Enabled: true, Exists: true, Count: 0})
		},
	})
	if _, ok := ReadDelivery(7, "crew-1"); !ok {
		t.Fatal("ReadDelivery ok = false")
	}
	if got != "limit=7&agent=crew-1" {
		t.Errorf("query = %q, want limit=7&agent=crew-1", got)
	}
}

func TestReadsFailClosedWithoutASocket(t *testing.T) {
	testRuntime(t) // no server started here
	if _, ok := ReadHealth(); ok {
		t.Error("ReadHealth ok = true with no socket, want false")
	}
	if _, ok := ReadAgents(); ok {
		t.Error("ReadAgents ok = true with no socket, want false")
	}
	if _, ok := ReadDelivery(10, "a"); ok {
		t.Error("ReadDelivery ok = true with no socket, want false")
	}
}

// TestEveryControlReadIsAGet pins the package's defining property: an
// observability surface must not be able to enroll or retire an agent, and the
// relay's socket serves POST /register and POST /unregister right beside the
// routes used here. If a future "convenience" helper reaches for a POST, this
// fails rather than the promise being re-read from the package comment.
func TestEveryControlReadIsAGet(t *testing.T) {
	rt := testRuntime(t)
	var methods []string
	serveControl(t, rt, map[string]http.HandlerFunc{
		"/health":   record(&methods),
		"/agents":   record(&methods),
		"/delivery": record(&methods),
	})
	for _, f := range []func(){
		func() { ReadHealth() },
		func() { ReadAgents() },
		func() { ReadDelivery(5, "crew-1") },
	} {
		f()
	}
	if len(methods) != 3 {
		t.Fatalf("control requests = %v, want exactly 3", methods)
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("control request used %s, want only GET — this client must be unable to mutate the relay", m)
		}
	}
}

func record(sink *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*sink = append(*sink, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}
}

func TestSpoolDistinguishesAbsentFromEmptyFromUnreadable(t *testing.T) {
	rt := testRuntime(t)

	missing := Spool("absent-agent")
	if missing.Exists || missing.Lines != 0 || missing.Err != nil {
		t.Errorf("absent spool = %+v, want Exists=false with no error", missing)
	}

	path := filepath.Join(rt, "empty-agent.chan")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	empty := Spool("empty-agent")
	if !empty.Exists || empty.Lines != 0 || empty.Err != nil {
		t.Errorf("empty spool = %+v, want Exists=true Lines=0", empty)
	}

	if err := os.WriteFile(path, []byte("CHAT_MSG|m-1|user|hi\nCHAT_MSG|m-2|agent|yo\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	two := Spool("empty-agent")
	if !two.Exists || two.Lines != 2 || two.Truncated {
		t.Errorf("two-line spool = %+v, want Lines=2 Truncated=false", two)
	}
}

// The cursor rules are copied from tools/relay/relay_poll.go's lastSpooledID
// because the relay is a separate module. Each rule gets its own case so a
// divergence reddens here rather than silently desynchronizing the CLI's
// answer from the relay's actual resume behaviour.
func TestSpoolCursorMatchesTheRelaysResumeRules(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name, body, want string
	}{
		{"absent", "", ""},
		{"one user message", "CHAT_MSG|m-1|user|hello\n", "m-1"},
		{"last of many wins", "CHAT_MSG|m-1|user|a\nCHAT_MSG|m-2|agent|b\n", "m-2"},
		{"trailing newline is not a line", "CHAT_MSG|m-3|user|a\n\n", "m-3"},
		{"crlf", "CHAT_MSG|m-4|user|a\r\n", "m-4"},
		{"no id is not a cursor", "CHAT_MSG||user|a\n", ""},
		{"non-chat role does not seed", "CHAT_MSG|tts-1|tts_event|x\n", ""},
		{"last chat line wins over a later event", "CHAT_MSG|m-5|user|a\nCHAT_MSG|tts-9|tts_event|x\n", "m-5"},
		{"garbage", "not a spool line\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "case.chan")
			if tc.body == "" {
				os.Remove(path)
			} else if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := SpoolCursor(path); got != tc.want {
				t.Errorf("SpoolCursor(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// A window larger than the read window must still resolve from the tail: the
// relay never scans the whole spool and neither may this.
func TestSpoolCursorFindsTheTailOfALargeSpool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.chan")
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("CHAT_MSG|pad-")
		b.WriteString(strings.Repeat("x", 20))
		b.WriteString("|user|filler line\n")
	}
	b.WriteString("CHAT_MSG|the-last-one|agent|done\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := SpoolCursor(path); got != "the-last-one" {
		t.Errorf("SpoolCursor = %q, want the-last-one", got)
	}
}
