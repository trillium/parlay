// Package parlaybeads is the crew-status seam's ONE client for parlay's own
// beads store — topology (d) of docs/status-lift-topology.md: the
// github.com/steveyegge/beads library imported directly, opened at a
// parlay-controlled beadsDir, no PAI federation, no gc binary, no running
// controller. Everything above this package talks Client; nothing above it
// imports the beads module, so a topology change is a one-package change.
//
// Two contracts every consumer inherits, both carried forward from the
// task-4cfpv.24 scope report (unit 1):
//
//   - Q5b — REFUSE LOUDLY. A verb that needs the store and cannot reach it
//     must die with the named *UnavailableError this package returns (it
//     carries the install pointer), never degrade silently into "no beads,
//     carry on". Open/Init never guess: a missing store is an
//     *UnavailableError from Open, and only Init may create one.
//
//   - Fail-open on closed-ness (identity/worklink.go BoundWorkItemClosed): a
//     lookup that merely FAILED is not evidence of a closed bead. Callers
//     gating destructive or suppressive behavior on "is it closed?" must use
//     AffirmativelyClosed, which returns true only on an affirmative closed
//     status from a successful read.
//
// This package is a LEAF as of unit 1: no verb is cut over to it, and today's
// status-file behavior is byte-identical with or without it. Units 3+ (gated
// on the spawn and events seams — report §6.4) are what start calling it.
package parlaybeads

import "context"

// Status values a crew bead can hold — parlay-owned names whose string values
// are byte-identical to the beads library's issue statuses, so a store written
// through this package reads back identically under `bd`.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusDeferred   = "deferred"
	StatusClosed     = "closed"
)

// Bead is the parlay-side view of one bead. It deliberately exposes only what
// the crew-status seam needs (units 3-5): identity, workflow status, the flat
// metadata map the crew schema (unit 2) writes its vocabulary into, and
// labels. Metadata values are strings by contract — the schema defines a flat
// string-to-string vocabulary — and a non-string JSON value written by a
// foreign tool reads back as its compact JSON text rather than erroring.
type Bead struct {
	ID          string
	Title       string
	Description string
	Status      string
	Type        string // beads issue type, e.g. "task"
	Assignee    string
	Labels      []string
	Metadata    map[string]string
	CloseReason string
}

// Closed reports whether the bead's status is affirmatively closed. On a Bead
// you hold, this is safe; to gate behavior on a bead you must first LOOK UP,
// go through AffirmativelyClosed instead (fail-open contract).
func (b Bead) Closed() bool { return b.Status == StatusClosed }

// Client is the one interface the crew-status seam programs against. All
// methods take a context because every backing store operation is I/O.
//
// Method errors: Get returns an error wrapping ErrNotFound for a missing id;
// every method can return the underlying store's error otherwise. None of
// them silently succeed on failure (Q5b's spirit applies to operations, not
// just Open).
type Client interface {
	// Create writes a new bead and returns its id. An empty b.ID asks the
	// store to generate one under the store's configured issue prefix.
	Create(ctx context.Context, b Bead) (id string, err error)

	// Get reads one bead by id. A missing id is an error wrapping ErrNotFound.
	Get(ctx context.Context, id string) (Bead, error)

	// MergeMetadata merges the given keys into the bead's metadata, leaving
	// keys not named here untouched (per-key atomic merge, not replace).
	MergeMetadata(ctx context.Context, id string, meta map[string]string) error

	// SetStatus moves the bead to the given workflow status (one of the
	// Status* constants). Closing goes through CloseBead, which records a
	// reason.
	SetStatus(ctx context.Context, id, status string) error

	// CloseBead closes the bead with a reason.
	CloseBead(ctx context.Context, id, reason string) error

	// ListByLabel returns every bead carrying the label.
	ListByLabel(ctx context.Context, label string) ([]Bead, error)

	// Close releases the underlying store handle. Not related to CloseBead.
	Close() error
}
