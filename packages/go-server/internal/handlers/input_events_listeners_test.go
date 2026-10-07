// The listener half of the input-seam read route: who has been asking each
// channel for messages. It exists because the ledger cannot express the
// difference between a queued input a listener will collect and one nobody
// will ever collect — both are the same `queued` row — so the view needs the
// server's own poll activity beside it.
//
// The nil-versus-empty contract is pinned here too: an ABSENT `listeners` field
// means "this server does not report listener activity" and an empty array
// means "nothing has polled any channel". A reader that conflated them would
// turn an old server into a silent fleet.
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"parlay/go-server/internal/store"
)

func findListener(resp inputEventsResponse, channel string) (inputListener, bool) {
	for _, l := range resp.Listeners {
		if l.Channel == channel {
			return l, true
		}
	}
	return inputListener{}, false
}

// waitForParkedPoller blocks until the presence tracker shows n waiters on
// channel, or fails. A parked poll is registered a moment after its request
// starts, so a reader that wants to observe one has to wait for it rather
// than assume it.
func waitForParkedPoller(t *testing.T, st *store.Store, channel string, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range st.Presence.Snapshot().PollChannels {
			if c.Channel == channel && c.Count == n {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("channel %q never showed %d parked poller(s)", channel, n)
}

func TestInputEventsReportsListenerActivity(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	hub := newHub(b)

	// Nothing has polled: the list is present and empty, which is a different
	// fact from an absent field.
	none := getInputEvents(t, st, "")
	if none.Listeners == nil {
		t.Fatal("listeners absent on a fresh ledger, want an empty array (an absent field means 'not reported')")
	}
	if len(none.Listeners) != 0 {
		t.Fatalf("listeners = %+v, want none before any poll", none.Listeners)
	}
	if none.PollHoldMs != defaultPollTimeout.Milliseconds() {
		t.Errorf("pollHoldMs = %d, want %d — a reader needs the window below which quiet is normal",
			none.PollHoldMs, defaultPollTimeout.Milliseconds())
	}

	// A parked long-poll on c0.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0", nil)
		rec := httptest.NewRecorder()
		handlePoll(st, b, hub, 250*time.Millisecond)(rec, req)
		done <- rec
	}()
	waitForParkedPoller(t, st, "c0", 1)

	parked := getInputEvents(t, st, "")
	fact, ok := findListener(parked, "c0")
	if !ok {
		t.Fatalf("c0 is parked on a poll but not listed: %+v", parked.Listeners)
	}
	if fact.ActivePollers != 1 {
		t.Errorf("activePollers = %d, want 1 while the poll is parked", fact.ActivePollers)
	}
	if _, err := time.Parse(time.RFC3339Nano, fact.LastPollTs); err != nil {
		t.Errorf("lastPollTs = %q, want an RFC3339Nano stamp: %v", fact.LastPollTs, err)
	}
	// A poll is not an input: reading listener activity records nothing.
	if len(parked.Events) != 0 {
		t.Errorf("the ledger gained %d row(s) from a poll, want none: %+v", len(parked.Events), parked.Events)
	}

	<-done

	// The waiter is gone; the fact that something asked is not.
	after := getInputEvents(t, st, "")
	fact, ok = findListener(after, "c0")
	if !ok {
		t.Fatalf("c0 vanished from the listener list after its poll returned: %+v", after.Listeners)
	}
	if fact.ActivePollers != 0 {
		t.Errorf("activePollers = %d, want 0 after the poll returned", fact.ActivePollers)
	}
	if fact.LastPollTs == "" {
		t.Error("lastPollTs is empty after a real poll — the one fact a live listener leaves behind")
	}
}

// A reader asking for one input, or for a page of the ledger, still gets the
// listener list: who is listening is a fact about the server right now, not
// about the window the reader narrowed to.
func TestInputEventsListenersSurviveNarrowing(t *testing.T) {
	st := newTestStore(t)
	st.Presence.TouchPoll("c9", time.Now().UTC().Format(time.RFC3339Nano))

	for _, q := range []string{"?limit=1", "?inputId=nope", "?afterSeq=0"} {
		resp := getInputEvents(t, st, q)
		if _, ok := findListener(resp, "c9"); !ok {
			t.Errorf("listeners dropped by %s: %+v", q, resp.Listeners)
		}
	}
}

// The proof the observer cannot fail a delivery: eight goroutines hammering
// the read route for the whole test while a parked poll is woken by a real
// send. The delivery still lands, and its delivered hop is still recorded.
func TestPollDeliveryIsNotBlockedByInputEventsReaders(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	hub := newHub(b)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0", nil)
		rec := httptest.NewRecorder()
		handlePoll(st, b, hub, 5*time.Second)(rec, req)
		done <- rec
	}()
	waitForParkedPoller(t, st, "c0", 1)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				req := httptest.NewRequest(http.MethodGet, inputEventsPath, nil)
				handleInputEvents(st)(httptest.NewRecorder(), req)
			}
		}()
	}

	postSend(t, st, b, `{"text":"wake up","toAgent":"c0"}`)

	select {
	case rec := <-done:
		if !strings.Contains(rec.Body.String(), `"role":"user"`) {
			t.Errorf("the woken poll answered %q, want the message", rec.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a woken delivery did not reach the parked poll while observers were reading")
	}
	close(stop)
	wg.Wait()

	ev := waitForEvents(t, st, 2)
	delivered := false
	for _, e := range ev {
		if e.Stage == "delivered" {
			delivered = true
		}
	}
	if !delivered {
		t.Errorf("no delivered hop recorded while readers were running: %+v", ev)
	}
}

// The wire contract the CLI's "not reported" state depends on: the field is
// emitted as an array, never null and never omitted.
func TestInputEventsListenersAreAlwaysAnArrayOnTheWire(t *testing.T) {
	st := newTestStore(t)
	req := httptest.NewRequest(http.MethodGet, inputEventsPath, nil)
	rec := httptest.NewRecorder()
	handleInputEvents(st)(rec, req)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := strings.TrimSpace(string(raw["listeners"])); got != "[]" {
		t.Errorf("listeners = %s, want [] — null or an absent key would read as 'not reported'", got)
	}
}
