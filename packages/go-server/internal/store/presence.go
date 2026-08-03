package store

import "sync"

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
	lastSeen     map[string]string // channel -> ISO timestamp
}

func newPresenceTracker() *PresenceTracker {
	return &PresenceTracker{
		pollers:  make(map[string]int),
		lastSeen: make(map[string]string),
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

// Snapshot is the current counters. Handlers (ticket C1) combine this with
// RegistryStore.List() to build the full SubscribersInfo response
// documented in docs/api-contract.md.
type Snapshot struct {
	PanelClients int
	PollCount    int
	PollChannels []PollChannel
	Presence     []PresenceEntry
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
	return Snapshot{
		PanelClients: p.panelClients,
		PollCount:    total,
		PollChannels: channels,
		Presence:     presence,
	}
}
