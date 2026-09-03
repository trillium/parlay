package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/store"
)

func TestConnectBurstAgentPresenceOrderAndValue(t *testing.T) {
	// The TS burst order is connected, history, agents, agent_presence,
	// presence_map (packages/server/src/router-events.ts) — the Go burst
	// must interleave agent_presence at the same position, and an idle hub
	// reports {active:false}.
	rec, stop := runEvents(t, newTestStore(t), newHub(newBroker()))
	time.Sleep(50 * time.Millisecond) // let the initial burst land
	stop()

	body := rec.Body.String()
	if !strings.Contains(body, "event: agent_presence\ndata: {\"active\":false}\n\n") {
		t.Fatalf("burst missing agent_presence {active:false}; got:\n%s", body)
	}
	iAgents := strings.Index(body, "event: agents\n")
	iPresence := strings.Index(body, "event: agent_presence\n")
	iMap := strings.Index(body, "event: presence_map\n")
	if iAgents == -1 || iMap == -1 || !(iAgents < iPresence && iPresence < iMap) {
		t.Errorf("burst order wrong: agents@%d agent_presence@%d presence_map@%d, want agents < agent_presence < presence_map",
			iAgents, iPresence, iMap)
	}
}

func TestConnectBurstAgentPresenceTrueWhileWaiterParked(t *testing.T) {
	hub := newHub(newBroker())
	t.Cleanup(hub.Stop)
	hub.pollWaiterParked()
	defer hub.pollWaiterDeparted()

	rec, stop := runEvents(t, newTestStore(t), hub)
	time.Sleep(50 * time.Millisecond)
	stop()

	if !strings.Contains(rec.Body.String(), "event: agent_presence\ndata: {\"active\":true}\n\n") {
		t.Errorf("burst with a parked waiter missing {active:true}; got:\n%s", rec.Body.String())
	}
}

func TestPollParkAndTimeoutBroadcastPresenceFlips(t *testing.T) {
	st := newTestStore(t)
	b := newBroker()
	hub := newHub(b)
	t.Cleanup(hub.Stop)
	sub, cancel := hub.subscribe("")
	defer cancel()

	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0", nil)
		handlePoll(st, b, hub, 150*time.Millisecond)(httptest.NewRecorder(), req)
		close(done)
	}()

	// Parking broadcasts the 0→1 flip...
	ev := awaitEvent(t, sub, eventAgentPresence, time.Second)
	if !ev.data.(agentPresencePayload).Active {
		t.Errorf("park flip = %+v, want {active:true}", ev.data)
	}
	// ...and the timeout departure broadcasts the 1→0 flip.
	<-done
	ev = awaitEvent(t, sub, eventAgentPresence, time.Second)
	if ev.data.(agentPresencePayload).Active {
		t.Errorf("depart flip = %+v, want {active:false}", ev.data)
	}
}

func TestAgentPresenceFlipsOnlyOnZeroOneTransitions(t *testing.T) {
	hub := newHub(newBroker())
	t.Cleanup(hub.Stop)
	sub, cancel := hub.subscribe("")
	defer cancel()

	// broadcast enqueues synchronously, so after these four synchronous
	// calls the subscription buffer holds exactly the flip events, in
	// order — a repeat-state broadcast would be sitting right there.
	hub.pollWaiterParked()   // 0→1: flip
	hub.pollWaiterParked()   // 1→2: silent
	hub.pollWaiterDeparted() // 2→1: silent
	hub.pollWaiterDeparted() // 1→0: flip

	first := awaitEvent(t, sub, eventAgentPresence, time.Second)
	if !first.data.(agentPresencePayload).Active {
		t.Errorf("first flip = %+v, want {active:true}", first.data)
	}
	second := awaitEvent(t, sub, eventAgentPresence, time.Second)
	if second.data.(agentPresencePayload).Active {
		t.Errorf("second flip = %+v, want {active:false}", second.data)
	}
	select {
	case ev := <-sub:
		t.Errorf("unexpected extra event %q %+v — repeat states must be swallowed", ev.name, ev.data)
	default:
	}
}

func TestBacklogServedPollDoesNotTouchPresence(t *testing.T) {
	// A poll answered from the retained backlog returns before parking, so
	// it must not emit agent_presence at all — mirroring the TS server,
	// where the pending-history path returns before a waiter is created.
	st := newTestStore(t)
	b := newBroker()
	hub := newHub(b)
	t.Cleanup(hub.Stop)
	first, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "first", Channel: "c0"})
	if err != nil {
		t.Fatalf("appendAndPublish 1: %v", err)
	}
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "agent", Text: "second", Channel: "c0"}); err != nil {
		t.Fatalf("appendAndPublish 2: %v", err)
	}
	sub, cancel := hub.subscribe("")
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/chat/poll?channel=c0&after="+first.ID, nil)
	handlePoll(st, b, hub, time.Second)(httptest.NewRecorder(), req)

	// The backlog path broadcasts message_received (synchronously, above);
	// drain everything enqueued and assert agent_presence is not among it.
	sawReceived := false
	for {
		select {
		case ev := <-sub:
			if ev.name == eventAgentPresence {
				t.Fatalf("backlog-served poll emitted agent_presence %+v", ev.data)
			}
			if ev.name == eventMessageReceived {
				sawReceived = true
			}
			continue
		default:
		}
		break
	}
	if !sawReceived {
		t.Error("backlog-served poll did not emit message_received — the drain read the wrong stream")
	}
}
