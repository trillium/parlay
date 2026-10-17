package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"parlay/go-server/internal/remoteinput"
)

func parseWordsFake() *remoteinput.FakeTalon {
	return &remoteinput.FakeTalon{
		ParseModes: []string{"all", "command"},
		ParseTable: map[string]*remoteinput.MatchedCommand{
			"copy that": {Name: "__copy__that__", Words: []string{"copy", "that"}, ExactWords: 2},
		},
	}
}

func postParseWords(t *testing.T, h http.HandlerFunc, hdr map[string]string, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/remote-input/parse-words", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Code, rec.Body.String()
}

func parseWordsHandler(f *remoteinput.FakeTalon, tok, version string) (http.HandlerFunc, func()) {
	svc := remoteinput.NewService(f, 0, nil)
	// Wrapped exactly as registerRemoteInput wraps it: the one shared gate.
	return requireTailnetAuth(func() string { return tok },
		handleRemoteInputParseWords(svc, func() string { return version })), svc.Stop
}

func TestParseWordsHandlerMatchesAndNormalizes(t *testing.T) {
	f := parseWordsFake()
	h, stop := parseWordsHandler(f, "s3cret", "")
	defer stop()
	code, body := postParseWords(t, h, map[string]string{"Authorization": "Bearer s3cret"},
		`{"words":"Copy that."}`)
	if code != http.StatusOK {
		t.Fatalf("got %d (%s)", code, body)
	}
	var resp remoteinput.ParseWordsResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Outcome != remoteinput.ParseMatched || resp.NoMatch || resp.MatchedCommand == nil ||
		resp.MatchedCommand.Name != "__copy__that__" || resp.Words != "copy that" {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Modes) != 2 || resp.Modes[1] != "command" {
		t.Fatalf("modes = %v", resp.Modes)
	}
	if len(f.Inserts)+len(f.RunActions)+len(f.FocusAppCalls) != 0 {
		t.Fatal("parse-words executed something")
	}
}

// A sleeping Talon must show up in the body, not as a bare noMatch.
func TestParseWordsHandlerAsleepIsNotSilent(t *testing.T) {
	f := parseWordsFake()
	f.ParseModes = []string{"all", "sleep"}
	h, stop := parseWordsHandler(f, "s3cret", "")
	defer stop()
	code, body := postParseWords(t, h, map[string]string{"Authorization": "Bearer s3cret"}, `{"words":"scroll down"}`)
	if code != http.StatusOK {
		t.Fatalf("got %d (%s)", code, body)
	}
	var raw map[string]any
	_ = json.Unmarshal([]byte(body), &raw)
	if raw["outcome"] != "talon_asleep" || raw["asleep"] != true || raw["noMatch"] != true {
		t.Fatalf("body = %s", body)
	}
	modes, _ := raw["modes"].([]any)
	if len(modes) != 2 || modes[1] != "sleep" {
		t.Fatalf("modes = %v", raw["modes"])
	}
}

func TestParseWordsHandlerStaleGrammar(t *testing.T) {
	h, stop := parseWordsHandler(parseWordsFake(), "s3cret", "g2")
	defer stop()
	_, body := postParseWords(t, h, map[string]string{"Authorization": "Bearer s3cret"},
		`{"words":"copy that","commandId":"__copy__that__","grammarVersion":"g1"}`)
	if !strings.Contains(body, `"outcome":"stale_grammar"`) {
		t.Fatalf("body = %s", body)
	}
	_, body = postParseWords(t, h, map[string]string{"Authorization": "Bearer s3cret"},
		`{"words":"copy that","commandId":"__paste__that__","grammarVersion":"g2"}`)
	if !strings.Contains(body, `"outcome":"parse_mismatch"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestParseWordsHandlerAuth(t *testing.T) {
	f := parseWordsFake()
	h, stop := parseWordsHandler(f, "s3cret", "")
	defer stop()
	noTok, stopNoTok := parseWordsHandler(f, "", "")
	defer stopNoTok()
	body := `{"words":"copy that"}`
	// A request through `tailscale serve`: loopback peer plus proxy markers.
	proxied := func(extra map[string]string) map[string]string {
		m := map[string]string{"X-Forwarded-For": "100.1.1.1"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	id := proxied(map[string]string{"Tailscale-User-Login": "me@example.com"})

	for _, auth := range []string{"", "Bearer wrong", "s3cret"} {
		hdr := proxied(nil)
		if auth != "" {
			hdr["Authorization"] = auth
		}
		if c, _ := postParseWords(t, h, hdr, body); c != http.StatusUnauthorized {
			t.Fatalf("auth %q: got %d, want 401", auth, c)
		}
	}
	if c, _ := postParseWords(t, noTok, proxied(nil), body); c != http.StatusServiceUnavailable {
		t.Fatalf("no token configured: got %d, want 503", c)
	}
	if len(f.ParseCalls) != 0 {
		t.Fatalf("unauthorized requests reached Talon: %v", f.ParseCalls)
	}
	if c, _ := postParseWords(t, h, proxied(map[string]string{"Authorization": "Bearer s3cret"}), body); c != http.StatusOK {
		t.Fatalf("bearer: got %d, want 200", c)
	}
	if c, _ := postParseWords(t, h, id, body); c != http.StatusOK {
		t.Fatalf("tailnet identity: got %d, want 200", c)
	}
	if c, _ := postParseWords(t, noTok, id, body); c != http.StatusOK {
		t.Fatalf("tailnet identity, no token configured: got %d, want 200", c)
	}
	// Plain local loopback (the panel, curl on this Mac) passes with no token,
	// like the sibling remote-input routes.
	if c, _ := postParseWords(t, h, nil, body); c != http.StatusOK {
		t.Fatalf("local loopback: got %d, want 200", c)
	}
	t.Setenv(TrustTailscaleHeaderEnv, "0")
	if c, _ := postParseWords(t, h, id, body); c != http.StatusUnauthorized {
		t.Fatalf("identity trust disabled: got %d, want 401", c)
	}
}

func TestParseWordsHandlerRejectsBadInput(t *testing.T) {
	f := parseWordsFake()
	h, stop := parseWordsHandler(f, "s3cret", "")
	defer stop()
	auth := map[string]string{"Authorization": "Bearer s3cret"}
	for name, body := range map[string]string{
		"not json":         `words=copy`,
		"missing words":    `{}`,
		"punctuation only": `{"words":"..."}`,
		"too long":         `{"words":"` + strings.Repeat("a ", remoteinput.MaxParseWordsLen) + `"}`,
	} {
		if c, b := postParseWords(t, h, auth, body); c != http.StatusBadRequest {
			t.Errorf("%s: got %d (%s), want 400", name, c, b)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/chat/remote-input/parse-words", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: got %d, want 405", rec.Code)
	}
	if len(f.ParseCalls) != 0 {
		t.Fatalf("rejected requests reached Talon: %v", f.ParseCalls)
	}
}

func TestParseWordsHandlerTalonDownIs502(t *testing.T) {
	f := parseWordsFake()
	f.ParseErr = remoteinput.ErrFocusTransport
	h, stop := parseWordsHandler(f, "s3cret", "")
	defer stop()
	if c, _ := postParseWords(t, h, map[string]string{"Authorization": "Bearer s3cret"}, `{"words":"copy that"}`); c != http.StatusBadGateway {
		t.Fatalf("got %d, want 502 (an unreachable Talon is not a no-match)", c)
	}
}
