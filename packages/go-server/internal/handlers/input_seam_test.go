package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/store"
)

// waitForEvents blocks until the ledger has at least n retained events. The
// writer is asynchronous by design, so a test that read the ring straight
// after a handler returned would be racing the design rather than testing it.
func waitForEvents(t *testing.T, st *store.Store, n int) []inputlog.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ev := st.Input.Events()
		if len(ev) >= n {
			return ev
		}
		if time.Now().After(deadline) {
			t.Fatalf("ledger has %d events after 3s, want at least %d: %+v", len(ev), n, ev)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func postSend(t *testing.T, st *store.Store, b *broker, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handleSend(st, b)(rec, req)
	return rec
}

func TestSendRecordsAQueuedHop(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()

	rec := postSend(t, st, b, `{"text":"ship it","toAgent":"c0"}`)
	var resp struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", rec.Body.String(), err)
	}
	if !resp.OK || resp.ID == "" {
		t.Fatalf("send response = %s, want ok with an id", rec.Body.String())
	}

	ev := waitForEvents(t, st, 1)
	got := ev[0]
	if got.InputID != resp.ID {
		t.Errorf("ledger InputID = %q, want the stored message id %q — a replay joins on it", got.InputID, resp.ID)
	}
	if got.Stage != inputlog.StageQueued || got.Class != inputlog.ClassOK {
		t.Errorf("hop = %s/%s, want queued/ok", got.Stage, got.Class)
	}
	if got.Source != inputSourceSend || got.Channel != "c0" {
		t.Errorf("source/channel = %q/%q, want send/c0", got.Source, got.Channel)
	}
}

func TestRefusedSendIsRecordedWithAReasonAndStoresNothing(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()

	rec := postSend(t, st, b, `{"toAgent":"c0"}`)
	if !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("refused send body = %s, want an error", rec.Body.String())
	}

	ev := waitForEvents(t, st, 1)
	got := ev[0]
	if got.Class != inputlog.ClassRefused || got.Reason != "empty-input" {
		t.Errorf("hop = %s/%q, want refused with the reason empty-input", got.Class, got.Reason)
	}
	if got.InputID == "" {
		t.Error("a refused input must still carry a ledger-local id")
	}
	if n := st.Messages.Count(); n != 0 {
		t.Errorf("history holds %d messages after a refused send, want 0", n)
	}
}

func TestPollRecordsADeliveredHopForTheBacklogPath(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	seed, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "seed", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish: %v", err)
	}
	next, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "next", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0&after="+seed.ID, nil)
	rec := httptest.NewRecorder()
	handlePoll(st, b, nil, 30*time.Millisecond)(rec, req)
	if !strings.Contains(rec.Body.String(), next.ID) {
		t.Fatalf("poll body = %s, want the queued message %s", rec.Body.String(), next.ID)
	}

	var delivered *inputlog.Event
	for _, e := range waitForEvents(t, st, 1) {
		if e.Stage == inputlog.StageDelivered && e.InputID == next.ID {
			ev := e
			delivered = &ev
		}
	}
	if delivered == nil {
		t.Fatalf("no delivered hop recorded for %s: %+v", next.ID, st.Input.Events())
	}
	if delivered.Source != inputSourcePollBacklog {
		t.Errorf("delivered source = %q, want %q — a drain after a reconnect is not a hot delivery", delivered.Source, inputSourcePollBacklog)
	}
}

func TestPollRecordsADeliveredHopForTheWakePath(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	done := make(chan string, 1)

	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0", nil)
		rec := httptest.NewRecorder()
		handlePoll(st, b, nil, 2*time.Second)(rec, req)
		done <- rec.Body.String()
	}()
	time.Sleep(50 * time.Millisecond) // let the waiter park

	sent, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "wake", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish: %v", err)
	}
	body := <-done
	if !strings.Contains(body, sent.ID) {
		t.Fatalf("parked poll body = %s, want the message %s", body, sent.ID)
	}

	var found bool
	for _, e := range waitForEvents(t, st, 1) {
		if e.Stage == inputlog.StageDelivered && e.InputID == sent.ID && e.Source == inputSourcePollWake {
			found = true
		}
	}
	if !found {
		t.Fatalf("no poll-wake delivered hop for %s: %+v", sent.ID, st.Input.Events())
	}
}

func TestInputEventsRouteServesLedgerAndStats(t *testing.T) {
	st := newTestStore(t)
	postSend(t, st, newBroker(), `{"text":"ship it","toAgent":"c0"}`)
	waitForEvents(t, st, 1)

	req := httptest.NewRequest(http.MethodGet, inputEventsPath, nil)
	rec := httptest.NewRecorder()
	handleInputEvents(st)(rec, req)

	var resp inputEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", rec.Body.String(), err)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	// The stats travel with the events on purpose: an empty event list and a
	// ledger that is silently dropping records must not look the same.
	if resp.Stats.Rejected != 0 || resp.Stats.Dropped != 0 {
		t.Errorf("stats = %+v, want no drops or rejections", resp.Stats)
	}

	// An unknown id is an honest empty answer, not an error and not a write.
	req = httptest.NewRequest(http.MethodGet, inputEventsPath+"?inputId=nope", nil)
	rec = httptest.NewRecorder()
	handleInputEvents(st)(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Events) != 0 {
		t.Errorf("unknown inputId returned %d events, want 0", len(resp.Events))
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestDeliveryIsNotSlowedOrFailedByAWedgedLedger is the handler-level half of
// the hard constraint that observability never sits in the delivery path. The
// ledger's sink never returns; accepting and delivering the operator's
// message must still work, and must not wait on it.
func TestDeliveryIsNotSlowedOrFailedByAWedgedLedger(t *testing.T) {
	st := newTestStore(t)
	block := make(chan struct{})
	wedged, err := inputlog.Open(filepath.Join(t.TempDir(), "input.jsonl"), inputlog.Options{
		Queue:    1,
		Appender: func([]byte) error { <-block; return nil },
	})
	if err != nil {
		t.Fatalf("inputlog.Open: %v", err)
	}
	t.Cleanup(func() { close(block) })
	st.Input.Close()
	st.Input = wedged

	b := newBroker()
	start := time.Now()
	rec := postSend(t, st, b, `{"text":"ship it","toAgent":"c0"}`)
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("send with a wedged ledger = %s, want success", rec.Body.String())
	}
	var sent struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// A second message so the poll below drains the backlog rather than
	// parking on the timeout.
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "second", Channel: "c0"}); err != nil {
		t.Fatalf("appendAndPublish: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0&after="+sent.ID, nil)
	pollRec := httptest.NewRecorder()
	handlePoll(st, b, nil, 30*time.Millisecond)(pollRec, req)
	elapsed := time.Since(start)

	if strings.Contains(pollRec.Body.String(), "timeout") {
		t.Fatalf("poll body = %s, want the queued message", pollRec.Body.String())
	}
	if elapsed > time.Second {
		t.Fatalf("accept+deliver took %v with a wedged ledger — observability is in the delivery path", elapsed)
	}
	// And the message itself is durably stored: a wedged observer never
	// costs the operator their input.
	if n := st.Messages.Count(); n != 2 {
		t.Errorf("history holds %d messages, want 2", n)
	}
}
