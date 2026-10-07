// Handler tests for the remote-input intake: wire shape, validation,
// status polling, and the success-clears-state signal (settled callback
// fires with "injected" so Parlay clears shared state only then).
package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/remoteinput"
)

// testRemoteService builds a fake-backed service with a settle hook.
func testRemoteService(settled chan remoteinput.Outcome) *remoteinput.Service {
	var cb func(remoteinput.Outcome)
	if settled != nil {
		cb = func(o remoteinput.Outcome) {
			if o.Terminal() {
				settled <- o
			}
		}
	}
	return remoteinput.NewService(&remoteinput.FakeTalon{}, 0, cb)
}

// waitRemoteOutcome polls the status handler until terminal or timeout.
func waitRemoteOutcome(t *testing.T, h http.HandlerFunc, id string) remoteinput.Outcome {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		req := httptest.NewRequest("GET", "/api/chat/remote-input/status?id="+id, nil)
		w := httptest.NewRecorder()
		h(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200", w.Code)
		}
		var o remoteinput.Outcome
		if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
			t.Fatalf("status: bad body: %v", err)
		}
		if o.Terminal() {
			return o
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRemoteInputSubmitQueuedThenInjected(t *testing.T) {
	settled := make(chan remoteinput.Outcome, 4)
	svc := testRemoteService(settled)
	defer svc.Stop()
	submit := handleRemoteInputSubmit(svc)
	status := handleRemoteInputStatus(svc)

	body, _ := json.Marshal(map[string]any{
		"device": "phone-1", "text": "hello\nworld",
		"allowUnfocused": true, // targetless live submit names the mode
	})
	req := httptest.NewRequest("POST", "/api/chat/remote-input/submit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	submit(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("submit: got %d, want 202", w.Code)
	}
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("submit: bad body: %v", err)
	}
	if resp.ID == "" || resp.Status != remoteinput.StatusQueued {
		t.Fatalf("submit: unexpected response %+v", resp)
	}

	o := waitRemoteOutcome(t, status, resp.ID)
	if o.Status != remoteinput.StatusInjected {
		t.Fatalf("expected injected, got %+v", o)
	}
	if !o.InjectAttempted {
		t.Fatalf("injected outcome must flag attempted: %+v", o)
	}
	if o.Focus != remoteinput.FocusAllowedUnfocused || !o.AllowUnfocused {
		t.Fatalf("named unfocused mode must surface on the outcome: %+v", o)
	}

	// Success-clears-state: the settle hook fired exactly once, injected —
	// the signal Parlay uses to clear shared state.
	select {
	case got := <-settled:
		if got.ID != resp.ID || got.Status != remoteinput.StatusInjected {
			t.Fatalf("settle signal wrong: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settle callback never fired")
	}
}

func TestRemoteInputSubmitValidation(t *testing.T) {
	svc := testRemoteService(nil)
	defer svc.Stop()
	submit := handleRemoteInputSubmit(svc)

	for name, body := range map[string]string{
		"missing device": `{"text":"hi"}`,
		"missing text":   `{"device":"d"}`,
		"invalid json":   `{`,
	} {
		req := httptest.NewRequest("POST", "/api/chat/remote-input/submit", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		submit(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, w.Code)
		}
	}

	req := httptest.NewRequest("GET", "/api/chat/remote-input/submit", nil)
	w := httptest.NewRecorder()
	submit(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("wrong method: got %d, want 405", w.Code)
	}
}

func TestRemoteInputDryRunSubmitAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		body  string
	}{
		{"field", "", `{"device":"phone-1","text":"dry ✓\nline","dryRun":true}`},
		{"query param", "?dryRun=1", `{"device":"phone-1","text":"dry ✓\nline"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := testRemoteService(nil)
			defer svc.Stop()
			submit := handleRemoteInputSubmit(svc)
			status := handleRemoteInputStatus(svc)

			req := httptest.NewRequest("POST", "/api/chat/remote-input/submit"+tc.query, bytes.NewBufferString(tc.body))
			w := httptest.NewRecorder()
			submit(w, req)
			if w.Code != http.StatusAccepted {
				t.Fatalf("submit: got %d, want 202", w.Code)
			}
			var resp remoteinput.SubmitResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("submit: bad body: %v", err)
			}

			o := waitRemoteOutcome(t, status, resp.ID)
			if o.Status != remoteinput.StatusDryRunPassed {
				t.Fatalf("expected dry_run_passed, got %+v", o)
			}
			if !o.DryRun || o.InjectAttempted {
				t.Fatalf("dry run must flag dryRun and never attempt: %+v", o)
			}
			if o.WouldInsert != "dry ✓\nline" {
				t.Fatalf("wouldInsert = %q, want exact bytes", o.WouldInsert)
			}
		})
	}
}

func TestRemoteInputSubmitRefusesTargetlessLive(t *testing.T) {
	svc := testRemoteService(nil)
	defer svc.Stop()
	submit := handleRemoteInputSubmit(svc)

	req := httptest.NewRequest("POST", "/api/chat/remote-input/submit",
		bytes.NewBufferString(`{"device":"phone-1","text":"blind?"}`))
	w := httptest.NewRecorder()
	submit(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("targetless live submit: got %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "allowUnfocused") {
		t.Fatalf("typed refusal must name the flag: %s", w.Body.String())
	}
}

func TestRemoteInputSubmitTargetlessAllowedWhenNamed(t *testing.T) {
	for _, target := range []string{
		"/api/chat/remote-input/submit?allowUnfocused=1",
		"/api/chat/remote-input/submit",
	} {
		body := `{"device":"phone-1","text":"explicit"}`
		if !strings.Contains(target, "?") {
			body = `{"device":"phone-1","text":"explicit","allowUnfocused":true}`
		}
		svc := testRemoteService(nil)
		submit := handleRemoteInputSubmit(svc)
		status := handleRemoteInputStatus(svc)

		req := httptest.NewRequest("POST", target, bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		submit(w, req)
		if w.Code != http.StatusAccepted {
			svc.Stop()
			t.Fatalf("%s: got %d, want 202", target, w.Code)
		}
		var resp remoteinput.SubmitResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			svc.Stop()
			t.Fatalf("submit: bad body: %v", err)
		}
		o := waitRemoteOutcome(t, status, resp.ID)
		svc.Stop()
		if o.Status != remoteinput.StatusInjected {
			t.Fatalf("%s: expected injected, got %+v", target, o)
		}
		if o.Focus != remoteinput.FocusAllowedUnfocused || !o.AllowUnfocused {
			t.Fatalf("%s: named mode must surface: %+v", target, o)
		}
	}
}

func TestRemoteInputSubmitDryRunTargetlessAllowed(t *testing.T) {
	// Dry runs type nothing, so the no-target rule exempts them: the
	// success leg stays provable with zero keystrokes and no target.
	svc := testRemoteService(nil)
	defer svc.Stop()
	submit := handleRemoteInputSubmit(svc)
	status := handleRemoteInputStatus(svc)

	req := httptest.NewRequest("POST", "/api/chat/remote-input/submit",
		bytes.NewBufferString(`{"device":"phone-1","text":"dry no target","dryRun":true}`))
	w := httptest.NewRecorder()
	submit(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("dry-run targetless submit: got %d, want 202", w.Code)
	}
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("submit: bad body: %v", err)
	}
	o := waitRemoteOutcome(t, status, resp.ID)
	if o.Status != remoteinput.StatusDryRunPassed {
		t.Fatalf("expected dry_run_passed, got %+v", o)
	}
	if o.Focus != remoteinput.FocusNotRequired {
		t.Fatalf("expected not_required focus, got %+v", o)
	}
	if o.WouldInsert != "dry no target" {
		t.Fatalf("wouldInsert = %q, want exact bytes", o.WouldInsert)
	}
}

func TestRemoteInputTargets(t *testing.T) {
	fake := &remoteinput.FakeTalon{TargetsList: []remoteinput.Target{
		{Name: "WezTerm", Focused: true, WindowTitle: "macbookpro: coder", WindowCount: 1, HasWindows: true},
		{Name: "Raycast", WindowCount: 0, HasWindows: false},
	}}
	svc := remoteinput.NewService(fake, 0, nil)
	defer svc.Stop()
	h := handleRemoteInputTargets(svc)

	req := httptest.NewRequest("GET", "/api/chat/remote-input/targets", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("targets: got %d, want 200", w.Code)
	}
	var resp remoteinput.TargetsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("targets: bad body: %v", err)
	}
	if len(resp.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %+v", resp)
	}
	if resp.Targets[0].Name != "WezTerm" || !resp.Targets[0].Focused {
		t.Fatalf("first target wrong: %+v", resp.Targets[0])
	}
	if resp.Targets[1].HasWindows || resp.Targets[1].WindowCount != 0 {
		t.Fatalf("windowless marker wrong: %+v", resp.Targets[1])
	}
	if resp.DryRun {
		t.Fatalf("plain targets call must not echo dryRun: %+v", resp)
	}
	if len(fake.Inserts) != 0 || len(fake.FocusAppCalls) != 0 {
		t.Fatalf("targets must type and focus nothing")
	}

	// ?dryRun=1 is an accepted no-op echo on the read-only path.
	req = httptest.NewRequest("GET", "/api/chat/remote-input/targets?dryRun=1", nil)
	w = httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("targets dryRun: got %d, want 200", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("targets dryRun: bad body: %v", err)
	}
	if !resp.DryRun || len(resp.Targets) != 2 {
		t.Fatalf("dry-run targets wrong: %+v", resp)
	}

	// Wrong method is 405, like the sibling routes.
	req = httptest.NewRequest("POST", "/api/chat/remote-input/targets", nil)
	w = httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("wrong method: got %d, want 405", w.Code)
	}
}

func TestRemoteInputTargetsTalonFailure(t *testing.T) {
	fake := &remoteinput.FakeTalon{TargetsErr: remoteinput.ErrFocusTransport}
	svc := remoteinput.NewService(fake, 0, nil)
	defer svc.Stop()
	h := handleRemoteInputTargets(svc)

	req := httptest.NewRequest("GET", "/api/chat/remote-input/targets", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("talon failure: got %d, want 502", w.Code)
	}
}

func TestRemoteInputStatusUnknown(t *testing.T) {
	svc := testRemoteService(nil)
	defer svc.Stop()
	status := handleRemoteInputStatus(svc)

	req := httptest.NewRequest("GET", "/api/chat/remote-input/status?id=ri-999", nil)
	w := httptest.NewRecorder()
	status(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown id: got %d, want 404", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/chat/remote-input/status", nil)
	w = httptest.NewRecorder()
	status(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing id: got %d, want 400", w.Code)
	}
}

func TestRemoteInputFocusFailureSurfacesTypedOutcome(t *testing.T) {
	fake := &remoteinput.FakeTalon{ActiveAppName: "Other", StickyActive: true}
	svc := remoteinput.NewService(fake, 0, nil)
	defer svc.Stop()
	submit := handleRemoteInputSubmit(svc)
	status := handleRemoteInputStatus(svc)

	body, _ := json.Marshal(map[string]string{
		"device": "phone-1", "text": "keep me",
		"app": "Terminal", "trigger": "send it",
	})
	req := httptest.NewRequest("POST", "/api/chat/remote-input/submit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	submit(w, req)
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("submit: bad body: %v", err)
	}

	o := waitRemoteOutcome(t, status, resp.ID)
	if o.Status != remoteinput.StatusFocusFailed {
		t.Fatalf("expected focus_failed, got %+v", o)
	}
	if o.InjectAttempted || !o.PreserveText || !o.StripTrigger {
		t.Fatalf("typed failure wrong: %+v", o)
	}
	if len(fake.Inserts) != 0 {
		t.Fatalf("focus failure must inject nothing, got %d", len(fake.Inserts))
	}
}
