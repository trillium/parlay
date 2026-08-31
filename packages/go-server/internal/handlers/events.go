package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"parlay/go-server/internal/capability"
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
//	tool_event        live, but produced OUTSIDE this process: the TS tool
//	                  tailer POSTs it to the external-producer ingress on
//	                  POST /api/chat/events, the only name that ingress
//	                  accepts today. See events_ingress.go, which owns the
//	                  allowlist and the rule for widening it.
//	agent_presence    live (added after C2, for TS parity) — sent once per
//	                  connection on open with the current {active} value,
//	                  and broadcast on the 0↔1 parked-long-poll-waiter flips
//	                  driven from handlePoll (see pollWaiterParked).
//	reload, navigate, device_cmd
//	                  live (after C2) — broadcast from the panel command
//	                  routes (panel.go), globally or device-scoped.
//	pages_patch       live (after C2) — broadcast from the pages watcher
//	                  (pages.go).
//	presence, draft, lavish_session, input_action
//	                  not live — each names a real client-side handler (see
//	                  sse.ts) but no code path in this server produces the
//	                  underlying state change yet (no draft broadcast, no
//	                  session relay, no thinking-status signal). broadcast
//	                  can carry any of these the moment a future ticket
//	                  wires a producer; encoding/transport is not what's
//	                  missing.
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
	eventAgentUnregister = "agent_unregister"
	eventAgentPresence   = "agent_presence"
	eventPresenceMap     = "presence_map"
	eventMessage         = "message"
	eventMessageReceived = "message_received"
	eventCommands        = "commands"
	eventCommandUpdate   = "command_update"
)

// busEmitEvents is the allowlist of event names Hub.broadcast forwards to
// the Gas City bus sink when dual-write is enabled (events-lift U1). Same
// doctrine as events_ingress.go's ingressEvents — an explicit set, grown
// one real name at a time — and for a sibling reason: the bus's event log
// is a file other local processes can read AND write, so anything the
// server ever *consumes back* from the bus (U2) must be observability
// reporting, never a panel-aiming verb. Keeping panel-aiming and
// device-scoped names (reload, navigate, device_cmd, input_action,
// tts_event, pages_patch, cursorless_rpc) out of the dual-write keeps them
// structurally out of that future consume path too.
//
// The connect-only snapshot frames (connected, history, agents,
// presence_map, commands) never pass through broadcast at all — they are
// per-connection state, meaningless as bus records — so this list is
// exactly the live broadcast-path observability names.
var busEmitEvents = map[string]bool{
	eventMessage:         true,
	eventMessageReceived: true,
	eventAgentRegister:   true,
	eventCommandUpdate:   true,
	"tool_event":         true, // arrives via events_ingress.go's allowlist
}

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

// sseClient holds metadata for one connected SSE client. connID keys this
// connection's entry in the hub's capability registry; a client that sent no
// ?caps= declaration simply has no entry there (legacy, ungated).
type sseClient struct {
	ch          chan sseEvent
	device      string
	connID      string
	connectedAt string
}

// Hub is the SSE fan-out: every currently connected GET /events client, plus
// a single bridge subscription onto broker's wildcard feed (see newHub).
// Unlike broker (keyed by channel, one waiter wakes at most once per poll),
// Hub has no channel scoping — every connected client gets every broadcast,
// matching /events having no channel query parameter.
type Hub struct {
	mu      sync.Mutex
	clients map[chan sseEvent]*sseClient
	// busSink, when non-nil, receives every busEmitEvents broadcast as a
	// dual-write onto the Gas City event bus (events-lift U1). Set once at
	// startup via SetBusSink when the default-off flag is on; nil means
	// byte-identical behaviour to a build without the sink.
	busSink func(name string, data any)
	// caps holds each declared connection's ?caps= declaration plus the
	// suppression counters (docs/interface-capabilities.md); every broadcast
	// choke point below consults it. Undeclared connections have no entry
	// and are legacy: delivered everything, byte-identical.
	caps *capability.Registry
	// nextConn mints connection ids for caps entries, under mu.
	nextConn int
	// pollWaiters counts currently PARKED legacy long-poll waiters (under
	// mu) — the source for the agent_presence event, mirroring the TS
	// server's pollWaiters array + setAgentPresence
	// (packages/server/src/sse.ts). Only a poll that actually parks counts:
	// a backlog-served poll returns before parking and never touches
	// presence, which is why handlePoll drives this explicitly around its
	// blocking select rather than broker.subscribe counting implicitly.
	pollWaiters int
}

