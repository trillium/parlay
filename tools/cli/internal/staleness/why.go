package staleness

import (
	"fmt"
	"sort"
)

// Cause explains one directly-stale edge that makes a record stale: which
// record holds the edge, which upstream moved, from what acked version to
// what current version, why the upstream last moved, and the dependency
// path from the queried record to the edge holder.
type Cause struct {
	// Record holds the stale edge. Equal to the queried record for a
	// direct cause; a downstream-of-the-damage intermediate otherwise.
	Record ID
	// Upstream is the edge's target whose current version no longer
	// matches what Record last validated against.
	Upstream ID
	// Acked is the upstream version Record last validated against.
	Acked Version
	// Current is the upstream's version now.
	Current Version
	// Reason is the free-form provenance string from the upstream's
	// last Bump (e.g. "edit", "superseded:major"). Empty when the
	// upstream has never been bumped since Add.
	Reason string
	// Path is the declared-dependency path from the queried record to
	// Record, inclusive of both. For a direct cause it is [queried].
	// On a diamond, a record reachable via several paths is examined
	// once and reports the first path in deterministic order.
	Path []ID
}

// Why answers "why is this record stale, and what edge made it so": every
// directly-stale edge reachable from id over declared dependency edges,
// in deterministic depth-first order with sorted expansion. An empty
// result means the record is fresh — Why(id) is non-empty exactly when
// IsStale(id) is true. Reads never appear: they are never walked.
//
// Cost: visited-once traversal, O(V+E) of the reachable subgraph worst
// case; at most one Cause per declared edge. Terminates on cycles.
func (g *Graph) Why(id ID) ([]Cause, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.records[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	var causes []Cause
	g.whyLocked(id, []ID{id}, map[ID]struct{}{}, &causes)
	return causes, nil
}

// whyLocked walks depth-first from id, path being the dependency chain
// from the original query down to and including id. Each record is
// examined once; its stale edges are emitted as causes and its upstreams
// recursed into.
func (g *Graph) whyLocked(id ID, path []ID, visited map[ID]struct{}, causes *[]Cause) {
	if _, seen := visited[id]; seen {
		return
	}
	visited[id] = struct{}{}
	rec := g.records[id]

	ups := make([]ID, 0, len(rec.deps))
	for up := range rec.deps {
		ups = append(ups, up)
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i] < ups[j] })

	for _, up := range ups {
		upRec := g.records[up]
		if acked := rec.deps[up]; upRec.version != acked {
			*causes = append(*causes, Cause{
				Record:   id,
				Upstream: up,
				Acked:    acked,
				Current:  upRec.version,
				Reason:   upRec.lastReason,
				Path:     append([]ID(nil), path...),
			})
		}
		g.whyLocked(up, append(path, up), visited, causes)
	}
}
