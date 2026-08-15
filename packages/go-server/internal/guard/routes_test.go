package guard_test

// Route-level proof for task-6ai1 / defect D7, run against the REAL mux this
// server serves — the same handlers.Register/RegisterData wiring
// cmd/parlay-server/main.go builds — behind a real httptest listener on a
// random loopback port. It never touches ~/.parlay (the store is rooted at
// t.TempDir()) or ports 31337/4242.
//
// Every `403` below is a line that read `200` before this ticket: the
// verification report drove exactly these requests cross-origin against this
// server and got them all accepted.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/guard"
	"parlay/go-server/internal/handlers"
	"parlay/go-server/internal/store"
)

const evil = "https://evil.example.com"

// newServer starts the production wiring — mux + guard.Wrap — on a scratch
// port with a scratch state dir, and returns its base URL.
func newServer(t *testing.T) string {
	t.Helper()
	st, err := store.Open(store.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Messages.Close() })

	mux := http.NewServeMux()
	handlers.Register(mux, st)
	handlers.RegisterData(mux, st)

	srv := httptest.NewServer(guard.Wrap(mux))
	t.Cleanup(srv.Close)
	return srv.URL
}

// do sends one request. origin == "" means "send no Origin header at all" —
// the CLI/curl shape.
func do(t *testing.T, method, url, origin, contentType, body string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// doHost is do with an explicit arrival Host — what a reverse proxy or tunnel
// forwards. Only the header changes; the request is still dialled at url.
func doHost(t *testing.T, method, url, origin, host, contentType, body string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

// TestD7CrossOriginWritesAreRefused is the regression for the four routes the
// verifier actually exploited. It sends a CORS *simple request*
// (Content-Type: text/plain, no preflight) — the shape a hostile page can
// send with no cooperation from this server at all — and asserts each one is
// now refused before it reaches a handler.
func TestD7CrossOriginWritesAreRefused(t *testing.T) {
	base := newServer(t)

	// A live agent for the attack to aim at, registered the legitimate way —
	// otherwise the /unregister case would 404 on an unknown id and prove
	// nothing about the guard.
	if r := do(t, http.MethodPost, base+"/api/chat/register-agent", "", "application/json", `{"id":"victim","name":"Victim"}`); r.StatusCode != http.StatusOK {
		t.Fatalf("setup register: status = %d", r.StatusCode)
	}

	for _, c := range []struct{ name, path, body string }{
		{"POST /send injected text into a live agent's stream", "/api/chat/send", `{"text":"pwned"}`},
		{"POST /alert reached the whole fleet", "/api/chat/alert", `{"text":"pwned"}`},
		{"POST /register-agent created evil-agent", "/api/chat/register-agent", `{"id":"evil-agent","name":"evil"}`},
		{"POST /unregister removed a live agent", "/api/chat/unregister", `{"id":"victim"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := do(t, http.MethodPost, base+c.path, evil, "text/plain", c.body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (was 200 before this fix)", resp.StatusCode)
			}
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
				t.Fatalf("Access-Control-Allow-Origin = %q, want none on a refusal", got)
			}
		})
	}

	// The refusals must be real, not cosmetic: nothing was written.
	agents, _ := io.ReadAll(do(t, http.MethodGet, base+"/api/chat/agents", "", "", "").Body)
	if strings.Contains(string(agents), "evil-agent") {
		t.Fatalf("the cross-origin register landed: %s", agents)
	}
	if !strings.Contains(string(agents), "victim") {
		t.Fatalf("the cross-origin unregister removed a live agent: %s", agents)
	}
	hist := do(t, http.MethodGet, base+"/api/chat/history", "", "", "")
	body, _ := io.ReadAll(hist.Body)
	if strings.Contains(string(body), "pwned") {
		t.Fatalf("history contains the attacker's text: %s", body)
	}
}

// TestD7JSONContentTypeAlsoRefusedCrossOrigin — a hostile page that bothers
// to set application/json triggers a preflight, which the guard refuses; the
// direct request is refused too, so neither half of that path works.
func TestD7JSONContentTypeAlsoRefusedCrossOrigin(t *testing.T) {
	base := newServer(t)

	resp := do(t, http.MethodPost, base+"/api/chat/send", evil, "application/json", `{"text":"pwned"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin JSON POST: status = %d, want 403", resp.StatusCode)
	}

	pre, err := http.NewRequest(http.MethodOptions, base+"/api/chat/send", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	pre.Header.Set("Origin", evil)
	pre.Header.Set("Access-Control-Request-Method", "POST")
	pre.Header.Set("Access-Control-Request-Headers", "content-type")
	pr, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	defer pr.Body.Close()
	if pr.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin preflight: status = %d, want 403", pr.StatusCode)
	}
	if got := pr.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("refused preflight carried Access-Control-Allow-Origin = %q", got)
	}
}

// TestD7TheRestOfTheWriteSurfaceIsRefused covers the mutating routes the
// report did not name but that sit on the same unauthenticated surface.
func TestD7TheRestOfTheWriteSurfaceIsRefused(t *testing.T) {
	base := newServer(t)

	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/chat/reply", `{"text":"pwned","agent":"victim"}`},
		{http.MethodPost, "/api/chat/message", `{"channel":"victim","text":"pwned"}`},
		{http.MethodPut, "/api/chat/draft", `{"text":"pwned"}`},
		{http.MethodPut, "/api/chat/parlay/settings", `{"textScale":999}`},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			resp := do(t, c.method, base+c.path, evil, "text/plain", c.body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
		})
	}

	// And the draft the panel would submit as the captain was not overwritten.
	got := decode(t, do(t, http.MethodGet, base+"/api/chat/draft", "", "", ""))
	if got["text"] == "pwned" {
		t.Fatal("the cross-origin draft write landed")
	}
}

// TestD9SubscribersNoLongerLeaksIdentifiers — the TS-side chain started here:
// /subscribers handed a foreign origin the connected device uuid and every
// registered agent id, which is what made the rest of the chain aimable.
// Guarding it is the same mechanism, and costs its real callers nothing
// because every one of them (parlay doctor, crew-state, the Go CLI, the
// split-test probe) is a no-Origin HTTP client.
func TestD9SubscribersNoLongerLeaksIdentifiers(t *testing.T) {
	base := newServer(t)

	// Register an agent so there is something to leak.
	if r := do(t, http.MethodPost, base+"/api/chat/register-agent", "", "application/json", `{"id":"victim","name":"Victim"}`); r.StatusCode != http.StatusOK {
		t.Fatalf("setup register: status = %d", r.StatusCode)
	}

	resp := do(t, http.MethodGet, base+"/api/chat/subscribers", evil, "", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want none", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "victim") {
		t.Fatalf("the refusal body still discloses an agent id: %s", body)
	}
}

// TestATunnelledPanelStillWorks isolates OriginAllowed's same-host comparison
// end-to-end. `panel` in TestTheCLIAndThePanelStillWork is http://127.0.0.1:
// <port>, which is also a private-v4 literal, so isLocalHostname accepts it
// whether or not the comparison exists — no other case in this file proves the
// branch. Here the origin's hostname is not loopback, not private-LAN, not
// .local and not allow-listed, so the only thing that can accept it is its
// equality with the Host the request arrived on. That is the deployment shape
// the branch exists for (a panel behind a Host-forwarding tunnel or reverse
// proxy); if it regresses, that panel gets 403 on every mutating route.
func TestATunnelledPanelStillWorks(t *testing.T) {
	base := newServer(t)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	origin := "http://panel.tunnel.test:" + u.Port()
	host := "panel.tunnel.test:" + u.Port()
	foreign := "other.tunnel.test:" + u.Port()

	t.Run("guarded PUT on its own forwarded Host reaches the handler", func(t *testing.T) {
		resp := doHost(t, http.MethodPut, base+"/api/chat/draft", origin, host, "application/json", `{"text":"tunnelled draft"}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("Access-Control-Allow-Origin = %q, want %q (never '*')", got, origin)
		}
		got := decode(t, doHost(t, http.MethodGet, base+"/api/chat/draft", origin, host, "", ""))
		if got["text"] != "tunnelled draft" {
			t.Fatalf("draft = %v, want what the tunnelled panel just wrote", got["text"])
		}
	})

	// The control: without it the accept case could be passing for some other
	// reason than the same-host comparison.
	t.Run("the same origin arriving on a different Host is refused", func(t *testing.T) {
		resp := doHost(t, http.MethodPut, base+"/api/chat/draft", origin, foreign, "application/json", `{"text":"should never land"}`)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Access-Control-Allow-Origin = %q, want none", got)
		}
	})
}

// TestTheCLIAndThePanelStillWork is the other half of the acceptance
// criteria: a guard that refuses a legitimate caller is a failed fix, not a
// strict one.
func TestTheCLIAndThePanelStillWork(t *testing.T) {
	base := newServer(t)
	// The panel's own origin is whatever host the request arrived on. Note it
	// satisfies both accept branches at once — see TestATunnelledPanelStillWorks
	// for the same-host comparison in isolation.
	panel := base

	t.Run("CLI/curl: no-Origin send is accepted", func(t *testing.T) {
		resp := do(t, http.MethodPost, base+"/api/chat/send", "", "application/json", `{"text":"from the cli"}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if ok, _ := decode(t, resp)["ok"].(bool); !ok {
			t.Fatal("ok != true")
		}
	})

	t.Run("CLI/curl: no-Origin subscribers still returns the full snapshot", func(t *testing.T) {
		resp := do(t, http.MethodGet, base+"/api/chat/subscribers", "", "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		// The exact keys `parlay doctor` / crew-state / the Go CLI read.
		got := decode(t, resp)
		for _, key := range []string{"parlay", "poll", "registered", "presence"} {
			if _, ok := got[key]; !ok {
				t.Fatalf("subscribers response is missing %q: %v", key, got)
			}
		}
	})

	t.Run("panel: same-origin register-agent is accepted and echoes its origin", func(t *testing.T) {
		resp := do(t, http.MethodPost, base+"/api/chat/register-agent", panel, "application/json", `{"id":"panel-agent","name":"Panel"}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != panel {
			t.Fatalf("Access-Control-Allow-Origin = %q, want %q (never '*')", got, panel)
		}
	})

	t.Run("panel: same-origin draft round-trips", func(t *testing.T) {
		if r := do(t, http.MethodPut, base+"/api/chat/draft", panel, "application/json", `{"text":"panel draft"}`); r.StatusCode != http.StatusOK {
			t.Fatalf("PUT status = %d, want 200", r.StatusCode)
		}
		got := decode(t, do(t, http.MethodGet, base+"/api/chat/draft", panel, "", ""))
		if got["text"] != "panel draft" {
			t.Fatalf("draft = %v, want the value the panel just wrote", got["text"])
		}
	})

	t.Run("panel: same-origin settings round-trip", func(t *testing.T) {
		if r := do(t, http.MethodPut, base+"/api/chat/parlay/settings", panel, "application/json", `{"textScale":120}`); r.StatusCode != http.StatusOK {
			t.Fatalf("PUT status = %d, want 200", r.StatusCode)
		}
		got := decode(t, do(t, http.MethodGet, base+"/api/chat/parlay/settings", panel, "", ""))
		if got["textScale"] != float64(120) {
			t.Fatalf("textScale = %v, want 120", got["textScale"])
		}
	})

	t.Run("panel: same-origin preflight on a guarded route succeeds", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodOptions, base+"/api/chat/draft", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Origin", panel)
		req.Header.Set("Access-Control-Request-Method", "PUT")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("preflight: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != panel {
			t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, panel)
		}
	})

	t.Run("read routes still answer, cross-origin included", func(t *testing.T) {
		for _, p := range []string{"/api/chat/history", "/api/chat/agents"} {
			if r := do(t, http.MethodGet, base+p, evil, "", ""); r.StatusCode != http.StatusOK {
				t.Errorf("%s: status = %d, want 200", p, r.StatusCode)
			}
		}
	})
}

// pixelGIF is a real 1x1 transparent GIF — the upload handler sniffs actual
// bytes, so a placeholder would be rejected for the wrong reason.
func pixelGIF(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return b
}

func multipartUpload(t *testing.T) (body *bytes.Buffer, contentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", "pixel.gif")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	part.Write(pixelGIF(t))
	w.Close()
	return body, w.FormDataContentType()
}

// TestUploadIsGuardedByOriginButNotByContentType pins the one deliberate
// asymmetry: /api/chat/upload is multipart by contract, so the JSON gate
// would reject every legitimate upload — the origin check alone carries it,
// which is sound because a browser always sends Origin on a cross-origin
// multipart POST.
func TestUploadIsGuardedByOriginButNotByContentType(t *testing.T) {
	base := newServer(t)

	t.Run("cross-origin multipart upload is refused", func(t *testing.T) {
		body, ct := multipartUpload(t)
		resp := do(t, http.MethodPost, base+"/api/chat/upload", evil, ct, body.String())
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (was 200 before this fix)", resp.StatusCode)
		}
	})

	t.Run("same-origin multipart upload still works and serves back", func(t *testing.T) {
		body, ct := multipartUpload(t)
		resp := do(t, http.MethodPost, base+"/api/chat/upload", base, ct, body.String())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 — the JSON gate must not apply here", resp.StatusCode)
		}
		got := decode(t, resp)
		if ok, _ := got["ok"].(bool); !ok {
			t.Fatalf("upload response = %v, want ok:true", got)
		}
		url, _ := got["url"].(string)
		if url == "" {
			t.Fatal("upload returned no url")
		}
		// The serve route stays unguarded so an <img src> can load it —
		// including from a page the guard would refuse to let write.
		img := do(t, http.MethodGet, base+url, evil, "", "")
		if img.StatusCode != http.StatusOK {
			t.Fatalf("serving the upload back: status = %d, want 200", img.StatusCode)
		}
		if ct := img.Header.Get("Content-Type"); ct != "image/gif" {
			t.Fatalf("Content-Type = %q, want image/gif", ct)
		}
	})

	t.Run("CLI/curl: no-Origin multipart upload is not 415'd", func(t *testing.T) {
		body, ct := multipartUpload(t)
		resp := do(t, http.MethodPost, base+"/api/chat/upload", "", ct, body.String())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})
}

// pollChannels reads the channels /api/chat/subscribers reports as actively
// polling. A poller exists only for the life of its request (handlePoll's
// AddPoller/RemovePoller pair), so this is the one observable that says a
// poll reached the handler.
func pollChannels(t *testing.T, base string) []string {
	t.Helper()
	body := decode(t, do(t, http.MethodGet, base+"/api/chat/subscribers", "", "", ""))
	p, _ := body["poll"].(map[string]any)
	list, _ := p["channels"].([]any)
	out := []string{}
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			if s, ok := m["channel"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// TestCrossOriginPollCannotRegisterAChannel is the regression for the second
// round of the same defect as D7: /api/chat/poll is a GET, so it was
// classified as a read and left outside the guard, while the handler takes a
// Presence poller slot for whatever channel the caller names (and on the TS
// server writes the agent registry outright). Against the pre-fix route set
// the request below answers 200 and the channel appears in /subscribers.
func TestCrossOriginPollCannotRegisterAChannel(t *testing.T) {
	base := newServer(t)

	// A legitimate no-Origin long poll held open in the background. It is the
	// control: it proves /subscribers really does surface a poller, so the
	// attacker's absence further down is evidence rather than an
	// unobservable state.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/chat/poll?channel=cli-poller", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()

	// The attack: a CORS *simple request* — a bare GET, no preflight, no
	// content type a browser would refuse to send.
	resp := do(t, http.MethodGet, base+"/api/chat/poll?channel=evil-poller", evil, "", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (was 200 before this fix)", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want none on a refusal", got)
	}

	var chans []string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		chans = pollChannels(t, base)
		if slices.Contains(chans, "cli-poller") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !slices.Contains(chans, "cli-poller") {
		t.Fatalf("the legitimate no-Origin poll never showed up in /subscribers (%v) — the assertion below would prove nothing", chans)
	}
	if slices.Contains(chans, "evil-poller") {
		t.Fatalf("the refused cross-origin poll registered a poller anyway: %v", chans)
	}

	cancel()
	<-done
}

// TestPollStillWorksForItsRealCallers is the other half: every poller in this
// repo (the relay, both CLI monitors, tools/split-test) is a no-Origin HTTP
// client, and the panel would be same-origin. Guarding /poll must cost them
// nothing.
func TestPollStillWorksForItsRealCallers(t *testing.T) {
	base := newServer(t)

	// Queue a message so the poll returns immediately instead of blocking for
	// defaultPollTimeout — an unknown `after` id falls back to full replay.
	if r := do(t, http.MethodPost, base+"/api/chat/message", "", "application/json",
		`{"channel":"cli-poller","text":"queued"}`); r.StatusCode != http.StatusOK {
		t.Fatalf("setup message: status = %d", r.StatusCode)
	}
	url := base + "/api/chat/poll?channel=cli-poller&after=unknown-id"

	t.Run("CLI/relay: no-Origin poll is accepted", func(t *testing.T) {
		resp := do(t, http.MethodGet, url, "", "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got, _ := decode(t, resp)["text"].(string); got != "queued" {
			t.Fatalf("polled text = %q, want the queued message", got)
		}
	})

	t.Run("panel: same-origin poll is accepted and echoes its own origin", func(t *testing.T) {
		resp := do(t, http.MethodGet, url, base, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != base {
			t.Fatalf("Access-Control-Allow-Origin = %q, want %q — never a wildcard", got, base)
		}
	})
}
