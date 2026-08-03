// Package robotswatch implements `parlay robots-watch` and `parlay
// robots-tail` — the MVP event poll-daemon (decision-4zr interim bridge)
// plus its PUSH fast-path tailer.
//
// Ported from packages/cli/src/commands-robots-watch/{index,detect,handlers,
// cursor,tail}.ts (ticket B6). The durable design (docs/CLI_VERBS_AND_EVENTS.md
// §2.4) is: beads owns EMIT (an app-blind on-status-change hook), parlay owns
// SUBSCRIBE+ROUTE+DELIVER. Until the beads EMIT hook exists, robots-watch
// STANDS IN for the missing emit by polling each watched store's
// `<store> list --all --json`, diffing a persisted per-bead status cursor,
// and routing each detected (store, status-change) through a handler table.
// robots-tail is the separate PUSH fast path: a byte-offset tailer of the
// robots emit stream for sub-~1s create→dispatch latency, with robots-watch
// staying the reconciler fallback for anything it misses.
//
// This file (detect.go, from detect.ts) is the pure event model + diff core:
// no I/O, so it stays trivially unit-testable.
package robotswatch

import (
	"regexp"
	"sort"
	"strings"
)

// BeadStatus is a bd store's raw status string (open, in_progress, closed, …).
type BeadStatus = string

// StoreState is one store's bead-id → status snapshot.
type StoreState map[string]BeadStatus

// EventKind is a status transition robots-watch can fire a handler for.
type EventKind string

const (
	EventCreated EventKind = "created"
	EventClosed  EventKind = "closed"
)

// RouteEvent is one detected (store, transition) to route to a handler.
type RouteEvent struct {
	Store  string
	Kind   EventKind
	ID     string
	Status BeadStatus
}

// Bead is one bead as a store's `list --json` returns it (only the fields
// robots-watch reads).
type Bead struct {
	ID     string   `json:"id"`
	Status string   `json:"status,omitempty"`
	Title  string   `json:"title,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

// isClosed reports whether status is bd's terminal status. Everything else
// (open/in_progress/blocked) is "live".
func isClosed(status BeadStatus) bool {
	return status == "closed"
}

// detectEvents compares the PREVIOUS status map for a store (nil = never
// seen → SEED) against the CURRENT one and returns the events to fire for
// the requested kinds.
//   - SEED (prev nil): fire nothing; caller adopts curr. No history replay.
//   - created: a bead present now, absent before, and NOT already closed.
//   - closed: a bead we previously saw LIVE that is now closed (open→closed).
//
// A bead that first appears already-closed fires neither (history, not a
// transition we witnessed). Events are returned in bead-id sorted order —
// TS iterates Object.entries in insertion order, which is not a semantic
// guarantee here, so a deterministic order is used instead of Go's
// randomized map iteration.
func detectEvents(prev StoreState, curr StoreState, store string, kinds []EventKind) (events []RouteEvent, seeded bool) {
	if prev == nil {
		return nil, true
	}
	want := make(map[EventKind]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	ids := make([]string, 0, len(curr))
	for id := range curr {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		status := curr[id]
		before, existed := prev[id]
		if want[EventCreated] && !existed && !isClosed(status) {
			events = append(events, RouteEvent{Store: store, Kind: EventCreated, ID: id, Status: status})
		}
		if want[EventClosed] && existed && !isClosed(before) && isClosed(status) {
			events = append(events, RouteEvent{Store: store, Kind: EventClosed, ID: id, Status: status})
		}
	}
	return events, false
}

// notifyChannels parses the requester channel(s) a bead subscribes for
// close-notification: a `notify:<channel>` label. This label IS the
// lightweight SUBSCRIBE of decision-4zr — the bead names who to wake;
// agent/channel knowledge stays in parlay. A bead with no notify: label has
// no subscriber and is skipped.
func notifyChannels(labels []string) []string {
	out := []string{}
	for _, l := range labels {
		m := notifyLabelRe.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		c := strings.TrimSpace(m[1])
		if c == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

var notifyLabelRe = regexp.MustCompile(`^notify:(.+)$`)
