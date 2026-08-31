package parlaybeads

import (
	"errors"
	"fmt"
)

// ErrNotFound marks a Get for an id the store does not have. Parlay-owned
// (not the beads library's sentinel) so a topology change cannot silently
// change what callers match on.
var ErrNotFound = errors.New("parlaybeads: bead not found")

// installPointer is the actionable half of every UnavailableError, per the
// Q5b contract: the error a verb dies with must tell the operator how to make
// the store exist, not just that it doesn't. Kept as one constant so every
// verb's failure text stays identical.
const installPointer = "parlay's crew store is a beads database (see docs/status-lift-topology.md); " +
	"it needs Dolt (`brew install dolt`) and either a CGO build of parlay (embedded mode) " +
	"or a running server (`bd init --server` in the store root)"

// UnavailableError is the Q5b named error: the store a verb needs cannot be
// reached. Verbs that need the store MUST surface this loudly (die with its
// message) and MUST NOT catch it to fall back to store-less behavior — a
// silent degrade is the failure mode this type exists to prevent.
//
// Missing distinguishes "the store directory does not exist" (an init-time
// problem — only Init creates stores) from "it exists but could not be
// opened" (a backend problem: Dolt absent, non-CGO build in embedded mode, a
// foreign backend in metadata.json, ...).
type UnavailableError struct {
	Dir     string // the beadsDir that was asked for
	Missing bool   // true when the directory itself does not exist
	Err     error  // the underlying open failure; nil when Missing
}

func (e *UnavailableError) Error() string {
	if e.Missing {
		return fmt.Sprintf("crew bead store unavailable: no store at %s — %s", e.Dir, installPointer)
	}
	return fmt.Sprintf("crew bead store unavailable at %s: %v — %s", e.Dir, e.Err, installPointer)
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// IsUnavailable reports whether err is (or wraps) the Q5b store-unavailable
// error, for callers that need to distinguish "no store" from an operation
// failure on a store that was reachable.
func IsUnavailable(err error) bool {
	var u *UnavailableError
	return errors.As(err, &u)
}