// SetBusSink installs the dual-write sink. The sink must never block: it
// is called on the broadcaster's goroutine (though outside the hub lock).
// internal/bus.Emitter.Emit satisfies that contract.
func (h *Hub) SetBusSink(sink func(name string, data any)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.busSink = sink
	h.mu.Unlock()
}

// newHub creates a Hub and starts its one background bridge goroutine,
// which subscribes to b (via subscribeAll) for the life of the process and
// re-broadcasts every message it sees as the `message` SSE event. This is
// the "reuse C1's broker as the event source" seam: broker.publish (called
// from appendAndPublish, unchanged by this ticket) is still the only place
// a ChatMessage fans out, whether to a blocked /poll or to every /events
// client.
func newHub(b *broker) *Hub {
	h := newHubCore()
	msgs, _ := b.subscribeAll() // never cancelled: lives exactly as long as the process, like b itself
	go func() {
		for m := range msgs {
			h.broadcast(eventMessage, m)
		}
	}()
	return h
}

// newHubCore builds a Hub with every field a live hub needs but no broker
// bridge — the shared core of newHub and of the bridge-less hubs the bus and
// eval tests construct. Nothing may build a Hub literal directly: a Hub
// whose caps registry is nil panics at the first broadcast (Decide on a nil
// receiver), deliberately unsoftened — a nil registry silently skipping the
// gate would be fail-open delivery.
func newHubCore() *Hub {
	return &Hub{clients: make(map[chan sseEvent]*sseClient), caps: capability.NewRegistry()}
}

// subscribe registers a new /events client and returns its event channel
// plus a cancel func the caller must defer immediately, mirroring
// broker.subscribe's contract.
func (h *Hub) subscribe(device string) (<-chan sseEvent, func()) {
	ch, cancel, _ := h.subscribeDeclared(device, nil) // nil declaration never errors: Register is not called
	return ch, cancel
}

// subscribeDeclared is subscribe plus an optional ?caps= capability
// declaration (docs/interface-capabilities.md). A non-nil decl is registered
// under a fresh connection id and deregistered by the same cancel that
// removes the client — the declaration's whole lifecycle is the
// connection's. The error path exists so a declaration the registry refuses
// fails the CONNECTION (fail loud) rather than silently downgrading the
// client to ungated legacy delivery — fail-open would widen what a
// narrowing surface receives.
func (h *Hub) subscribeDeclared(device string, decl *capability.Declaration) (<-chan sseEvent, func(), error) {
	ch := make(chan sseEvent, sseClientBuffer)
	h.mu.Lock()
	h.nextConn++
	connID := strconv.Itoa(h.nextConn)
	if decl != nil {
		if err := h.caps.Register(connID, decl); err != nil {
			h.mu.Unlock()
			return nil, nil, err
		}
	}
	h.clients[ch] = &sseClient{
		ch:          ch,
		device:      device,
		connID:      connID,
		connectedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
		h.caps.Deregister(connID)
	}
	return ch, cancel, nil
}

// broadcast delivers (name, data) to every currently connected client that
// the capability gate clears — a client that declared ?caps= only receives
// the gated presentation commands it accepts; undeclared clients are legacy
// and get everything (Registry.Decide counts each suppression, so the gate
// is observable on /api/chat/subscribers). Best-effort: a client whose
// buffer is already full (a stalled writer) has this event dropped rather
// than blocking every other subscriber — the same drop-if-full reasoning
// broker.publish already uses, and for the same underlying reason (one slow
// consumer must never stall every producer). A nil Hub is a deliberate no-op
// so handlePoll/handleRegisterAgent don't need a nil check at every call
// site — tests that don't care about SSE can pass nil in place of newHub(b).
func (h *Hub) broadcast(name string, data any) {
	if h == nil {
		return
	}
	h.mu.Lock()
	sink := h.busSink
	for ch, c := range h.clients {
		if h.caps.Decide(c.connID, name).Verdict == capability.VerdictSuppress {
			continue
		}
		select {
		case ch <- sseEvent{name: name, data: data}:
		default:
		}
	}
	h.mu.Unlock()
	// Dual-write after (and outside) the SSE delivery loop: the sink is
	// non-blocking by contract, but even so it must never run under h.mu.
	// broadcastToDevice deliberately has no sink — device-scoped events are
	// panel-aiming control pushes, not observability (see busEmitEvents).
	if sink != nil && busEmitEvents[name] {
		sink(name, data)
	}
}

