package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The relay is a per-user singleton on a host-wide runtime dir bound to ONE
// -server for its whole life, so a second parlay instance pointed at another
// chat server shares this process. /health is the only place the monitor can
// learn that, which is why these two properties are pinned here rather than
// left to the bash side that consumes them.
func TestHealthNamesTheServerAndRuntimeItIsBoundTo(t *testing.T) {
	r := &relay{server: "http://127.0.0.1:14999", runtimeDir: "/tmp/parlay-test-runtime"}
	srv := httptest.NewServer(r.controlMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true {
		t.Errorf(`ok = %v, want true — ensure-up greps '"ok":true' to decide the relay is live`, body["ok"])
	}
	if got := body["server"]; got != r.server {
		t.Errorf("server = %v, want %q — the monitor compares this against the CLI's resolved server to refuse a cross-instance enroll", got, r.server)
	}
	if got := body["runtime"]; got != r.runtimeDir {
		t.Errorf("runtime = %v, want %q", got, r.runtimeDir)
	}
}

// ensure-up decides liveness with `curl … | grep -q '"ok":true'`
// (tools/relay/deploy/lib.sh), and Go's json encoder emits map keys in sorted
// order — so `ok` sorts first and the grep cannot be broken by adding fields.
// If that ever stops being true (a struct with a field order, or a hand-rolled
// writer), the whole relay autostart silently no-ops.
func TestHealthKeepsOkFirstForTheEnsureUpGrep(t *testing.T) {
	r := &relay{server: "http://localhost:4242", runtimeDir: "/tmp/parlay"}
	srv := httptest.NewServer(r.controlMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	raw := make([]byte, 512)
	n, _ := resp.Body.Read(raw)
	got := string(raw[:n])

	if !strings.Contains(got, `"ok":true`) {
		t.Fatalf("body = %q, does not contain the literal ensure-up greps for", got)
	}
	if i := strings.Index(got, `"ok"`); i != 1 { // `{"ok":true,...`
		t.Errorf(`"ok" at offset %d, want 1 (first key) — lib.sh's grep is '"ok":true' and must keep matching`, i)
	}
}
