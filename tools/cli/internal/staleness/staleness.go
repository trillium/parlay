// Package staleness implements parlay's representation-plane record
// staleness model (issue #128 §21–§24, §57): a record becomes stale when a
// record it explicitly depends on changes. Reads never carry staleness —
// only a declared dependency edge does (§23).
//
// Prior art — this package deliberately adopts the Dagster model: every
// record carries an opaque version, and every dependency edge carries a
// snapshot of the upstream version the dependent was last validated
// against ("acked"). Staleness is DERIVED by comparing acked versions to
// current versions; nothing is ever eagerly cascaded. A change is O(1) at
// write time, so unbounded propagation is structurally impossible, and
// Bazel-style early cutoff falls out for free: revalidating a record
// without moving its own version shields its entire downstream subgraph.
// Content-addressed invalidation (Bazel/Nix/Buck2) was considered and
// rejected: beads are not content-addressed artifacts, and #128 already
// makes versions first-class (§14, §17). Full rationale, termination
// argument, and cost table: docs/staleness-model.md.
//
// This is NOT the execution-plane agent/worktree staleness handled by
// `parlay stale` / `parlay sweep`; the two concepts share a word and
// nothing else.
//
// The graph is an in-memory, deterministic engine with no I/O and no
// inference, following the pattern of internal/routing. All methods are
// safe for concurrent use.
package staleness

import (
	"errors"
	"fmt"
	"sync"
)

// ID identifies a record (a bead, in #128 terms). Opaque to this package.
type ID string

// Version is an opaque version stamp. The engine compares versions only
// for equality; it never orders, parses, or interprets them. Semantic
// versioning policy (which changes are staleness-inducing) belongs to the
// caller — see the supersession seam in docs/staleness-model.md.
type Version string

// Errors returned by graph operations. All are wrapped with the offending
// ID; test with errors.Is.
var (
	ErrUnknownRecord  = errors.New("staleness: unknown record")
	ErrRecordExists   = errors.New("staleness: record already exists")
	ErrSelfDependency = errors.New("staleness: record cannot depend on itself")
)

// record is the per-ID state. deps maps each declared upstream to the
// version of that upstream this record last validated against. reads is
// provenance only: nothing in this package ever walks it when computing
// staleness — that is the #128 §23 guarantee, enforced by construction.
type record struct {
	version    Version
	lastReason string
	deps       map[ID]Version
	rdeps      map[ID]struct{}
	reads      map[ID]struct{}
}

// Graph holds records and their declared dependency edges.
type Graph struct {
	mu      sync.Mutex
	records map[ID]*record
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{records: make(map[ID]*record)}
}

// Add registers a record at an initial version. Adding an existing ID is
// an error: version moves must go through Bump so they carry a reason.
func (g *Graph) Add(id ID, v Version) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.records[id]; ok {
		return fmt.Errorf("%w: %s", ErrRecordExists, id)
	}
	g.records[id] = &record{
		version: v,
		deps:    make(map[ID]Version),
		rdeps:   make(map[ID]struct{}),
		reads:   make(map[ID]struct{}),
	}
	return nil
}

// Bump moves a record to a new version. reason is free-form provenance
// surfaced by Why (e.g. "edit", "superseded:major"); the engine never
// branches on it. Bumping to the identical version is a no-op — that is
// the "reran, produced the same output" case and must not disturb
// dependents. Cost: O(1); no traversal of any kind happens here.
func (g *Graph) Bump(id ID, to Version, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.records[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	if rec.version == to {
		return nil
	}
	rec.version = to
	rec.lastReason = reason
	return nil
}

// Version reports a record's current version.
func (g *Graph) Version(id ID) (Version, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.records[id]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	return rec.version, nil
}

// DeclareDependency declares that dependent depends on upstream, acking
// upstream's current version. Re-declaring an existing edge re-acks it.
// Only edges created here carry staleness; reads (RecordRead) never do.
// Cycles are permitted — termination is the traversal's job, not the
// writer's (see docs/staleness-model.md); only self-edges are rejected.
func (g *Graph) DeclareDependency(dependent, upstream ID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if dependent == upstream {
		return fmt.Errorf("%w: %s", ErrSelfDependency, dependent)
	}
	dep, ok := g.records[dependent]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRecord, dependent)
	}
	up, ok := g.records[upstream]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRecord, upstream)
	}
	dep.deps[upstream] = up.version
	up.rdeps[dependent] = struct{}{}
	return nil
}

// RemoveDependency deletes a declared edge. Removing an edge that does
// not exist is a no-op, provided both records exist.
func (g *Graph) RemoveDependency(dependent, upstream ID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	dep, ok := g.records[dependent]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRecord, dependent)
	}
	up, ok := g.records[upstream]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRecord, upstream)
	}
	delete(dep.deps, upstream)
	delete(up.rdeps, dependent)
	return nil
}

// RecordRead records that reader consulted read, as provenance only. Per
// #128 §23 a read is NOT a dependency: no method that computes staleness
// ever follows a read attachment, so reads can never pollute the
// dependency graph or carry a cascade.
func (g *Graph) RecordRead(reader, read ID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rd, ok := g.records[reader]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRecord, reader)
	}
	if _, ok := g.records[read]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRecord, read)
	}
	rd.reads[read] = struct{}{}
	return nil
}

// Reads returns the IDs this record has read attachments to, for
// observability. The slice is a copy in unspecified order.
func (g *Graph) Reads(id ID) ([]ID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.records[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	out := make([]ID, 0, len(rec.reads))
	for r := range rec.reads {
		out = append(out, r)
	}
	return out, nil
}

// Revalidate acks every declared edge of id at its upstream's current
// version, clearing id's direct staleness. It deliberately does NOT move
// id's own version: if revalidation actually produced a new output the
// caller must Bump — otherwise dependents of id stay fresh. That is the
// early-cutoff rule that stops cascades at the first node whose output
// did not change.
func (g *Graph) Revalidate(id ID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.records[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	for up := range rec.deps {
		rec.deps[up] = g.records[up].version
	}
	return nil
}

// DirectlyStale reports whether any declared edge of id has an acked
// version that differs from the upstream's current version. Cost:
// O(out-degree of id). Transitive staleness is IsStale.
func (g *Graph) DirectlyStale(id ID) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.records[id]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	return g.directlyStaleLocked(rec), nil
}

func (g *Graph) directlyStaleLocked(rec *record) bool {
	for up, acked := range rec.deps {
		if g.records[up].version != acked {
			return true
		}
	}
	return false
}
