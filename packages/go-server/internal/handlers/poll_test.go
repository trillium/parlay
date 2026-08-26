package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"parlay/go-server/internal/store"
)

func TestHandlePollTimesOutWhenNothingArrives(t *testing.T) {
	st := newTestStore(t)
	h := handlePoll(st, newBroker(), nil, 30*time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var got map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got["timeout"] {
		t.Errorf("body = %s, want {timeout:true}", rec.Body.String())
	}
}

func TestHandlePollWithoutAfterDoesNotReplayBacklog(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "already here", Channel: "c0"}); err != nil {
		t.Fatalf("appendAndPublish: %v", err)
	}

	h := handlePoll(st, b, nil, 30*time.Millisecond)
	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0", nil) // no after
	rec := httptest.NewRecorder()
	h(rec, req)

	var got map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got["timeout"] {
		t.Errorf("body = %s, want {timeout:true} — a bare poll with no `after` must not replay existing backlog", rec.Body.String())
	}
}

func TestHandlePollReturnsBacklogImmediatelyWhenAfterGiven(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	first, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "first", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish 1: %v", err)
	}
	second, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "agent", Text: "second", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish 2: %v", err)
	}

	h := handlePoll(st, b, nil, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0&after="+first.ID, nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var got pollMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != second.ID || got.Text != "second" {
		t.Errorf("poll response = %+v, want the message after %q", got, first.ID)
	}
}

func TestHandlePollUnknownCursorResumesFromZeroWithoutCap(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	first, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "first", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish 1: %v", err)
	}
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "agent", Text: "second", Channel: "c0"}); err != nil {
		t.Fatalf("appendAndPublish 2: %v", err)
	}

	// A cursor the store has never seen — the truncated/rotated/unknown-store
	// case (bound 2): the poll must resume from the oldest retained message
	// rather than silently returning {timeout:true} forever.
	h := handlePoll(st, b, nil, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0&after=stale-cursor-id", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var got pollMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != first.ID || got.Text != "first" {
		t.Errorf("poll response = %+v, want the oldest retained message %q", got, first.ID)
	}
	if !got.CursorReset {
		t.Errorf("cursorReset = false, want true for an unresolvable cursor")
	}
	if got.Skipped != 0 {
		t.Errorf("skipped = %d, want 0 — the whole 2-message backlog fits under the default cap", got.Skipped)
	}
}

func TestHandlePollUnknownCursorCapsReplayAndAnnouncesSkip(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()

	const total = store.DefaultReplayMax + 5
	var ids []string
	for i := 0; i < total; i++ {
		m, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "x", Channel: "c0"})
		if err != nil {
			t.Fatalf("appendAndPublish %d: %v", i, err)
		}
		ids = append(ids, m.ID)
	}

	// Bound 3: replay off an unresolvable cursor is capped to the newest
	// DefaultReplayMax messages, and the drop is announced on the wire
	// rather than silently truncated.
	h := handlePoll(st, b, nil, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0&after=stale-cursor-id", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var got pollMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantSkipped := total - store.DefaultReplayMax
	if !got.CursorReset {
		t.Errorf("cursorReset = false, want true")
	}
	if got.Skipped != wantSkipped {
		t.Errorf("skipped = %d, want %d", got.Skipped, wantSkipped)
	}
	if got.ID != ids[wantSkipped] {
		t.Errorf("poll response id = %q, want %q (oldest message inside the newest-%d window)", got.ID, ids[wantSkipped], store.DefaultReplayMax)
	}
}

func TestHandlePollResolvedCursorCarriesNoResetFlag(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	first, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "first", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish 1: %v", err)
	}
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "agent", Text: "second", Channel: "c0"}); err != nil {
		t.Fatalf("appendAndPublish 2: %v", err)
	}

	h := handlePoll(st, b, nil, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0&after="+first.ID, nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := raw["cursorReset"]; present {
		t.Errorf("response = %s, want no cursorReset field for a resolvable cursor (omitempty)", rec.Body.String())
	}
	if _, present := raw["skipped"]; present {
		t.Errorf("response = %s, want no skipped field for a resolvable cursor (omitempty)", rec.Body.String())
	}
}

func TestHandlePollIgnoresBacklogOnOtherChannels(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	first, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "seed", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish: %v", err)
	}
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "other channel", Channel: "other"}); err != nil {
		t.Fatalf("appendAndPublish 2: %v", err)
	}

	h := handlePoll(st, b, nil, 30*time.Millisecond)
	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0&after="+first.ID, nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var got map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got["timeout"] {
		t.Errorf("body = %s, want {timeout:true} — message on a different channel must not satisfy this poll", rec.Body.String())
	}
}

func TestHandlePollWakesOnNewMessage(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	h := handlePoll(st, b, nil, 2*time.Second)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0", nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		done <- rec
	}()

	// Small sleep to let the handler goroutine reach subscribe() before we
	// publish; deterministic synchronization would need a test-only hook
	// into the broker, not worth it for a single wake-up test.
	time.Sleep(20 * time.Millisecond)
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "agent", Text: "hi", Channel: "c0", From: "c1"}); err != nil {
		t.Fatalf("appendAndPublish: %v", err)
	}

	select {
	case rec := <-done:
		var got pollMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.Text != "hi" || got.Role != "agent" || got.From != "c1" {
			t.Errorf("poll response = %+v, want text=hi role=agent from=c1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("poll did not wake up on new message")
	}
}

func TestHandlePollWrongMethodIs405(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	handlePoll(newTestStore(t), newBroker(), nil, time.Second)(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestBrokerPublishSkipsFullSubscriberBuffer(t *testing.T) {
	b := newBroker()
	ch, cancel := b.subscribe("c0")
	defer cancel()

	msg := store.ChatMessage{ID: "m1", Channel: "c0"}
	if d := b.publish(msg); d != 1 {
		t.Fatalf("first publish delivered = %d, want 1", d)
	}
	// ch's buffer (size 1) now holds msg unread; a second publish to the
	// same subscriber must not block.
	if d := b.publish(store.ChatMessage{ID: "m2", Channel: "c0"}); d != 0 {
		t.Errorf("second publish delivered = %d, want 0 (buffer already full)", d)
	}
	<-ch // drain so cancel doesn't race a pending send in future changes
}
