package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
)

// AgentInfo matches AgentInfo in docs/api-contract.md (§Agent registry /
// presence). Caps is arbitrary JSON forwarded from `parlay listen --caps`.
type AgentInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Color     string   `json:"color"`
	Nicknames []string `json:"nicknames,omitempty"`
	URLs      []string `json:"urls,omitempty"`
	Path      []string `json:"path,omitempty"`
	Caps      any      `json:"caps,omitempty"`
}

// RegistryStore is the agent registry: a full-snapshot JSON file
// (agents.json), atomically rewritten on every change. The registry is
// small (a handful of agents), so unlike MessageStore there's no case for
// an append-log / ring-buffer split here.
type RegistryStore struct {
	mu   sync.RWMutex
	path string
	byID map[string]AgentInfo
}

func openRegistryStore(path string) (*RegistryStore, error) {
	rs := &RegistryStore{path: path, byID: make(map[string]AgentInfo)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return rs, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var agents []AgentInfo
	if err := json.Unmarshal(data, &agents); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, a := range agents {
		rs.byID[a.ID] = a
	}
	return rs, nil
}

// Upsert inserts or updates an agent's registry entry, keyed by ID.
// register-agent is documented as idempotent and per-call-site
// partial (each caller sends only the fields it knows) — so an update only
// overwrites fields the caller actually sent (non-zero string fields,
// non-nil slices); it never blanks an existing field just because this call
// omitted it. nicknames is the one field the contract explicitly says an
// empty (non-nil) slice clears, which this preserves since nil is the only
// "omitted" sentinel here.
func (rs *RegistryStore) Upsert(agent AgentInfo) (AgentInfo, error) {
	if agent.ID == "" {
		return AgentInfo{}, fmt.Errorf("registry: id is required")
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()

	merged := agent
	if existing, ok := rs.byID[agent.ID]; ok {
		if merged.Name == "" {
			merged.Name = existing.Name
		}
		if merged.Color == "" {
			merged.Color = existing.Color
		}
		if merged.Nicknames == nil {
			merged.Nicknames = existing.Nicknames
		}
		if merged.URLs == nil {
			merged.URLs = existing.URLs
		}
		if merged.Path == nil {
			merged.Path = existing.Path
		}
		if merged.Caps == nil {
			merged.Caps = existing.Caps
		}
	}
	rs.byID[agent.ID] = merged
	if err := rs.saveLocked(); err != nil {
		return AgentInfo{}, err
	}
	return merged, nil
}

// Remove deregisters an agent. Returns false if the id was not registered —
// unregister's documented idempotent-no-op behavior for an unknown id.
func (rs *RegistryStore) Remove(id string) (bool, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, ok := rs.byID[id]; !ok {
		return false, nil
	}
	delete(rs.byID, id)
	if err := rs.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

// Get returns one agent's registry entry.
func (rs *RegistryStore) Get(id string) (AgentInfo, bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	a, ok := rs.byID[id]
	return a, ok
}

// List returns every registered agent, sorted by id for stable output.
func (rs *RegistryStore) List() []AgentInfo {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.snapshotLocked()
}

// snapshotLocked returns a sorted copy of the registry. Caller must hold
// at least a read lock.
func (rs *RegistryStore) snapshotLocked() []AgentInfo {
	out := make([]AgentInfo, 0, len(rs.byID))
	for _, a := range rs.byID {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// saveLocked rewrites agents.json from the current in-memory map. Caller
// must hold mu (write lock).
func (rs *RegistryStore) saveLocked() error {
	data, err := json.MarshalIndent(rs.snapshotLocked(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agents: %w", err)
	}
	if err := writeFileAtomic(rs.path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", rs.path, err)
	}
	return nil
}
