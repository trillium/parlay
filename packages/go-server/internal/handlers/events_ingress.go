package handlers

import (
	"encoding/json"
	"net/http"

	"parlay/go-server/internal/store"
)

// POST /api/chat/events — the external-producer ingress into the SSE hub.
//
// Every event Hub.broadcast carries today originates inside this process (the
// broker bridge, handleRegisterAgent, handlePoll, the command registry). Some
// producers do not and cannot live here: the two PAI observability tailers
// (packages/server/src/{tool,hook}-tailer.ts) read JSONL files under $PAI_DIR
// in the TS/Pulse home and are staying TS-side, but once the panel's
// /api/chat/events connection is served by this server their in-process
// broadcastToClients call reaches zero clients. This route is the seam: an
// out-of-process producer POSTs {event, data} and the payload fans out to
// every connected client exactly as an internal broadcast would. Same shape
// as the TS server's /api/chat/eval-push, which solved the same problem for
// the Go eval engine.
//
// # The allowlist, and why it is not "every documented event"
//
// ingressEvents is exactly the set of docs/api-contract.md "SSE Events" names
// that have a first-party client subscriber and NO producer inside this
// server — the "not live" column of events.go's table. Every name this server
// does produce (connected, history, agents, agent_register, presence_map,
// message, message_received, commands, command_update) is deliberately
// REFUSED here, because each of those frames is this server reporting its own
// persisted state. Accepting them from outside would let a caller put a
// message on the panel that is in no history file, or an agent in the panel's
// registry that GET /agents does not know about — a frame the panel cannot
// tell from the real thing and no reconnect would reproduce. An external
// producer that wants a message broadcast has POST /api/chat/message, which
// persists first and broadcasts as a consequence; that is the route the hook
// tailer uses.
//
// An unknown name is a 400 rather than a pass-through broadcast: the client's
// onSse shim lets plugins subscribe to arbitrary names, so an unguarded
// pass-through would make this route a general "push any frame to every
// panel" primitive on a server with no authentication.
//
// system_update is not in the list because it is not an event name. It is the
// `type` field of a ChatMessage carried on the `message` event (see
// packages/client/src/sse.ts and thread.ts, which branch on m.type ===
// 'system_update'); nothing in the panel listens for an event so named. The
// hook tailer's system_update lines therefore go to POST /api/chat/message
// with type "system_update", not here.
//
// This route is in guard.GuardedPaths, which is what keeps a foreign page
// from driving it — and because the guard classifies by path rather than
// method, that also closes GET /api/chat/events to cross-origin EventSource.
// Every legitimate caller is unaffected: the tailers and the CLI send no
// Origin, and the panel is same-origin.
var ingressEvents = map[string]bool{
	"tool_event":     true,
	"presence":       true,
	"agent_presence": true,
	"draft":          true,
	"lavish_session": true,
	"reload":         true,
	"navigate":       true,
	"input_action":   true,
	"device_cmd":     true,
	"pages_patch":    true,
}

// eventIngressRequest is the POST body: the event name plus its payload,
// captured as RawMessage so the payload reaches the wire byte-identical to
// what the producer sent. The tailers' existing frames must arrive at the
// panel indistinguishable from today's in-process broadcast, so this handler
// never reshapes, validates or re-keys the payload — the producer owns it.
type eventIngressRequest struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// eventIngressResponse is the {"ok": true, "event": "..."} ack. Echoing the
// accepted name back keeps a producer's own logging useful without the
// handler having to know anything about the payload.
type eventIngressResponse struct {
	OK    bool   `json:"ok"`
	Event string `json:"event"`
}

// handleEventsIngress implements POST /api/chat/events. Validation failures
// use the non-2xx convention (like /message and /unregister): the callers are
// server-side producers whose only sensible response to a rejection is to log
// and continue, and a status code is what an HTTP client checks without
// parsing a body.
func handleEventsIngress(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req eventIngressRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Event == "" {
			writeStatusError(w, http.StatusBadRequest, "event is required")
			return
		}
		if !ingressEvents[req.Event] {
			writeStatusError(w, http.StatusBadRequest, "event not accepted from an external producer: "+req.Event)
			return
		}

		// An absent payload broadcasts `{}`, matching how this package already
		// writes a payload-less frame (writeSSE(w, eventConnected, struct{}{})).
		// `reload` is the documented no-payload event.
		var data any = struct{}{}
		if len(req.Data) > 0 {
			data = req.Data
		}
		hub.broadcast(req.Event, data)

		writeJSON(w, eventIngressResponse{OK: true, Event: req.Event})
	}
}

// handleEventsRoute is the one mux registration for /api/chat/events, method-
// dispatching between the SSE stream and the ingress. net/http.ServeMux
// panics on a duplicate pattern, so a path serving two methods needs a single
// registration that switches — the same shape handleDraft and handleSettings
// already use for their GET/PUT pairs.
func handleEventsRoute(st *store.Store, hub *Hub) http.HandlerFunc {
	stream := handleEvents(st, hub)
	ingress := handleEventsIngress(hub)
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			stream(w, r)
		case http.MethodPost:
			ingress(w, r)
		default:
			methodNotAllowed(w, "GET, POST")
		}
	}
}
