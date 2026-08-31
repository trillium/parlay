package handlers

import (
	"encoding/json"
	"net/http"

	"parlay/go-server/internal/sourcecontracts"
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
// # The allowlist: one name per real producer, derived from enrolled contracts
//
// ingressEvents is the set of event names an out-of-process producer that
// exists TODAY needs. It is no longer written by hand: at init it is derived
// from the enrolled source contracts (docs/source-contracts.md — the
// canonical contracts/sources/ tree at the repo root, embedded here via
// internal/sourcecontracts) as the union of `emits` across every contract
// with the observability trust posture. Today that is tool_event alone — the
// TS tool tailer, enrolled as contracts/sources/tool-tailer.json. The hook
// tailer, the only other caller this seam was built for, does not come through
// here at all: it posts to /api/chat/message, which persists first and
// broadcasts as a consequence.
//
// The rule for widening it is per real producer, not per documented name —
// and enrollment is how a real producer is named now: the set grows only when
// a contract declaring the producer lands in contracts/sources/ (a reviewed
// repo change; the tools/cli/internal/sourcecontract engine holds each event
// name to a single owning contract). The tempting larger set — every
// docs/api-contract.md SSE name with a first-party client subscriber and no
// producer inside this server (the "not live" column of events.go's table) —
// is deliberately NOT what this is, because it sweeps in the panel-aiming
// events. This route's guard allows a missing Origin by design (that is what
// lets the tailers and the CLI through), so admitting one of those would let
// any local or LAN process reload or navigate every connected panel,
// overwrite the captain's draft, or replay an input_action envelope. The
// refused rosters below are therefore hard-coded HERE, not read from the
// contract tree: no enrollment, however trusted its posture, can put one of
// those names in this map — deriveIngressEvents panics instead, and the
// engine independently refuses to validate a contract declaring one (same
// vocabulary, enforced on both sides of the module boundary on purpose).
//
// An unknown name is a 400 rather than a pass-through broadcast: the client's
// onSse shim lets plugins subscribe to arbitrary names, so an unguarded
// pass-through would make this route a general "push any frame to every
// panel" primitive on a server with no authentication.
//
// This route is in guard.GuardedPaths, which is what keeps a foreign page
// from driving it — and because the guard classifies by path rather than
// method, that also closes GET /api/chat/events to cross-origin EventSource.
// Every legitimate caller is unaffected: the tailers and the CLI send no
// Origin, and the panel is same-origin.

// panelAimingEvents are the names whose frames drive the connected panels
// themselves (navigate away, force-reload, inject input, overwrite the
// draft). This server has no route that emits any of them, and no external
// producer may either — see the doctrine above.
var panelAimingEvents = map[string]bool{
	"navigate":       true,
	"reload":         true,
	"device_cmd":     true,
	"input_action":   true,
	"draft":          true,
	"tts_event":      true,
	"pages_patch":    true,
	"cursorless_rpc": true,
}

// serverOwnedEvents are the names this server itself produces — each is this
// server reporting its own persisted state. Accepting one from outside would
// let a caller put a message on the panel that is in no history file, or an
// agent in the panel's registry that GET /agents does not know about — a
// frame the panel cannot tell from the real thing and no reconnect would
// reproduce.
var serverOwnedEvents = map[string]bool{
	"connected":        true,
	"history":          true,
	"agents":           true,
	"agent_register":   true,
	"presence_map":     true,
	"message":          true,
	"message_received": true,
	"commands":         true,
	"command_update":   true,
}

// deriveIngressEvents builds the allowlist from the enrolled contracts: the
// union of emits across every observability-posture declaration. It fails
// closed in both directions. A missing or unparseable contract tree yields an
// empty set (Enrolled() returns nil), so a corrupted registry refuses every
// producer rather than half-working. A forbidden name yields a panic at init,
// because a server that would boot with `reload` externally injectable is
// worse than a server that does not boot — and the panic can only fire on a
// contract change, which lands through CI that runs this package's tests.
//
// system_update is refused by name because it is not an event name at all: it
// is the `type` field of a ChatMessage carried on the `message` event (see
// packages/client/src/sse.ts and thread.ts, which branch on m.type ===
// 'system_update'); nothing in the panel listens for an event so named. The
// hook tailer's system_update lines therefore go to POST /api/chat/message
// with type "system_update", not here.
func deriveIngressEvents(enrolled []sourcecontracts.Declared) map[string]bool {
	events := make(map[string]bool)
	for _, d := range enrolled {
		if d.Trust != "observability" {
			continue
		}
		for _, name := range d.Emits {
			switch {
			case panelAimingEvents[name]:
				panic("source contract " + d.Name + " emits panel-aiming event " + name + " — refused, see events_ingress.go doctrine")
			case serverOwnedEvents[name]:
				panic("source contract " + d.Name + " emits server-owned event " + name + " — refused, see events_ingress.go doctrine")
			case name == "system_update":
				panic("source contract " + d.Name + " emits system_update, which is a message type, not an event name — refused")
			}
			events[name] = true
		}
	}
	return events
}

var ingressEvents = deriveIngressEvents(sourcecontracts.Enrolled())

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
		// writes a payload-less frame (writeSSE(w, eventConnected, struct{}{})),
		// so an allowed name whose producer has nothing to say still reaches
		// the wire as valid JSON rather than as a bare `data:` line.
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
