// The delivery half of `parlay explain`: the relay's data-plane trail as this
// command can see it, and the one decision that keeps the 2am answer alive —
// when the relay does not answer, read the LEDGER FROM DISK.
//
// The gap this closes: explain read the trail only over the relay's control
// socket, so a relay that had died (usually the reason someone is asking)
// printed "delivery unknown — the relay did not answer" for a trail that was
// sitting on disk the whole time. `parlay timeline` already reads both
// generations of that file for exactly this reason; explain does now too, and
// names which source the rows came from.
//
// The socket is still asked FIRST, because it is the only source that can say
// two things a file cannot: whether recording is switched OFF right now
// (PARLAY_RELAY_DELIVERY_LOG=0), and — through /health — which chat server the
// relay is polling. The file fallback never pretends to know either; it says
// so on the line.
package commands

import (
	"fmt"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
)

// explainDelivery is the relay's delivery trail as explain renders it: ONE
// shape for both sources, so the renderer cannot grow a second, subtly
// different vocabulary for the same evidence. Socket says which source
// produced Entries, because a reader has to know that "recording is switched
// off" is unanswerable from a file.
type explainDelivery struct {
	Socket  bool // the live relay answered GET /delivery
	Enabled bool // socket only: is the ledger recording RIGHT NOW
	// Exists: there is a ledger to read — the socket's own `exists` flag, or,
	// on the file path, a generation that was read OR refused to be read. A
	// ledger that does not exist is not an answer about this agent, which is
	// what keeps the nothing-observable exit code honest now that a file
	// fallback exists.
	Exists  bool
	Path    string
	Entries []relayctl.DeliveryEntry

	// File-only coverage. Rotation, truncation, corrupt lines and a generation
	// that refused to open are what make a SHORT trail different from a quiet
	// one, so they travel with the rows instead of being recomputed by the
	// renderer. Err/RotatedErr are set only when that generation was unreadable.
	FromRotated int // of the shown rows, how many came from the rotated generation
	Corrupt     int
	Truncated   bool
	Err         error
	RotatedErr  error
}

// deliverySocket wraps the live relay's answer for this agent.
func deliverySocket(d relayctl.Delivery) *explainDelivery {
	return &explainDelivery{
		Socket:  true,
		Enabled: d.Enabled,
		Exists:  d.Exists,
		Path:    d.Ledger,
		Entries: d.Entries,
	}
}

// deliveryOnDisk is the fallback: the ledger file itself, both generations,
// oldest first, narrowed to this agent.
//
// The agent filter mirrors the relay's own GET /delivery?agent= (which filters
// first and only then keeps the newest explainEventTail lines), so the two
// sources pick the same rows for the same trail. A `rotated` marker is kept
// even though it names no agent: it is the only evidence that everything older
// is GONE, and without it a partial trail reads as a quiet fleet.
//
// `started` is deliberately NOT kept: it is the relay process's own event, it
// says nothing about this channel, and every agent's screen carrying every
// restart of the box's relay would be noise of exactly the kind a reader learns
// to skip. `parlay timeline --outcome started,resumed` is that query.
func deliveryOnDisk(agentID string) *explainDelivery {
	l := relayctl.ReadLedger()
	d := &explainDelivery{
		Path:       l.Path,
		Corrupt:    l.Corrupt,
		Truncated:  l.Truncated,
		Err:        l.Err,
		RotatedErr: l.RotatedErr,
	}
	switch l.State {
	case relayctl.TrailRead, relayctl.TrailUnreadable:
		d.Exists = true
	}
	switch l.RotatedState {
	case relayctl.TrailRead, relayctl.TrailUnreadable:
		d.Exists = true
	}

	keep := func(e relayctl.DeliveryEntry) bool {
		return e.Agent == agentID || e.Event == "rotated"
	}
	rotated, active := l.Entries[:l.FromRotated], l.Entries[l.FromRotated:]
	for _, e := range rotated {
		if keep(e) {
			d.Entries = append(d.Entries, e)
			d.FromRotated++
		}
	}
	for _, e := range active {
		if keep(e) {
			d.Entries = append(d.Entries, e)
		}
	}
	if n := len(d.Entries); n > explainEventTail {
		d.Entries = d.Entries[n-explainEventTail:]
		d.FromRotated = 0 // the tail can start past the rotation boundary
	}
	return d
}

// unreadable is the failure that made this trail unreadable, if any: whichever
// generation refused, the contents are unknown rather than empty.
func (d explainDelivery) unreadable() error {
	if d.Err != nil {
		return d.Err
	}
	return d.RotatedErr
}

// coverage names what the file read could NOT cover, appended to the header of
// a non-empty on-disk trail. An empty string means the rows below are the whole
// trail for this agent.
func (d explainDelivery) coverage() string {
	parts := []string{}
	if d.FromRotated > 0 {
		parts = append(parts, fmt.Sprintf("%d of them from the generation before the last rotation (older history is in %s)",
			d.FromRotated, relayctl.LedgerPathRotated()))
	}
	// A generation that refused to be read while the OTHER one answered still
	// leaves rows to show — but the trail is short, and saying so is the whole
	// point. Only when nothing was readable at all does the caller print the
	// "contents unknown" line instead.
	if d.RotatedErr != nil {
		parts = append(parts, fmt.Sprintf("the previous generation (%s) could not be read (%v), so older history is unknown",
			relayctl.LedgerPathRotated(), d.RotatedErr))
	} else if d.Err != nil {
		parts = append(parts, fmt.Sprintf("the active ledger could not be read in full (%v), so this may be short", d.Err))
	}
	if d.Truncated {
		parts = append(parts, "TRUNCATED: the file exceeded this reader's cap, so only its newest bytes were read")
	}
	if d.Corrupt > 0 {
		parts = append(parts, fmt.Sprintf("%d corrupt line(s) skipped", d.Corrupt))
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}