// BroadcastFromBus delivers a bus-consumed event to every connected client
// (events-lift U2). It differs from broadcast in exactly two ways, both
// deliberate: it never touches busSink — a consumed event re-entering the
// dual-write would echo around the bus forever — and it enforces
// busEmitEvents itself, because the bus's event log is a file any local
// process can append to, so the hub (not the consumer subprocess plumbing)
// is where a smuggled panel-aiming name must die. Returns whether the name
// was accepted; a nil Hub accepts nothing.
func (h *Hub) BroadcastFromBus(name string, data any) bool {
	if h == nil || !busEmitEvents[name] {
		return false
	}
	h.mu.Lock()
	for ch, c := range h.clients {
		// Same capability gate as broadcast, for uniformity at every choke
		// point — vacuous today, since busEmitEvents holds no presentation
		// commands and only presentation commands ever suppress.
		if h.caps.Decide(c.connID, name).Verdict == capability.VerdictSuppress {
			continue
		}
		select {
		case ch <- sseEvent{name: name, data: data}:
		default:
		}
	}
	h.mu.Unlock()
	return true
}

// broadcastToDevice delivers (name, data) to every connected client whose
// device matches the given deviceId. Returns the count of clients that
// received the event — a capability-suppressed client does not count, so
// callers report delivery truth, not addressing truth (same rule as the TS
// side's broadcastToDevice). If deviceId is empty, broadcasts to all
// clients (back-compat).
func (h *Hub) broadcastToDevice(deviceId string, name string, data any) int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	matched := 0
	for ch, client := range h.clients {
		if deviceId != "" && client.device != deviceId {
			continue
		}
		if h.caps.Decide(client.connID, name).Verdict == capability.VerdictSuppress {
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

// capabilitySuppressed returns the per-event suppression counters for
// GET /api/chat/subscribers' `capability_suppressed` field — a silent gate
// would be indistinguishable from a gate that never runs.
func (h *Hub) capabilitySuppressed() map[string]int {
	if h == nil {
		return map[string]int{}
	}
	return h.caps.Suppressed()
}

// capabilityDeclarationEntry is one declared SSE connection as reported on
// GET /api/chat/subscribers' `capability_declarations` — field-for-field the
// TS side's shape: sorted accepts names, the advisory content/interactions
// axes (always arrays, never null, matching the TS parse's defaulting), and
// the connection's device id when it has one.
type capabilityDeclarationEntry struct {
	Surface      capability.Surface `json:"surface"`
	Accepts      []string           `json:"accepts"`
	Content      []string           `json:"content"`
	Interactions []string           `json:"interactions"`
	ConnectedAt  string             `json:"connectedAt"`
	Device       string             `json:"device,omitempty"`
}

// capabilityDeclarations lists every currently declared connection, in
// connection order (connectedAt is RFC3339Nano, so the lexicographic sort is
// chronological — the map iteration itself is unordered).
func (h *Hub) capabilityDeclarations() []capabilityDeclarationEntry {
	out := []capabilityDeclarationEntry{}
	if h == nil {
		return out
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		decl := h.caps.Get(c.connID)
		if decl == nil {
			continue
		}
		content, interactions := decl.Content, decl.Interactions
		if content == nil {
			content = []string{}
		}
		if interactions == nil {
			interactions = []string{}
		}
		out = append(out, capabilityDeclarationEntry{
			Surface:      decl.Surface,
			Accepts:      decl.AcceptNames(),
			Content:      content,
			Interactions: interactions,
			ConnectedAt:  c.connectedAt,
			Device:       c.device,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ConnectedAt != out[j].ConnectedAt {
			return out[i].ConnectedAt < out[j].ConnectedAt
		}
		return out[i].Device < out[j].Device
	})
	return out
}

// messageReceivedPayload is the `message_received` event's documented
// `{id}` shape.
type messageReceivedPayload struct {
	ID string `json:"id"`
}

// agentPresencePayload is the `agent_presence` event's `{active}` shape —
// true while ≥1 legacy long-poll waiter is parked (the panel's "agent away"
// banner listens for this).
type agentPresencePayload struct {
	Active bool `json:"active"`
}

// pollWaiterParked / pollWaiterDeparted bracket a parked long-poll waiter's
// lifetime (handlePoll calls them around its blocking select) and broadcast
// agent_presence on the 0↔1 flips only, mirroring the TS server's
// setAgentPresence (packages/server/src/sse.ts): repeat states are
// swallowed, so two concurrent waiters produce one true and one false, not
// two of each. The flip is computed under mu but broadcast after releasing
// it — broadcast takes mu itself.
func (h *Hub) pollWaiterParked() {
	if h == nil { // same convention as broadcast: nil hub = no SSE side at all
		return
	}
	h.mu.Lock()
	h.pollWaiters++
	flip := h.pollWaiters == 1
	h.mu.Unlock()
	if flip {
		h.broadcast(eventAgentPresence, agentPresencePayload{Active: true})
	}
}

func (h *Hub) pollWaiterDeparted() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.pollWaiters--
	flip := h.pollWaiters == 0
	h.mu.Unlock()
	if flip {
		h.broadcast(eventAgentPresence, agentPresencePayload{Active: false})
	}
}

// agentActive reports whether any long-poll waiter is currently parked —
// the connect burst's agent_presence value.
func (h *Hub) agentActive() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pollWaiters > 0
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

// connectedEcho is the `connected` payload for a declaring client — a
// struct rather than nested maps so the field order on the wire matches the
// TS server's (schema, recognized, unknown).
type connectedEcho struct {
	Capabilities struct {
		Schema     string   `json:"schema"`
		Recognized []string `json:"recognized"`
		Unknown    []string `json:"unknown"`
	} `json:"capabilities"`
}

// handleEvents implements GET /api/chat/events?device=<uuid>&after=<lastMsgId>&url=<currentPageUrl>&caps=<url-encoded JSON>.
// `device` is used for device-scoped SSE broadcasts (eval relay routes, etc);
// `url` is accepted but unused (nothing in this server yet needs per-URL
// history scoping). A clean-slate choice to not invent scoping logic nothing
// currently consumes.
//
// `caps` is the interface-capability declaration
// (docs/interface-capabilities.md): when present it is parsed and validated
// before anything is written, and an invalid declaration refuses the whole
// connection with a 400 `{"error": …}` and no stream — never a silent
// fallback to ungated legacy delivery, which would widen what the surface
// declared it wants. Absent means legacy: gated by nothing, byte-identical
// to the pre-capability behaviour.
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

		query := r.URL.Query()
		var decl *capability.Declaration
		if rawCaps, declared := query["caps"]; declared {
			d, err := capability.ParseDeclaration([]byte(rawCaps[0]))
			if err != nil {
				writeStatusError(w, http.StatusBadRequest, err.Error())
				return
			}
			decl = d
		}

		device := query.Get("device")
		ch, cancel, err := hub.subscribeDeclared(device, decl)
		if err != nil {
			writeStatusError(w, http.StatusBadRequest, err.Error())
			return
		}
		defer cancel()

		st.Presence.AddPanelClient()
		defer st.Presence.RemovePanelClient()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		after := query.Get("after")
		// Declaring clients get the negotiation echo — which accepts names
		// this server gates on vs. never heard of — so a surface can tell
		// when a capability it cares about is not enforced by the server it
		// reached. Legacy clients get the payload they always got,
		// byte-identical. (The TS side's echo also carries a clientId; this
		// server has no per-connection public id — divergence 1.)
		if decl != nil {
			recognized, unknown := decl.Recognize()
			var echo connectedEcho
			echo.Capabilities.Schema = decl.Schema
			echo.Capabilities.Recognized = recognized
			echo.Capabilities.Unknown = unknown
			writeSSE(w, eventConnected, echo)
		} else {
			writeSSE(w, eventConnected, struct{}{})
		}
		writeSSE(w, eventHistory, st.Messages.HistorySince(after))
		writeSSE(w, eventAgents, st.Registry.List())
		writeSSE(w, eventAgentPresence, agentPresencePayload{Active: hub.agentActive()})
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
