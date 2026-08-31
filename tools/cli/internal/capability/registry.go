// The connection registry: declarations keyed by connection id, living
// exactly as long as their connection (docs/interface-capabilities.md
// "Negotiation mechanics"). A declaration can never outlive its surface —
// registration is deregistered by the same teardown that closes the stream,
// so there is nothing to sweep and no staleness to reconcile.
package capability

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds the live declarations and the suppression counters. It is
// concurrency-safe and does no I/O — the transport owns persistence-free
// connection state; this owns the decisions about it.
type Registry struct {
	mu         sync.Mutex
	byID       map[string]*Declaration
	suppressed map[string]int // event name → suppressed deliveries
}

func NewRegistry() *Registry {
	return &Registry{
		byID:       map[string]*Declaration{},
		suppressed: map[string]int{},
	}
}

// Register validates and stores one connection's declaration. Rejecting
// here is what makes the fail-loud contract hold at the seam: a caller
// that cannot Register must refuse the connection, not proceed undeclared.
// Re-registering an id replaces its declaration (a reconnect reusing a
// connection id is a new negotiation).
func (r *Registry) Register(id string, d *Declaration) error {
	if id == "" {
		return fmt.Errorf("capability registry: empty connection id")
	}
	if d == nil {
		return fmt.Errorf("capability registry: nil declaration (an undeclared client is simply never registered)")
	}
	if err := d.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[id] = d
	return nil
}

// Deregister forgets a connection. Unknown ids are a no-op: teardown paths
// fire best-effort and must be idempotent.
func (r *Registry) Deregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
}

// Get returns the declaration for a connection, nil when undeclared.
func (r *Registry) Get(id string) *Declaration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[id]
}

// Decide applies the delivery gate for one connection and one event,
// counting suppressions — a silent gate would be indistinguishable from a
// gate that never runs (the presenceBroadcasts precedent).
func (r *Registry) Decide(id, event string) Decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := Decide(r.byID[id], event)
	if d.Verdict == VerdictSuppress {
		r.suppressed[event]++
	}
	return d
}

// Suppressed returns a copy of the per-event suppression counters.
func (r *Registry) Suppressed() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.suppressed))
	for k, v := range r.suppressed {
		out[k] = v
	}
	return out
}

// Entry is one registered declaration in a Snapshot.
type Entry struct {
	ID          string       `json:"id"`
	Declaration *Declaration `json:"declaration"`
}

// Snapshot lists the registered declarations sorted by connection id, for
// the observability surface (/api/chat/subscribers).
func (r *Registry) Snapshot() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, 0, len(r.byID))
	for id, d := range r.byID {
		out = append(out, Entry{ID: id, Declaration: d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
