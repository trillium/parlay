package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// postIngress drives POST /api/chat/events with a raw JSON body.
func postIngress(t *testing.T, hub *Hub, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleEventsIngress(hub)(rec, req)
	return rec
}

func TestEventsIngressBroadcastsAnAllowedEventToEveryClient(t *testing.T) {
	hub := newHub(newBroker())
	a, cancelA := hub.subscribe("")
	defer cancelA()
	b, cancelB := hub.subscribe("")
	defer cancelB()

	rec := postIngress(t, hub, `{"event":"tool_event","data":{"tool":"Bash","channel":"c1"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp eventIngressResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK || resp.Event != "tool_event" {
		t.Fatalf("response = %+v, want ok + the accepted event name", resp)
	}

	// Every connected client, not just the first: the hub has no channel
	// scoping and the tailers' frames must reach every panel tab.
	for i, sub := range []<-chan sseEvent{a, b} {
		ev := awaitEvent(t, sub, "tool_event", time.Second)
		got, err := json.Marshal(ev.data)
		if err != nil {
			t.Fatalf("client %d: marshal payload: %v", i, err)
		}
		// Byte-identical passthrough: the panel must not be able to tell an
		// ingress frame from today's in-process broadcast.
		if want := `{"tool":"Bash","channel":"c1"}`; string(got) != want {
			t.Errorf("client %d payload = %s, want %s", i, got, want)
		}
	}
}

func TestEventsIngressRejectsUnknownAndServerOwnedEvents(t *testing.T) {
	hub := newHub(newBroker())
	sub, cancel := hub.subscribe("")
	defer cancel()

	for _, name := range []string{
		"not_a_real_event",
		// Names this server produces from its own persisted state. Accepting
		// them from outside would put a frame on the panel that no history
		// file, registry entry or reconnect would reproduce.
		"message", "history", "agents", "agent_register", "message_received",
		"presence_map", "commands", "command_update",
		// Not an event name at all — it is a ChatMessage.type carried on
		// `message`; the hook tailer posts it to /api/chat/message instead.
		"system_update",
	} {
		rec := postIngress(t, hub, `{"event":"`+name+`","data":{}}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("event %q: status = %d, want 400", name, rec.Code)
		}
	}

	// A refusal broadcasts nothing at all.
	select {
	case ev := <-sub:
		t.Fatalf("a refused ingress broadcast %q", ev.name)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventsIngressRejectsAMissingEventNameAndABadBody(t *testing.T) {
	hub := newHub(newBroker())

	if rec := postIngress(t, hub, `{"data":{}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing event: status = %d, want 400", rec.Code)
	}
	if rec := postIngress(t, hub, `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("unparseable body: status = %d, want 400", rec.Code)
	}
}

func TestEventsIngressBroadcastsAnEmptyPayloadForADataLessEvent(t *testing.T) {
	hub := newHub(newBroker())
	sub, cancel := hub.subscribe("")
	defer cancel()

	if rec := postIngress(t, hub, `{"event":"tool_event"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	ev := awaitEvent(t, sub, "tool_event", time.Second)
	got, err := json.Marshal(ev.data)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if string(got) != "{}" {
		t.Errorf("payload = %s, want {}", got)
	}
}

// The route registration is one HandleFunc serving two methods; pin that both
// still reach their own handler and that anything else 405s with an Allow
// header naming both.
func TestEventsRouteDispatchesByMethod(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	route := handleEventsRoute(st, hub)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/events", strings.NewReader(`{"event":"tool_event","data":{}}`))
	req.Header.Set("Content-Type", "application/json")
	route(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("POST: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	route(rec, httptest.NewRequest(http.MethodDelete, "/api/chat/events", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, POST" {
		t.Errorf("Allow = %q, want %q", allow, "GET, POST")
	}
}
