package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ChannelStore holds the session_id → channel map that POST
// /api/chat/declare-channel writes.
//
// WHY THIS EXISTS: processing signals (hook firings, tool activity) are
// stamped with the Claude Code session_id that produced them, never with a
// Parlay channel. Routing that processing into the right agent's tab needs
// session_id → channel, and an explicit declaration is the deterministic way
// to get it — no text-parsing, and it survives a server restart without the
// agent having to re-arm a monitor first. See the same rationale at length in
// packages/server/src/session-channel.ts.
//
// DECLARATIONS ARE STICKY — first one wins. An agent's identity is set at
// spawn. A later declaration of a DIFFERENT channel for the same session is
// that agent watching another channel (relay, orchestration), not it becoming
// that agent; honoring it would mis-route one agent's turns onto another's
// tab. The TS server learned this the hard way (firstmate's turns landing on
// edgar's tab after it monitored edgar), so this port keeps the same rule.
//
// The TS server keeps two layers here — this JSON file as primary and
// tool-activity.jsonl scanning as fallback. Only the primary is ported: the
// Go server has no tool-activity tailer, and inventing one to back a fallback
// nobody has asked for would be scope this ticket does not have.
type ChannelStore struct {
	mu      sync.RWMutex
	path    string
	byOwner map[string]string
}

func openChannelStore(path string) (*ChannelStore, error) {
	cs := &ChannelStore{path: path, byOwner: map[string]string{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cs, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cs.byOwner); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cs.byOwner == nil { // a literal `null` in the file unmarshals to a nil map
		cs.byOwner = map[string]string{}
	}
	return cs, nil
}

// Declare records sessionID → channel and returns the channel that is in
// effect afterward, which is the FIRST channel ever declared for that session
// — see the stickiness note on ChannelStore. A re-declaration that agrees with
// the existing mapping is a no-op rather than a rewrite, so the common case of
// an agent re-declaring on every reconnect does not touch the disk.
//
// Returns an error only if the mapping changed and could not be persisted; the
// in-memory map is left consistent with what is on disk in that case.
func (cs *ChannelStore) Declare(sessionID, channel string) (string, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if existing, ok := cs.byOwner[sessionID]; ok {
		return existing, nil
	}

	cs.byOwner[sessionID] = channel
	data, err := json.MarshalIndent(cs.byOwner, "", "  ")
	if err != nil {
		delete(cs.byOwner, sessionID)
		return "", fmt.Errorf("marshal channels: %w", err)
	}
	if err := writeFileAtomic(cs.path, append(data, '\n'), 0o644); err != nil {
		delete(cs.byOwner, sessionID)
		return "", fmt.Errorf("write %s: %w", cs.path, err)
	}
	return channel, nil
}

// ChannelFor returns the channel declared for sessionID, if any.
func (cs *ChannelStore) ChannelFor(sessionID string) (string, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	ch, ok := cs.byOwner[sessionID]
	return ch, ok
}

// All returns a copy of every declaration.
func (cs *ChannelStore) All() map[string]string {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	out := make(map[string]string, len(cs.byOwner))
	for k, v := range cs.byOwner {
		out[k] = v
	}
	return out
}
