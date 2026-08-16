package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"parlay/go-server/internal/store"
)

// Ticket C2: the SSE hub behind GET /api/chat/events, the real-time push
// side of the chat server sitting next to poll.go's `broker` (C1's
// long-poll fan-out). docs/api-contract.md ("SSE Events") documents 17
// event names with a first-party browser-side subscriber
// (packages/client/src/sse.ts, `connect()`) — the table below is that same
// list, annotated with whether this server actually emits it today:
//
//	connected         live — sent once per connection, on open.
//	history           live — sent once per connection, on open (full or
//	                  delta via HistorySince, exactly as poll.go's own
//	                  after-vs-empty convention already established).
//	agents            live — sent once per connection, on open.
//	presence_map      live — sent once per connection, on open. Derived from
//	                  PresenceTracker.Snapshot's PollChannels (a channel with
//	                  an active long-poll waiter is reported "online"; every
//	                  other channel is simply absent from the map) — a
//	                  clean-slate choice, since PresenceTracker has no richer
//	                  status vocabulary than poll-count. It is not
//	                  re-broadcast when presence changes after connect
//	                  (PresenceTracker has no change-notification hook); a
//	                  reconnect (the client's own backoff-and-retry loop) is
//	                  how a tab picks up a fresher snapshot for now.
//	agent_register    live — broadcast from handleRegisterAgent (registry.go)
//	                  on every successful upsert.
//	message           live — bridged from broker.subscribeAll (poll.go); see
//	                  newHub.
//	message_received  live — broadcast from handlePoll (poll.go) at both
//	                  points a queued message is handed to a waiting agent.
//	agent_presence, presence, draft, tool_event, lavish_session, reload,
//	navigate, input_action, device_cmd, pages_patch
//	                  not live — each names a real client-side handler (see
//	                  sse.ts) but no C0/C1/C2 code path in this server
//	                  produces the underlying state change yet (no draft-set
//	                  endpoint, no device-cmd endpoint, no tool/session
//	                  relay, no thinking-status signal). broadcast can carry
//	                  any of these the moment a future ticket wires a
//	                  producer; encoding/transport is not what's missing.
//
// That is 17 documented names, 8 of them live from this ticket alone (9
// counting `message`, whose live is via the pre-existing C1 endpoints that
// already call appendAndPublish — send/reply/alert/message all become
// visible over SSE for free through the broker bridge, with zero changes to
// messaging.go).
//
// Two names were ADDED after that table, by the live-command registry
// (commands.go, docs/live-commands.md):
//
//	commands          live — sent once per connection, on open: the full
//	                  CommandRegistry snapshot, the same array GET
//	                  /api/chat/commands returns.
//	command_update    live — one CommandInvocation, broadcast whenever a
//	                  record starts, ends, or is reaped. State "dropped" means
//	                  the record has left the registry entirely — aged out of
//	                  retention, or shed by the record cap — and a client
//	                  should forget the id.
//
// Both are additive: an older client simply has no listener registered for
// them and ignores the frames, which is why this could be done without
// touching any existing event's shape.
const (
	eventConnected       = "connected"
	eventHistory         = "history"
	eventAgents          = "agents"
	eventAgentRegister   = "agent_register"
	eventPresenceMap     = "presence_map"
	eventMessage         = "message"
	eventMessageReceived = "message_received"
	eventCommands        = "commands"
	eventCommandUpdate   = "command_update"

	// Event names this server emits via device-scoped or broadcast paths on
	// top of the connect-burst above. Each matches a first-party client
	// subscriber in packages/client/src/sse.ts (see docs/api-contract.md
	// "SSE Events"): device_cmd/navigate/reload/input_action are the
	// device-driving actions, draft echoes cross-device draft sync, presence
	// drives the thinking-dots indicator, pages_patch updates the page-nav
	// picker, and tool_event carries the observability tailer's log lines.
	eventDeviceCmd   = "device_cmd"
	eventNavigate    = "navigate"
	eventReload      = "reload"
	eventInputAction = "input_action"
	eventDraft       = "draft"
	eventPresence    = "presence"
	eventPagesPatch  = "pages_patch"
	eventToolEvent   = "tool_event"
	eventCursorless  = "cursorless_rpc"
)

// sseClientBuffer sizes each connected client's outgoing event channel. A
// generous multiple of wildcardBuffer: unlike the hub's single bridge
// reader (which must never block broker.publish), a per-client buffer only
// protects against that one client's own HTTP write stalling — dropping
// here is a lost update for one browser tab, not a stall for every other
// publisher, so err on the side of not dropping under an ordinary burst.
const sseClientBuffer = 64

// sseHeartbeatInterval is how often a `GET /events` connection gets a
// comment-only keep-alive line. Same reasoning as poll.go's
// defaultPollTimeout: sit comfortably under common reverse-proxy/idle-
// connection timeouts so a quiet channel doesn't get silently dropped.
const sseHeartbeatInterval = 25 * time.Second

// sseEvent is one named, JSON-encodable event queued for a connected client.
type sseEvent struct {
	name string
	data any
}

