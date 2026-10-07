package store

import (
	"sort"
	"sync"
)

// PresenceTracker holds the transient, in-memory connection/subscriber
// counters GET /subscribers reports (docs/api-contract.md §Agent registry /
// presence, SubscribersInfo). Unlike the other substores, none of this is
// persisted to disk — a connection count that survived a restart would just
// be wrong, since every live connection is gone the moment the process
// exits.
type PresenceTracker struct {
	mu sync.RWMutex

	panelClients int
	pollers      map[string]int    // channel -> active long-poll count
	lastSeen     map[string]string // channel -> ISO timestamp of last message activity
	lastPoll     map[string]string // channel -> ISO timestamp of the last POLL REQUEST
}

func newPresenceTracker() *PresenceTracker {
	return &PresenceTracker{
		pollers:  make(map[string]int),
		lastSeen: make(map[string]string),
		lastPoll: make(map[string]string),
	}
}

// AddPanelClient / RemovePanelClient track connected panel tabs (SSE
// connections) — owned by the SSE hub, ticket C2.
func (p *PresenceTracker) AddPanelClient() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.panelClients++
}

func (p *PresenceTracker) RemovePanelClient() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.panelClients > 0 {
		p.panelClients--
	}
}

// AddPoller / RemovePoller track active GET /poll long-poll requests per
// channel — owned by the legacy poll handler, ticket C1.
func (p *PresenceTracker) AddPoller(channel string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pollers[channel]++
}

func (p *PresenceTracker) RemovePoller(channel string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pollers[channel] > 0 {
		p.pollers[channel]--
		if p.pollers[channel] == 0 {
			delete(p.pollers, channel)
		}
	}
}

// Touch records channel as having been seen (most recent activity) at ts.
func (p *PresenceTracker) Touch(channel, ts string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastSeen[channel] = ts
}

// TouchPoll records that something ASKED this channel for messages at ts.
//
// It is deliberately not Touch: lastSeen answers "when did this channel last
// have something in it", which is a fact about messages, while this answers
// "when did anything last come looking", which is a fact about listeners.
// The second is what separates "nothing is listening" from "a listener is
// attached and took nothing" — PollChannels lists only channels with a live
// waiter, and a waiter that has just been served, or has just timed out, is
// momentarily absent, so a live listener can look exactly like no listener.
//
// Called on every poll request, whatever branch answers it (a served backlog
// and a parked long-poll are both a listener asking), so it is one map write
// under the same lock the poller counters already take: no I/O, no error, no
// new way for a delivery to fail.
func (p *PresenceTracker) TouchPoll(channel, ts string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastPoll[channel] = ts
}

// PollChannel is one entry of Snapshot.PollChannels.
type PollChannel struct {
	Channel string
	Count   int
}

// PresenceEntry is one entry of Snapshot.Presence.
type PresenceEntry struct {
	Channel  string
	LastSeen string
}

// PollActivityEntry is one entry of Snapshot.PollActivity: when anything last
// asked this channel for messages, and how many long-polls are parked on it
// right now. It covers every channel that has EVER been polled, which
// PollChannel (live waiters only) cannot, and it is a separate list rather
// than a wider PollChannel so /subscribers' documented shape is untouched.
type PollActivityEntry struct {
	Channel       string
	LastPoll      string
	ActivePollers int
}

// Snapshot is the current counters. Handlers (ticket C1) combine this with
// RegistryStore.List() to build the full SubscribersInfo response
// documented in docs/api-contract.md.
type Snapshot struct {
	PanelClients int
	PollCount    int
	PollChannels []PollChannel
	Presence     []PresenceEntry
	PollActivity []PollActivityEntry
}

func (p *PresenceTracker) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	total := 0
	channels := make([]PollChannel, 0, len(p.pollers))
	for ch, n := range p.pollers {
		total += n
		channels = append(channels, PollChannel{Channel: ch, Count: n})
	}
	presence := make([]PresenceEntry, 0, len(p.lastSeen))
	for ch, ts := range p.lastSeen {
		presence = append(presence, PresenceEntry{Channel: ch, LastSeen: ts})
	}

	// Sorted by channel: this list reaches a view and a test, and map order
	// would make both of them unstable for no reason.
	activity := make([]PollActivityEntry, 0, len(p.lastPoll))
	for ch, ts := range p.lastPoll {
		activity = append(activity, PollActivityEntry{Channel: ch, LastPoll: ts, ActivePollers: p.pollers[ch]})
	}
	sort.Slice(activity, func(i, j int) bool { return activity[i].Channel < activity[j].Channel })

	return Snapshot{
		PanelClients: p.panelClients,
		PollCount:    total,
		PollChannels: channels,
		Presence:     presence,
		PollActivity: activity,
	}
}
