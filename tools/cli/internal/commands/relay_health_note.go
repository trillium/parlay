// The one line every surface prints about a LIVE relay's own bindings.
//
// The bug this exists to remove: liveness and timeline built that line by
// string concatenation, so a relay whose /health does not carry the server and
// runtime fields — an older build answers {"ok":true} alone — printed
//
//	up — polling , runtime
//
// which reads as two measured facts, both empty. It is not a fact at all: the
// relay was asked who it polls and did not say. `explain` already treated the
// empty values as unknown; the other two did not, so the same running relay was
// described two different ways by two commands an operator uses together.
//
// Nothing here infers a mismatch from silence: the same-server warning belongs
// to the caller, which can only be raised when BOTH urls are comparable.
package commands

import (
	"fmt"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
)

// relayHealthNote renders what a live relay reports about itself, always
// naming the half it did not report. The returned string is the tail of an
// "up — …" line, so callers that append a warning keep composing it the same
// way they always did.
func relayHealthNote(h relayctl.Health) string {
	note := fmt.Sprintf("polling %s, runtime %s", orUnknown(h.Server), orUnknown(h.Runtime))
	switch {
	case h.Server == "" && h.Runtime == "":
		note += " — this relay's /health reported neither, so which server it polls is UNKNOWN, not a mismatch"
	case h.Server == "":
		note += " — this relay's /health did not report which server it polls, so whether it polls this server is UNKNOWN, not a mismatch"
	case h.Runtime == "":
		note += " — this relay's /health did not report its runtime dir"
	}
	return note
}
