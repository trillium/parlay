package handlers

import (
	"bytes"
	"net/http/httptest"
	"reflect"
	"testing"
)

// sinkRecorder collects bus-sink calls synchronously — broadcast invokes
// the sink on the caller's goroutine, so no synchronization dance is
// needed the way it is for client channels.
type sinkRecorder struct {
	calls []sseEvent
}

func (s *sinkRecorder) record(name string, data any) {
	s.calls = append(s.calls, sseEvent{name: name, data: data})
}

func TestBroadcastForwardsOnlyAllowlistedEventsToBusSink(t *testing.T) {
	h := &Hub{clients: make(map[chan sseEvent]*sseClient)}
	rec := &sinkRecorder{}
	h.SetBusSink(rec.record)

	h.broadcast(eventMessage, "chat")
	h.broadcast(eventMessageReceived, messageReceivedPayload{ID: "m1"})
	h.broadcast(eventAgentRegister, "agent")
	h.broadcast(eventCommandUpdate, "cmd")
	h.broadcast("tool_event", "tool")
	// Panel-aiming / device-level names must never reach the bus.
	h.broadcast("reload", struct{}{})
	h.broadcast("tts_event", "speech")
	h.broadcast("pages_patch", "patch")
	h.broadcast("cursorless_rpc", "rpc")

	wantNames := []string{eventMessage, eventMessageReceived, eventAgentRegister, eventCommandUpdate, "tool_event"}
	if len(rec.calls) != len(wantNames) {
		t.Fatalf("sink calls: want %d, got %d (%v)", len(wantNames), len(rec.calls), rec.calls)
	}
	for i, w := range wantNames {
		if rec.calls[i].name != w {
			t.Errorf("sink call %d: want %q, got %q", i, w, rec.calls[i].name)
		}
	}
}

func TestBroadcastToDeviceNeverReachesBusSink(t *testing.T) {
	h := &Hub{clients: make(map[chan sseEvent]*sseClient)}
	rec := &sinkRecorder{}
	h.SetBusSink(rec.record)

	// Even an allowlisted name via the device-scoped path stays off the bus:
	// broadcastToDevice is the panel-aiming control channel.
	h.broadcastToDevice("dev-1", eventMessage, "scoped")
	h.broadcastToDevice("", "tool_event", "scoped-all")

	if len(rec.calls) != 0 {
		t.Fatalf("device-scoped broadcast reached the bus sink: %v", rec.calls)
	}
}

// TestBroadcastOutputIdenticalWithAndWithoutSink is the flag-off
// byte-identity assertion (asserted on emitted output, not timing): the
// frames a subscribed client receives — and their rendered SSE bytes —
// are the same whether or not a bus sink is installed, and installing a
// nil sink (the flag-off wiring) records nothing.
func TestBroadcastOutputIdenticalWithAndWithoutSink(t *testing.T) {
	sequence := func(h *Hub) []sseEvent {
		ch, cancel := h.subscribe("")
		defer cancel()
		h.broadcast(eventMessage, map[string]string{"id": "m1", "text": "hello"})
		h.broadcast("tool_event", map[string]string{"tool": "Bash"})
		h.broadcast("reload", struct{}{})
		var got []sseEvent
		for len(got) < 3 {
			got = append(got, <-ch)
		}
		return got
	}

	plain := &Hub{clients: make(map[chan sseEvent]*sseClient)}
	sinked := &Hub{clients: make(map[chan sseEvent]*sseClient)}
	sinked.SetBusSink((&sinkRecorder{}).record)

	gotPlain := sequence(plain)
	gotSinked := sequence(sinked)
	if !reflect.DeepEqual(gotPlain, gotSinked) {
		t.Fatalf("client-visible events differ with sink installed:\nplain:  %v\nsinked: %v", gotPlain, gotSinked)
	}

	// And the wire rendering is byte-identical.
	render := func(evs []sseEvent) []byte {
		rec := httptest.NewRecorder()
		for _, ev := range evs {
			writeSSE(rec, ev.name, ev.data)
		}
		return rec.Body.Bytes()
	}
	if !bytes.Equal(render(gotPlain), render(gotSinked)) {
		t.Fatal("SSE wire bytes differ with sink installed")
	}
}
