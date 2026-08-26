package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/store"
)

// runEvents starts handleEvents in a goroutine against a cancellable
// context and returns the recorder plus a stop func that cancels the
// context and waits for the handler goroutine to return — the same
// "sleep long enough for the other goroutine to reach its blocking point,
// then synchronize on a done channel" pattern poll_test.go's
// TestHandlePollWakesOnNewMessage already uses, since a deterministic hook
// into hub.subscribe would not be worth adding for tests alone.
func runEvents(t *testing.T, st *store.Store, hub *Hub) (rec *httptest.ResponseRecorder, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/chat/events", nil).WithContext(ctx)
	rec = httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handleEvents(st, hub)(rec, req)
		close(done)
	}()
	return rec, func() {
		cancel()
		<-done
	}
}

// awaitEvent reads sub until it sees an event named want (skipping any
// other event, e.g. the `message` bridge broadcast a test's own
// appendAndPublish call also triggers alongside the event under test) or
// fails the test after timeout.
func awaitEvent(t *testing.T, sub <-chan sseEvent, want string, timeout time.Duration) sseEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-sub:
			if ev.name == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("did not see event %q within %s", want, timeout)
			return sseEvent{}
		}
	}
}

func TestHandleEventsSendsInitialBurstOnConnect(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Registry.Upsert(store.AgentInfo{ID: "c1", Name: "Firstmate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Messages.Append(store.ChatMessage{Role: "user", Text: "hi", Channel: "c0"}); err != nil {
		t.Fatal(err)
	}

	rec, stop := runEvents(t, st, newHub(newBroker()))
	time.Sleep(50 * time.Millisecond) // let the initial burst land before we stop the stream
	stop()

	body := rec.Body.String()
	for _, want := range []string{"event: connected", "event: history", "event: agents", "event: presence_map"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; got:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `"text":"hi"`) {
		t.Errorf("history event missing appended message; got:\n%s", body)
	}
	if !strings.Contains(body, `"name":"Firstmate"`) {
		t.Errorf("agents event missing registered agent; got:\n%s", body)
	}
}

func TestHandleEventsWrongMethodIs405(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	handleEvents(newTestStore(t), newHub(newBroker()))(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleEventsDeliversMessageViaBrokerBridge(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	hub := newHub(b)

	rec, stop := runEvents(t, st, hub)
	time.Sleep(50 * time.Millisecond) // let the client register with hub before we publish
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "agent", Text: "pushed", Channel: "c1"}); err != nil {
		t.Fatalf("appendAndPublish: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the bridge goroutine deliver before we stop
	stop()

	body := rec.Body.String()
	if !strings.Contains(body, "event: message") || !strings.Contains(body, `"text":"pushed"`) {
		t.Errorf("body missing pushed message event; got:\n%s", body)
	}
}

func TestHandleEventsTracksPanelClientPresence(t *testing.T) {
	st := newTestStore(t)
	rec, stop := runEvents(t, st, newHub(newBroker()))
	_ = rec

	time.Sleep(50 * time.Millisecond)
	if got := st.Presence.Snapshot().PanelClients; got != 1 {
		t.Errorf("PanelClients while connected = %d, want 1", got)
	}
	stop()
	if got := st.Presence.Snapshot().PanelClients; got != 0 {
		t.Errorf("PanelClients after disconnect = %d, want 0", got)
	}
}

func TestHubBroadcastOnNilHubIsNoop(t *testing.T) {
	var hub *Hub
	hub.broadcast(eventMessage, store.ChatMessage{}) // must not panic
}

func TestBrokerPublishDeliversToWildcardSubscribers(t *testing.T) {
	b := newBroker()
	ch, cancel := b.subscribeAll()
	defer cancel()

	if d := b.publish(store.ChatMessage{ID: "m1", Channel: "any-channel"}); d != 0 {
		t.Errorf("publish delivered (channel-scoped count) = %d, want 0 (no channel subscriber)", d)
	}
	select {
	case got := <-ch:
		if got.ID != "m1" {
			t.Errorf("got = %+v, want ID=m1", got)
		}
	default:
		t.Fatal("wildcard subscriber did not receive published message")
	}
}

func TestHandlePollBroadcastsMessageReceivedOnBacklogDelivery(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	hub := newHub(b)
	first, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "first", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish 1: %v", err)
	}
	second, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "agent", Text: "second", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish 2: %v", err)
	}

	sub, cancel := hub.subscribe("")
	defer cancel()

	h := handlePoll(st, b, hub, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0&after="+first.ID, nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	ev := awaitEvent(t, sub, eventMessageReceived, time.Second)
	payload, ok := ev.data.(messageReceivedPayload)
	if !ok || payload.ID != second.ID {
		t.Errorf("event data = %+v, want id=%s", ev.data, second.ID)
	}
}

func TestHandlePollBroadcastsMessageReceivedOnWakeup(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	hub := newHub(b)
	sub, cancel := hub.subscribe("")
	defer cancel()

	h := handlePoll(st, b, hub, 2*time.Second)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0", nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		done <- rec
	}()

	time.Sleep(20 * time.Millisecond) // let the handler goroutine reach subscribe() before we publish
	msg, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "agent", Text: "hi", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poll did not wake up on new message")
	}

	ev := awaitEvent(t, sub, eventMessageReceived, time.Second)
	payload, ok := ev.data.(messageReceivedPayload)
	if !ok || payload.ID != msg.ID {
		t.Errorf("event data = %+v, want id=%s", ev.data, msg.ID)
	}
}

func TestHandleRegisterAgentBroadcastsAgentRegister(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	sub, cancel := hub.subscribe("")
	defer cancel()

	postJSON(t, handleRegisterAgent(st, hub), map[string]any{"id": "c1", "name": "Firstmate"})

	select {
	case ev := <-sub:
		if ev.name != eventAgentRegister {
			t.Fatalf("event name = %q, want %q", ev.name, eventAgentRegister)
		}
		agent, ok := ev.data.(store.AgentInfo)
		if !ok || agent.ID != "c1" {
			t.Errorf("event data = %+v, want AgentInfo{ID: c1}", ev.data)
		}
	case <-time.After(time.Second):
		t.Fatal("hub did not receive agent_register event")
	}
}

func TestPresenceMapPayloadMarksActivePollersOnline(t *testing.T) {
	st := newTestStore(t)
	st.Presence.AddPoller("c1")

	got := presenceMapPayload(st)
	if got["c1"] != "online" {
		t.Errorf("presenceMapPayload = %+v, want c1=online", got)
	}
	if _, ok := got["c2"]; ok {
		t.Errorf("presenceMapPayload = %+v, want no entry for an untouched channel", got)
	}
}
