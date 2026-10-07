package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/store"
)

// waitForSubscriber blocks until the hub holds at least one client. The hub's
// own lock is used, so this is a bounded wait on real state rather than a sleep
// that hopes the SSE handler got there first.
func waitForSubscriber(t *testing.T, hub *Hub) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		n := len(hub.clients)
		hub.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no SSE subscriber registered within 2s")
}

// drainSSE lets the write loop flush, tears the subscription down, and returns
// what the stream carried. Reading the recorder while the handler goroutine is
// still writing would be a data race, so the stream is always closed first.
func drainSSE(rec *httptest.ResponseRecorder, stop func()) string {
	time.Sleep(100 * time.Millisecond)
	stop()
	return rec.Body.String()
}

// evalWithStore posts one /api/chat/eval through the real handler with a store
// the caller controls, so a test can assert on both the HTTP response and the
// command-log record the same call produced.
func evalWithStore(t *testing.T, st *store.Store, hub *Hub, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/eval", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleEval(st, hub)(rec, req)
	return rec
}

// okEngine replies with one named action and a `fired` command id — the
// smallest engine response that exercises both log axes.
func okEngine(fired string, verbs ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actions := make([]map[string]any, 0, len(verbs))
		for _, v := range verbs {
			actions = append(actions, map[string]any{"verb": v})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"v": 1, "streamId": "s", "seq": 1, "baseVersion": 1,
			"actions": actions, "engineEvalNs": int64(4242), "fired": fired,
		})
	}
}

func onlyRecord(t *testing.T, st *store.Store) store.ActionRecord {
	t.Helper()
	list := st.ActionLog.List(store.ActionLogFilter{})
	if len(list) != 1 {
		t.Fatalf("action log holds %d records, want exactly 1: %+v", len(list), list)
	}
	return list[0]
}

// A preview stream is evaluated against the real engine and its result is never
// broadcast — the whole difference between "show me" and "do it".
func TestPreviewIsEvaluatedButNeverDelivered(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	fakeEngine(t, okEngine("clear", "clear"))
	rec, stop := runEventsTarget(t, st, hub, "/api/chat/events?device=dev-1")
	waitForSubscriber(t, hub)

	body := `{"device":"dev-1","streamId":"sandbox-preview-dev-1-1","version":1,"text":"change inside input","voiceEnabled":true}`
	resp := evalWithStore(t, st, hub, body)
	sse := drainSSE(rec, stop)

	var decoded map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The engine DID answer: the preview is real evaluation, not a mock.
	actions, _ := decoded["actions"].([]any)
	if len(actions) == 0 {
		t.Error("preview returned no actions; a preview that cannot show the real result is a mock")
	}
	if strings.Contains(sse, "input_action") {
		t.Errorf("a preview stream delivered input_action over SSE:\n%s", sse)
	}
	got := onlyRecord(t, st)
	if got.Source != "test-site" {
		t.Errorf("source = %q, want test-site", got.Source)
	}
	if got.Outcome != store.OutcomeDropped || got.Reason != "preview-suppressed" {
		t.Errorf("outcome = %s/%s, want dropped/preview-suppressed", got.Outcome, got.Reason)
	}
}

// The same string on a FIRE stream is delivered, and the log says so.
func TestFireStreamIsDeliveredAndLogged(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	fakeEngine(t, okEngine("clear", "clear"))
	rec, stop := runEventsTarget(t, st, hub, "/api/chat/events?device=dev-2")
	waitForSubscriber(t, hub)

	evalWithStore(t, st, hub, `{"device":"dev-2","streamId":"sandbox-fire-dev-2-1","version":1,"text":"change inside input","voiceEnabled":true}`)
	sse := drainSSE(rec, stop)
	if !strings.Contains(sse, "input_action") {
		t.Errorf("a fire stream did not deliver input_action over SSE:\n%s", sse)
	}

	got := onlyRecord(t, st)
	if got.Outcome != store.OutcomeDelivered {
		t.Errorf("outcome = %s (%s), want delivered", got.Outcome, got.Reason)
	}
	if got.Source != "test-site" {
		t.Errorf("source = %q, want test-site", got.Source)
	}
}

// Delivered vs dropped is decided by whether anyone was actually listening.
func TestDeliveredRequiresALiveSubscriber(t *testing.T) {
	st := newTestStore(t)
	fakeEngine(t, okEngine("clear", "clear"))

	evalWithStore(t, st, newHub(newBroker()), `{"device":"nobody","streamId":"eval-nobody-main","version":1,"text":"x","voiceEnabled":true}`)

	got := onlyRecord(t, st)
	if got.Outcome != store.OutcomeDropped || got.Reason != "no-subscriber" {
		t.Errorf("outcome = %s/%s, want dropped/no-subscriber", got.Outcome, got.Reason)
	}
}

// An armed submit is QUEUED, not delivered: the engine deferred it to its own
// timer, which is a different fact from "the client already has it".
func TestArmedSubmitIsQueuedNotDelivered(t *testing.T) {
	st := newTestStore(t)
	fakeEngine(t, okEngine("submit", "armTimer"))

	evalWithStore(t, st, newHub(newBroker()), `{"device":"dev-3","streamId":"eval-dev-3-main","version":1,"text":"send it","voiceEnabled":true}`)

	got := onlyRecord(t, st)
	if got.Outcome != store.OutcomeQueued || got.Reason != "submit-armed" {
		t.Errorf("outcome = %s/%s, want queued/submit-armed", got.Outcome, got.Reason)
	}
	if got.InputAction != "submit" {
		t.Errorf("inputAction = %q, want submit", got.InputAction)
	}
}

// An unreachable engine is DROPPED, distinct from a refusal and from a match.
func TestEngineUnreachableIsDropped(t *testing.T) {
	st := newTestStore(t)
	t.Setenv("PARLAY_EVAL_ENGINE_URL", "http://127.0.0.1:1") // refused connection

	evalWithStore(t, st, newHub(newBroker()), `{"device":"dev-4","streamId":"eval-dev-4-main","version":1,"text":"x","voiceEnabled":true}`)

	got := onlyRecord(t, st)
	if got.Outcome != store.OutcomeDropped || got.Reason != "engine-unreachable" {
		t.Errorf("outcome = %s/%s, want dropped/engine-unreachable", got.Outcome, got.Reason)
	}
}

// A string that matches nothing is a distinct thing from a lost delivery, and
// the reason token is what keeps the two apart in the log.
func TestNoMatchIsDroppedWithItsOwnReason(t *testing.T) {
	st := newTestStore(t)
	fakeEngine(t, okEngine(""))

	evalWithStore(t, st, newHub(newBroker()), `{"device":"dev-5","streamId":"eval-dev-5-main","version":1,"text":"just some words","voiceEnabled":true}`)

	got := onlyRecord(t, st)
	if got.Outcome != store.OutcomeDropped || got.Reason != "no-match" {
		t.Errorf("outcome = %s/%s, want dropped/no-match", got.Outcome, got.Reason)
	}
}
