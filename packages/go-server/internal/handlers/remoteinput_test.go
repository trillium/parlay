// Handler tests for the remote-input intake: wire shape, validation,
// status polling, and the success-clears-state signal (settled callback
// fires with "injected" so Parlay clears shared state only then).
package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	body, _ := json.Marshal(map[string]string{
		"device": "phone-1", "text": "hello\nworld",
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
