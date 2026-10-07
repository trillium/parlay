// The cursor fact on the listener half of the input-seam read route.
//
// "A listener is attached and has not taken it" is a shrug until you know
// whether that listener could have taken it: handlePoll consults the retained
// store only for a request that carried `after=`, so a cursorless poll can only
// ever see a message published while it waited. The route reports which kind of
// poll was last seen per channel, and — because an older server simply will not
// have the key — it must be ABSENT rather than false there, or every listener
// on an old server would read as blind.
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

// pollOnce drives one real GET /poll and returns when it has answered (a bare
// poll parks and times out, which is enough to have recorded the fact).
func pollOnce(t *testing.T, st *store.Store, b *broker, hub *Hub, target string) {
	t.Helper()
	rec := httptest.NewRecorder()
	handlePoll(st, b, hub, 40*time.Millisecond)(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll %s answered %d, want 200", target, rec.Code)
	}
}

func TestInputEventsReportsWhetherTheLastPollCarriedACursor(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	hub := newHub(b)

	// A bare poll: nothing to replay from, so this listener cannot be handed a
	// message that was already queued when it arrived.
	pollOnce(t, st, b, hub, "/api/chat/poll?channel=c0")
	fact, ok := findListener(getInputEvents(t, st, ""), "c0")
	if !ok {
		t.Fatalf("c0 polled but is not in the listener list")
	}
	if fact.LastPollCursored == nil {
		t.Fatal("lastPollCursored absent after a real bare poll, want an explicit false")
	}
	if *fact.LastPollCursored {
		t.Error("a poll with no `after=` was reported as carrying a cursor")
	}

	// The same channel, now asking for the retained backlog. The LAST poll
	// decides: a listener that learned to pass a cursor is no longer blind.
	pollOnce(t, st, b, hub, "/api/chat/poll?channel=c0&after=m0")
	fact, _ = findListener(getInputEvents(t, st, ""), "c0")
	if fact.LastPollCursored == nil || !*fact.LastPollCursored {
		t.Errorf("lastPollCursored = %v after a cursored poll, want true (the last poll wins)", fact.LastPollCursored)
	}
}

// A server that reports the fact always emits it, so nil is reserved for a
// server that does not: the CLI's "not reported" state depends on that.
func TestInputEventsCursorFactIsAlwaysOnTheWire(t *testing.T) {
	st := newTestStore(t)
	st.Presence.TouchPoll("c0", time.Now().UTC().Format(time.RFC3339Nano), false)

	rec := httptest.NewRecorder()
	handleInputEvents(st)(rec, httptest.NewRequest(http.MethodGet, inputEventsPath, nil))

	var raw struct {
		Listeners []map[string]json.RawMessage `json:"listeners"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Listeners) != 1 {
		t.Fatalf("listeners = %+v, want one entry", raw.Listeners)
	}
	got, present := raw.Listeners[0]["lastPollCursored"]
	if !present {
		t.Fatal("lastPollCursored omitted — an absent key means 'not reported', so a reporting server must always send it")
	}
	if strings.TrimSpace(string(got)) != "false" {
		t.Errorf("lastPollCursored = %s, want false", got)
	}
}