// Hub is the SSE fan-out: every currently connected GET /events client, plus
// a single bridge subscription onto broker's wildcard feed (see newHub).
// Unlike broker (keyed by channel, one waiter wakes at most once per poll),
// Hub has no channel scoping — every connected client gets every broadcast,
// matching /events having no channel query parameter. Each client DOES carry
// its optional device id (from ?device=) so the device-driving routes
// (device-cmd, navigate, reload, input_action) can target exactly one panel.
type Hub struct {
	mu      sync.Mutex
	clients map[chan sseEvent]string // event channel -> device id ("" = none)
}

// newHub creates a Hub and starts its one background bridge goroutine,
// which subscribes to b (via subscribeAll) for the life of the process and
// re-broadcasts every message it sees as the `message` SSE event. This is
// the "reuse C1's broker as the event source" seam: broker.publish (called
// from appendAndPublish, unchanged by this ticket) is still the only place
// a ChatMessage fans out, whether to a blocked /poll or to every /events
// client.
func newHub(b *broker) *Hub {
	h := &Hub{clients: make(map[chan sseEvent]string)}
	msgs, _ := b.subscribeAll() // never cancelled: lives exactly as long as the process, like b itself
	go func() {
		for m := range msgs {
			h.broadcast(eventMessage, m)
		}
	}()
	return h
}

// subscribe registers a new /events client (with its optional device id) and
// returns its event channel plus a cancel func the caller must defer
// immediately, mirroring broker.subscribe's contract.
func (h *Hub) subscribe(device string) (<-chan sseEvent, func()) {
	ch := make(chan sseEvent, sseClientBuffer)
	h.mu.Lock()
	h.clients[ch] = device
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
	}
	return ch, cancel
}

// broadcast delivers (name, data) to every currently connected client.
// Best-effort: a client whose buffer is already full (a stalled writer) has
// this event dropped rather than blocking every other subscriber — the same
// drop-if-full reasoning broker.publish already uses, and for the same
// underlying reason (one slow consumer must never stall every producer). A
// nil Hub is a deliberate no-op so handlePoll/handleRegisterAgent don't need
// a nil check at every call site — tests that don't care about SSE can pass
// nil in place of newHub(b).
func (h *Hub) broadcast(name string, data any) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- sseEvent{name: name, data: data}:
		default:
		}
	}
}

// broadcastToDevice delivers (name, data) to every client whose recorded
// device id equals deviceId, returning how many clients matched. This is the
// scoped counterpart of broadcast for the device-driving surface: eval's
// input_action, device-cmd, navigate, and reload all target one panel rather
// than every tab. A deviceId of "" matches no client (broadcast is the
// all-tabs path); passing a device the panel never registered simply fans
// out to zero, which callers report as a zero matched count.
func (h *Hub) broadcastToDevice(deviceId, name string, data any) int {
	if h == nil || deviceId == "" {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	matched := 0
	for ch, dev := range h.clients {
		if dev != deviceId {
			continue
		}
		matched++
		select {
		case ch <- sseEvent{name: name, data: data}:
		default:
		}
	}
	return matched
}

// messageReceivedPayload is the `message_received` event's documented
// `{id}` shape.
type messageReceivedPayload struct {
	ID string `json:"id"`
}

// presenceMapPayload derives the `presence_map` event's channel→status map
// — see the package doc comment above for the "online iff an active poller"
// design choice.
func presenceMapPayload(st *store.Store) map[string]string {
	snap := st.Presence.Snapshot()
	m := make(map[string]string, len(snap.PollChannels))
	for _, pc := range snap.PollChannels {
		if pc.Count > 0 {
			m[pc.Channel] = "online"
		}
	}
	return m
}

// writeSSE writes one `event: name\ndata: <json>\n\n` frame. Encode errors
// are ignored, matching writeJSON's own convention in handlers.go: every
// concrete type this package ever passes here (ChatMessage(s), AgentInfo(s),
// map[string]string, messageReceivedPayload, struct{}{}) always marshals.
func writeSSE(w http.ResponseWriter, name string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
}

// handleEvents implements GET /api/chat/events?device=<uuid>&after=<lastMsgId>&url=<currentPageUrl>.
// `device` is now stored per client so the device-driving routes
// (device-cmd, navigate, reload, input_action) can target exactly one panel;
// a missing device simply means that client never receives scoped events.
// `url` is accepted but unused: nothing needs per-URL history scoping
// (docs/api-contract.md's own wording is speculative, not a documented
// required behavior), so no scoping logic is invented for it.
func handleEvents(st *store.Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		device := r.URL.Query().Get("device")
		ch, cancel := hub.subscribe(device)
		defer cancel()

		st.Presence.AddPanelClient()
		defer st.Presence.RemovePanelClient()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		after := r.URL.Query().Get("after")
		writeSSE(w, eventConnected, struct{}{})
		writeSSE(w, eventHistory, st.Messages.HistorySince(after))
		writeSSE(w, eventAgents, st.Registry.List())
		writeSSE(w, eventPresenceMap, presenceMapPayload(st))
		writeSSE(w, eventCommands, st.Commands.List())
		flusher.Flush()

		heartbeat := time.NewTicker(sseHeartbeatInterval)
		defer heartbeat.Stop()

		for {
			select {
			case ev := <-ch:
				writeSSE(w, ev.name, ev.data)
				flusher.Flush()
			case <-heartbeat.C:
				fmt.Fprint(w, ": keep-alive\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}
