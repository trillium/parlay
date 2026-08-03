package handlers

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"parlay/go-server/internal/store"
)

// defaultPollTimeout is how long GET /api/chat/poll blocks before returning
// {"timeout": true}. Not specified anywhere observable — docs/api-contract.md
// Open Gap #7 says this duration "not observable from any client call site" —
// chosen to sit comfortably under common reverse-proxy/idle-connection
// timeouts (60s+) while staying long enough to avoid a busy-poll loop.
const defaultPollTimeout = 25 * time.Second

// wildcardBuffer sizes the channel handed to subscribeAll. Only one reader
// ever drains it (the SSE hub's single bridging goroutine, ticket C2), so a
// small buffer is enough to absorb a burst (e.g. /alert fanning out to
// several channels in one request) without publish() blocking; see
// subscribeAll's doc comment for what happens if that reader ever falls
// behind anyway.
const wildcardBuffer = 16

// broker is transient, in-memory pub/sub between message-appending handlers
// and blocked GET /poll requests, keyed by channel. It intentionally holds
// no persisted state — see the package doc comment for why this isn't part
// of internal/store. Ticket C2 (the SSE hub) reuses this same broker as its
// event source via subscribeAll rather than standing up a second publish
// call site — see appendAndPublish, this type's one and only publish()
// caller, which neither ticket had to change.
type broker struct {
	mu       sync.Mutex
	subs     map[string]map[chan store.ChatMessage]struct{}
	wildcard map[chan store.ChatMessage]struct{}
}

func newBroker() *broker {
	return &broker{
		subs:     make(map[string]map[chan store.ChatMessage]struct{}),
		wildcard: make(map[chan store.ChatMessage]struct{}),
	}
}

// subscribeAll registers a waiter on every channel at once — the SSE hub's
// (ticket C2) registration path, since GET /events has no channel scoping
// (docs/api-contract.md's query params are device/after/url only, no
// channel). Deliberately a separate map from subs rather than a per-channel
// subscribe to every known channel: channels come and go with whatever
// string a caller passes to /send's toAgent, there is no fixed enumerable
// set to subscribe to up front.
func (b *broker) subscribeAll() (<-chan store.ChatMessage, func()) {
	ch := make(chan store.ChatMessage, wildcardBuffer)
	b.mu.Lock()
	b.wildcard[ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		delete(b.wildcard, ch)
		b.mu.Unlock()
	}
	return ch, cancel
}

// subscribe registers a waiter on channel and returns a receive-only channel
// plus a cancel func the caller must defer immediately — even on the
// timeout/success paths, not just an early return — to avoid leaking the
// subscription.
func (b *broker) subscribe(channel string) (<-chan store.ChatMessage, func()) {
	ch := make(chan store.ChatMessage, 1)
	b.mu.Lock()
	if b.subs[channel] == nil {
		b.subs[channel] = make(map[chan store.ChatMessage]struct{})
	}
	b.subs[channel][ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		delete(b.subs[channel], ch)
		if len(b.subs[channel]) == 0 {
			delete(b.subs, channel)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// publish delivers msg to every current waiter on msg.Channel, then to every
// subscribeAll waiter regardless of channel, and returns how many
// channel-scoped (poll) waiters received it — subscribeAll waiters aren't
// counted since delivered is /alert's per-channel-poller count, not
// something the SSE hub consumes. A subscriber whose buffer is already full
// is skipped rather than blocked on: each poll request only ever wants one
// message before it returns, so it can't be waiting to receive a second, and
// the hub's single subscribeAll reader is expected to drain continuously
// (see subscribeAll's doc comment) — a full wildcard buffer means that
// reader has stalled, and dropping the event there is preferable to
// blocking every other publish() caller (in-flight /send, /reply, /alert
// requests) on one wedged consumer.
func (b *broker) publish(msg store.ChatMessage) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	delivered := 0
	for ch := range b.subs[msg.Channel] {
		select {
		case ch <- msg:
			delivered++
		default:
		}
	}
	for ch := range b.wildcard {
		select {
		case ch <- msg:
		default:
		}
	}
	return delivered
}

// pollMessage is the shape GET /poll returns on a new message — a subset of
// ChatMessage's fields, matching docs/api-contract.md's documented response
// (`{id, role, text, from}`; no ts/channel/type).
type pollMessage struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	Text string `json:"text"`
	From string `json:"from,omitempty"`
}

func toPollMessage(m store.ChatMessage) pollMessage {
	return pollMessage{ID: m.ID, Role: m.Role, Text: m.Text, From: m.From}
}

// handlePoll implements GET /api/chat/poll?after=<lastId>&channel=<agentId>.
// timeout is a parameter (rather than always defaultPollTimeout) so tests can
// exercise the timeout path without waiting 25s. hub is ticket C2's SSE fan-out
// — a message delivered here means a queued message was just polled by its
// destination agent, exactly the trigger docs/api-contract.md's
// `message_received` event documents ("a queued user message was polled by
// the agent → flips the ◌→✓ pip"), so this is the one place in the C1
// handlers that also emits on the C2 hub.
func handlePoll(st *store.Store, b *broker, hub *Hub, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		channel := r.URL.Query().Get("channel")
		after := r.URL.Query().Get("after")

		ch, cancel := b.subscribe(channel)
		defer cancel()

		// Only check the retained backlog when the caller actually supplied
		// `after` — a bare poll with no `after` means "wait for the next
		// message", not "replay everything I might have missed". This is a
		// narrower scope than MessageStore.HistorySince's own "empty afterID
		// means full replay" convention (built for the SSE reconnect flow in
		// a later ticket), so it deliberately isn't called with after="".
		if after != "" {
			for _, m := range st.Messages.HistorySince(after) {
				if m.Channel == channel {
					writeJSON(w, toPollMessage(m))
					hub.broadcast(eventMessageReceived, messageReceivedPayload{ID: m.ID})
					return
				}
			}
		}

		st.Presence.AddPoller(channel)
		defer st.Presence.RemovePoller(channel)

		timer := time.NewTimer(timeout)
		defer timer.Stop()

		select {
		case m := <-ch:
			writeJSON(w, toPollMessage(m))
			hub.broadcast(eventMessageReceived, messageReceivedPayload{ID: m.ID})
		case <-timer.C:
			writeJSON(w, map[string]bool{"timeout": true})
		case <-r.Context().Done():
			// Client disconnected before a message arrived or the timeout
			// fired — nothing to write.
		}
	}
}

func handleHistory(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		limit := 0
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				limit = n
			}
		}
		writeJSON(w, st.Messages.History(limit))
	}
}
