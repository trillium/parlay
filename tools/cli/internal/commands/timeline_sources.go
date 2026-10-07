// Gathering for `parlay timeline`: every read it performs, each one recorded
// as a source note so the renderer can name what did and did not answer.
//
// The shape of this file is the point. A timeline is assembled from records
// that live in four different places with four different availability models —
// two files the relay wrote (which survive the relay dying), one local socket
// (which does not), and one HTTP route on the chat server (which may be an
// older build, or not running at all). The relay's trails are read off disk
// PRECISELY so that the answer to "what happened" does not depend on the relay
// being alive to be asked, which is the situation an operator is usually in.
package commands

import (
	"fmt"
	"sort"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/timeline"
)

// timelineSpoolCap bounds how many agent spools one timeline read opens. The
// per-message "is it still queued?" answer needs the spool, and a fleet-wide
// read of a thousand agents would be a thousand file scans for a question
// nobody asked; past the cap the answer is unknown-with-a-reason instead of
// slow.
const timelineSpoolCap = 64

// sourceNote is one line of the sources footer: which record, what state it
// came back in, and the sentence that says it. State is a closed vocabulary so
// a --json consumer can branch on it without parsing prose.
type sourceNote struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
}

// Source states.
const (
	srcRead        = "read"        // the record answered, possibly with nothing in it
	srcAbsent      = "absent"      // the record has never been written
	srcUnreadable  = "unreadable"  // it exists and could not be read
	srcUnreachable = "unreachable" // the thing to ask is not running
	srcUnsupported = "unsupported" // it answered 404: too old to have this record
	srcOff         = "off"         // recording is switched off for the trail
	srcUnconfirmed = "unconfirmed" // only a live socket could say; it did not answer
)

// timelineGather is everything the renderer is allowed to know. Keeping the
// gather and the render apart is what makes each degraded mode testable
// without standing up a fleet.
type timelineGather struct {
	Server  string
	Runtime string

	Records  timeline.Records
	Presence map[string]timeline.Presence
	Sources  []sourceNote

	// answered is the exit-code predicate: did ANY record answer? A record
	// that was read and had nothing in it counts. A missing file does not —
	// "nothing to report" and "nothing was observable" are different exits.
	answered bool
}

// gatherTimeline performs every read once and never fails: each source that
// cannot be reached leaves its records empty and its source note saying so.
func gatherTimeline(agentFilter string) timelineGather {
	g := timelineGather{
		Server:   config.ServerURL(),
		Runtime:  relayctl.RuntimeDir(),
		Presence: map[string]timeline.Presence{},
	}

	// --- the two durable trails (files, so they outlive the relay) ---
	ledger := relayctl.ReadLedger()
	g.Records.Delivery = make([]timeline.DeliveryRecord, 0, len(ledger.Entries))
	for i, e := range ledger.Entries {
		g.Records.Delivery = append(g.Records.Delivery, timeline.DeliveryRecord{
			Entry:   e,
			Rotated: i < ledger.FromRotated,
		})
	}
	g.Sources = append(g.Sources, ledgerNote(ledger))
	if ledger.RotatedState == relayctl.TrailRead {
		g.Sources = append(g.Sources, rotatedNote(ledger))
	}
	if ledger.State == relayctl.TrailRead || ledger.RotatedState == relayctl.TrailRead {
		g.answered = true
	}

	audit := relayctl.ReadAudit()
	g.Records.Audit = audit.Entries
	g.Sources = append(g.Sources, auditNote(audit))
	if audit.State == relayctl.TrailRead {
		g.answered = true
	}

	// --- the relay control socket: the only thing that can say whether
	// recording is switched off right now, and which server this relay polls ---
	relayNote := sourceNote{Name: "relay control socket", State: srcUnreachable,
		Detail: fmt.Sprintf("no answer at %s — the relay is not running (or uses another runtime dir). The trails above are files and were still read; only the relay's live state is unknown", relayctl.SockPath())}
	if h, ok := relayctl.ReadHealth(); ok {
		g.answered = true
		if d, ok := relayctl.ReadDelivery(1, agentFilter); ok && !d.Enabled {
			g.Sources = append(g.Sources, sourceNote{Name: "delivery recording", State: srcOff, Path: d.Ledger,
				Detail: "PARLAY_RELAY_DELIVERY_LOG=0 in the relay's environment — it is recording nothing now, so any events below predate the switch-off"})
		}
		relayNote = sourceNote{Name: "relay control socket", State: srcRead,
			Detail: "up — polling " + h.Server + ", runtime " + h.Runtime}
		if equal, comparable := sameServerURL(h.Server, g.Server); comparable && !equal {
			relayNote.Detail += fmt.Sprintf(" · WARNING this relay polls %s, NOT the server this CLI targets (%s): nothing sent to %s reaches this relay", h.Server, g.Server, g.Server)
		}
	}
	g.Sources = append(g.Sources, relayNote)

	// --- the command registry (server) ---
	resp, state, detail := fetchTimelineCommands()
	switch state {
	case srcRead:
		g.answered = true
		g.Records.Commands = resp.Commands
	}
	g.Sources = append(g.Sources, sourceNote{Name: "command registry", State: state, Detail: detail})

	g.Presence = gatherSpoolPresence(g.Records.Delivery)
	return g
}

// gatherSpoolPresence reads the spool of each agent this trail mentions, which
// is what turns "spooled" into "still queued". It is bounded and it is
// per-agent, so one unreadable spool degrades exactly that agent's events.
func gatherSpoolPresence(recs []timeline.DeliveryRecord) map[string]timeline.Presence {
	out := map[string]timeline.Presence{}
	agents := map[string]bool{}
	for _, r := range recs {
		if r.Entry.Agent != "" {
			agents[r.Entry.Agent] = true
		}
	}
	names := make([]string, 0, len(agents))
	for a := range agents {
		names = append(names, a)
	}
	sort.Strings(names)

	for i, agent := range names {
		if i >= timelineSpoolCap {
			out[agent] = timeline.Presence{Reason: fmt.Sprintf(
				"this trail mentions %d agents and one timeline read reconciles at most %d spools; narrow with --agent to get a per-message answer",
				len(names), timelineSpoolCap)}
			continue
		}
		out[agent] = presenceFor(agent)
	}
	return out
}

// presenceFor inspects one spool. A missing spool is NOT "the message left":
// a spool can be removed (a fresh registration deletes the tombstone) and
// every agent reading this way is a runtime-dir mismatch, so it is unknown
// with the reason attached.
func presenceFor(agent string) timeline.Presence {
	if agent == "" {
		return timeline.Presence{Reason: "no agent named by the trail to look up"}
	}
	s := relayctl.SpoolMessageIDs(agent)
	switch {
	case s.Err != nil:
		return timeline.Presence{Reason: fmt.Sprintf("unreadable (%v)", s.Err)}
	case !s.Exists:
		return timeline.Presence{Reason: fmt.Sprintf("no spool at %s — never created here, or removed; if every agent reads this way, the relay and this CLI may be using different runtime dirs", s.Path)}
	default:
		return timeline.Presence{Known: true, IDs: s.IDs, Retired: s.Retired, Truncated: s.Truncated}
	}
}
