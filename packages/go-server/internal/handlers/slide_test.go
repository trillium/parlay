package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/remoteinput"
)

func TestSlidePageLinksToDeck(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/slide", nil)
	rec := httptest.NewRecorder()
	handleSlidePage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /slide: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	const link = `<a class="deck" href="https://voicefirst.dev/slides/" target="_blank" rel="noopener">Open slides</a>`
	if !strings.Contains(body, link) {
		t.Fatal("slide page missing Open slides link")
	}
	for _, id := range []string{`id="token"`, `id="next"`, `id="previous"`} {
		if !strings.Contains(body, id) {
			t.Fatalf("slide page missing %s", id)
		}
	}
}

func postSlide(t *testing.T, h http.HandlerFunc, auth, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/slide", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Code, rec.Body.String()
}

// The phone trigger and the generic action-mode submit must reach Talon with
// the identical RunAction name — the one voice's slide_next/slide_previous
// resolve to through remoteinput.ActionSnippet.
func TestSlideTriggerReachesSameActionAsSubmit(t *testing.T) {
	for _, tc := range []struct{ dir, action, snippet string }{
		{"next", "slide_next", "actions.user.slide_next()"},
		{"previous", "slide_previous", "actions.user.slide_previous()"},
	} {
		phone := &remoteinput.FakeTalon{}
		phoneSvc := remoteinput.NewService(phone, 0, nil)
		defer phoneSvc.Stop()
		code, body := postSlide(t, handleSlideTrigger(phoneSvc, remoteInputSeam{}, func() string { return "s3cret" }),
			"Bearer s3cret", `{"direction":"`+tc.dir+`"}`)
		if code != http.StatusAccepted {
			t.Fatalf("%s: got %d (%s)", tc.dir, code, body)
		}
		waitSvcID(t, phoneSvc, body)

		direct := &remoteinput.FakeTalon{}
		directSvc := remoteinput.NewService(direct, 0, nil)
		defer directSvc.Stop()
		code, raw := postBead(t, directSvc, "/api/chat/remote-input/submit",
			`{"device":"phone-1","mode":"action","action":"`+tc.action+`"}`)
		if code != http.StatusAccepted {
			t.Fatalf("direct %s: got %d (%s)", tc.action, code, raw)
		}
		waitSvcID(t, directSvc, string(raw))

		if len(phone.RunActions) != 1 || len(direct.RunActions) != 1 || phone.RunActions[0] != direct.RunActions[0] {
			t.Fatalf("phone %v vs submit %v: not the same path", phone.RunActions, direct.RunActions)
		}
		if got, _ := remoteinput.ActionSnippet(phone.RunActions[0]); got != tc.snippet {
			t.Fatalf("snippet = %q, want %q", got, tc.snippet)
		}
	}
}

func TestSlideTriggerAuth(t *testing.T) {
	fake := &remoteinput.FakeTalon{}
	svc := remoteinput.NewService(fake, 0, nil)
	defer svc.Stop()
	tok := ""
	h := handleSlideTrigger(svc, remoteInputSeam{}, func() string { return tok })
	body := `{"direction":"next"}`

	if code, _ := postSlide(t, h, "Bearer x", body); code != http.StatusServiceUnavailable {
		t.Fatalf("no token configured: got %d, want 503", code)
	}
	tok = "s3cret"
	for _, auth := range []string{"", "Bearer wrong", "s3cret"} {
		if code, _ := postSlide(t, h, auth, body); code != http.StatusUnauthorized {
			t.Fatalf("auth %q: got %d, want 401", auth, code)
		}
	}
	if code, _ := postSlide(t, h, "Bearer s3cret", `{"direction":"quit"}`); code != http.StatusBadRequest {
		t.Fatalf("bad direction: got %d, want 400", code)
	}
	if len(fake.RunActions) != 0 {
		t.Fatalf("rejected requests reached Talon: %v", fake.RunActions)
	}
}

// waitSvcID waits for the submission named in a 202 body to settle.
func waitSvcID(t *testing.T, svc *remoteinput.Service, body string) {
	t.Helper()
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("bad submit body %q: %v", body, err)
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if o, ok := svc.Get(resp.ID); ok && o.Terminal() {
			return
		}
	}
	t.Fatalf("submission %s never settled", resp.ID)
}

func TestSlidePageConsumesPairingFragment(t *testing.T) {
	rec := httptest.NewRecorder()
	handleSlidePage(rec, httptest.NewRequest(http.MethodGet, "/slide", nil))
	body := rec.Body.String()
	for _, want := range []string{`/[#&]t=([^&]+)/.exec(location.hash)`, `history.replaceState`, `localStorage.setItem('slideToken'`} {
		if !strings.Contains(body, want) {
			t.Fatalf("slide page missing pairing-fragment handling %q", want)
		}
	}
}

func TestSlideTokenFallsBackToPairToken(t *testing.T) {
	t.Cleanup(func() { SetPairToken("") })
	t.Setenv(SlideTokenEnv, "")
	SetPairToken("paired")
	if got := slideTokenFromEnv(); got != "paired" {
		t.Fatalf("got %q, want pairing token", got)
	}
	t.Setenv(SlideTokenEnv, "envwins")
	if got := slideTokenFromEnv(); got != "envwins" {
		t.Fatalf("got %q, want env override", got)
	}
}

func TestSlideTailscaleIdentity(t *testing.T) {
	svc := remoteinput.NewService(&remoteinput.FakeTalon{}, 0, nil)
	defer svc.Stop()
	h := handleSlideTrigger(svc, remoteInputSeam{}, func() string { return "tok" })
	do := func(remote, header string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/chat/slide", strings.NewReader(`{"direction":"next","dryRun":true}`))
		req.RemoteAddr = remote
		if header != "" {
			req.Header.Set("Tailscale-User-Login", header)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}
	if c := do("127.0.0.1:5555", "me@example.com"); c != http.StatusAccepted {
		t.Fatalf("loopback proxy with identity: got %d, want 202", c)
	}
	if c := do("192.168.1.9:5555", "me@example.com"); c != http.StatusUnauthorized {
		t.Fatalf("forged identity from LAN peer: got %d, want 401", c)
	}
	if c := do("127.0.0.1:5555", ""); c != http.StatusUnauthorized {
		t.Fatalf("loopback without identity or token: got %d, want 401", c)
	}
}
