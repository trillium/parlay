package staleness

import (
	"fmt"
	"sort"
)

// DefaultAffectedBudget caps AffectedBy when the caller passes
// maxNodes <= 0. There is deliberately no way to request an unbounded
// pass: the budget is the cost model's hard stop, not a convenience.
const DefaultAffectedBudget = 4096

// IsStale reports transitive staleness: id is stale when it is directly
// stale or can reach a directly-stale record over declared dependency
// edges. Reads are never followed.
//
// Cost: each record reachable from id is visited at most once per call —
// O(V+E) of the reachable subgraph, worst case. The visited set is what
// makes cycles terminate and keeps diamonds from multiplying work; the
// graph is not required to be acyclic.
func (g *Graph) IsStale(id ID) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.records[id]; !ok {
		return false, fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	return g.staleLocked(id, make(map[ID]struct{})), nil
}

// staleLocked is a visited-once reachability DFS to a directly-stale
// record. Every reachable record is examined exactly once: the visited
// mark prunes only re-entries, so pruning never skips an unexamined
// record.
func (g *Graph) staleLocked(id ID, visited map[ID]struct{}) bool {
	if _, seen := visited[id]; seen {
		return false
	}
	visited[id] = struct{}{}
	rec := g.records[id]
	if g.directlyStaleLocked(rec) {
		return true
	}
	for up := range rec.deps {
		if g.staleLocked(up, visited) {
			return true
		}
	}
	return false
}

// Affected is the result of one propagation pass (AffectedBy).
type Affected struct {
	// IDs lists the dependents reachable from the changed record over
	// declared dependency edges (the record itself excluded), in
	// deterministic breadth-first order with sorted expansion.
	IDs []ID
	// Visited counts records examined, including the origin. It can
	// never exceed the budget.
	Visited int
	// Truncated reports that the budget stopped the pass before the
	// reachable set was exhausted. A truncated pass means "at least
	// these"; callers wanting the rest must ask again with a larger
	// budget — the pass never silently runs on.
	Truncated bool
}

// AffectedBy enumerates the records a change to id may invalidate: the
// reverse-reachable dependents over declared edges. It is topology, not
// a staleness verdict — pair each result with IsStale for that. This is
// the only cascade-shaped operation in the package, and it carries a
// hard node budget: maxNodes caps records examined (origin included);
// maxNodes <= 0 means DefaultAffectedBudget. Visited-once traversal
// terminates on cycles; the budget bounds cost even on graphs larger
// than the caller expected, reporting Truncated instead of running on.
func (g *Graph) AffectedBy(id ID, maxNodes int) (Affected, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.records[id]; !ok {
		return Affected{}, fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	if maxNodes <= 0 {
		maxNodes = DefaultAffectedBudget
	}

	visited := map[ID]struct{}{id: {}}
	out := Affected{Visited: 1}
	queue := []ID{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		// Sorted expansion: map iteration order is random, and a
		// deterministic custodian must not emit a random-order pass.
		next := make([]ID, 0, len(g.records[cur].rdeps))
		for d := range g.records[cur].rdeps {
			if _, seen := visited[d]; !seen {
				next = append(next, d)
			}
		}
		sort.Slice(next, func(i, j int) bool { return next[i] < next[j] })
		for _, d := range next {
			if out.Visited >= maxNodes {
				out.Truncated = true
				return out, nil
			}
			visited[d] = struct{}{}
			out.Visited++
			out.IDs = append(out.IDs, d)
			queue = append(queue, d)
		}
	}
	return out, nil
}
