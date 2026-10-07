// Narrowing and ordering: the query axes (agent, outcome, window) and the
// display order, kept apart from how an event was classified.
package timeline

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// orderEvents sorts chronologically, oldest first. An event whose stamp did
// not parse cannot be placed, so it sorts AFTER the timed ones rather than
// disappearing: the record is real evidence, and dropping it to keep the
// ordering tidy would be the same silent loss this package refuses.
func orderEvents(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.HasAt != b.HasAt {
			return a.HasAt
		}
		if !a.HasAt {
			return false // both unplaced: keep read order
		}
		return a.At.Before(b.At)
	})
}

// Window is the time filter. A zero Window matches everything.
type Window struct {
	Since, Until       time.Time
	HasSince, HasUntil bool
}

// Contains reports whether t falls in the window. Untimed events are the
// caller's problem (see Select).
func (w Window) Contains(t time.Time) bool {
	if w.HasSince && t.Before(w.Since) {
		return false
	}
	if w.HasUntil && t.After(w.Until) {
		return false
	}
	return true
}

// Filter is the whole narrowing surface: one agent (channel), a set of
// outcomes, and a window. Agent is exact — a fleet-wide read is the empty
// string, which is a different question, not a superset by accident.
type Filter struct {
	Agent    string
	Outcomes map[Outcome]bool
	Window   Window
}

// Select narrows events to those matching f and returns the newest limit of
// them in chronological order, plus how many matched in total so a caller can
// say "12 shown of 340 matching". limit <= 0 means all of them.
//
// A windowed read KEEPS events whose stamp did not parse and flags them: it
// cannot place them in the window, and dropping evidence because it cannot be
// dated is worse than reporting an undated record inside a dated question.
func Select(events []Event, f Filter, limit int) (kept []Event, matched int) {
	for _, e := range events {
		if f.Agent != "" && e.Agent != f.Agent {
			continue
		}
		if len(f.Outcomes) > 0 && !f.Outcomes[e.Outcome] {
			continue
		}
		if e.HasAt && !f.Window.Contains(e.At) {
			continue
		}
		matched++
		kept = append(kept, e)
	}
	if limit > 0 && len(kept) > limit {
		kept = kept[len(kept)-limit:]
	}
	if kept == nil {
		kept = []Event{}
	}
	return kept, matched
}

// Counts is how many of each outcome are present, for the header line. It is
// computed over the SELECTED events, so it describes what the reader is about
// to see rather than what exists.
func Counts(events []Event) []string {
	tally := map[Outcome]int{}
	var order []Outcome
	for _, e := range events {
		if tally[e.Outcome] == 0 {
			order = append(order, e.Outcome)
		}
		tally[e.Outcome]++
	}
	// Walk the closed vocabulary order so the summary is stable between runs
	// and between machines.
	out := make([]string, 0, len(order))
	for _, o := range Outcomes {
		if tally[o] > 0 {
			out = append(out, fmt.Sprintf("%s=%d", o, tally[o]))
		}
	}
	return out
}

func reasonOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func actorOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unrecorded"
	}
	return s
}

// FormatDuration renders an age for a human: two units at most, seconds up.
func FormatDuration(d time.Duration) string { return formatDuration(d) }

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// ParseStamp accepts the stamps these trails carry. Exported so the caller's
// --since/--until parsing and Build cannot disagree about what a valid stamp
// is.
func ParseStamp(s string) (time.Time, bool) { return parseStamp(s) }

func parseStamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
