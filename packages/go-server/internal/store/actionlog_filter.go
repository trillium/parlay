package store

import (
	"sort"
	"strings"
	"time"
)

// The QUERY half of the command log: listing, the filter matcher, and the facet
// vocabulary. Split from actionlog.go (which owns the record and the ring) so
// neither file has to be read to understand the other.

// List returns the records matching f, newest first — the order every renderer
// reads in, and the order a human reads a log in.
func (al *ActionLog) List(f ActionLogFilter) []ActionRecord {
	al.mu.RLock()
	snapshot := append([]ActionRecord(nil), al.records...)
	al.mu.RUnlock()

	// The ring is stored oldest-first, in APPEND order, so walking it backwards is
	// the newest-first order every renderer wants — without a comparison.
	//
	// Sorting by the formatted At string instead would be wrong: time.RFC3339Nano
	// drops trailing zeros from the fractional second, so lexical order is not
	// chronological order. `12:00:00Z` compares GREATER than `12:00:00.1Z`
	// because 'Z' (0x5A) > '.' (0x2E), which would sort a record stamped on an
	// exact second as newer than a later one. A clock step backwards is the same
	// hazard from the other side. Insertion order has neither problem.
	out := make([]ActionRecord, 0, len(snapshot))
	for i := len(snapshot) - 1; i >= 0; i-- {
		if f.matches(snapshot[i]) {
			out = append(out, snapshot[i])
		}
	}
	return out
}

// matches applies every non-zero filter field. Time bounds are parsed from the
// record's own At so a caller never has to compare formatted strings.
func (f ActionLogFilter) matches(rec ActionRecord) bool {
	if f.Source != "" && !strings.EqualFold(f.Source, rec.Source) {
		return false
	}
	if f.InputAction != "" && !strings.EqualFold(f.InputAction, rec.InputAction) {
		return false
	}
	if f.Outcome != "" && !strings.EqualFold(f.Outcome, rec.Outcome) {
		return false
	}
	if f.Reason != "" && !strings.EqualFold(f.Reason, rec.Reason) {
		return false
	}
	if f.Device != "" && !strings.EqualFold(f.Device, rec.Device) {
		return false
	}
	if f.OutputAction != "" && !containsFold(rec.OutputActions, f.OutputAction) {
		return false
	}
	if !f.Since.IsZero() || !f.Until.IsZero() {
		at, err := time.Parse(time.RFC3339Nano, rec.At)
		if err != nil {
			// An unparseable stamp cannot be placed in a window; a windowed
			// query must not silently include it.
			return false
		}
		if !f.Since.IsZero() && at.Before(f.Since) {
			return false
		}
		if !f.Until.IsZero() && at.After(f.Until) {
			return false
		}
	}
	return true
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// Facets is the log's own filter vocabulary: every distinct value actually
// present, per axis, so a renderer offers choices instead of a free-text box
// that can only ever be right by luck. Each slice is sorted.
func (al *ActionLog) Facets() ActionLogFacets {
	al.mu.RLock()
	snapshot := append([]ActionRecord(nil), al.records...)
	al.mu.RUnlock()

	var acc = facetAccumulator{
		sources:       map[string]bool{},
		inputActions:  map[string]bool{},
		outputActions: map[string]bool{},
		outcomes:      map[string]bool{},
		reasons:       map[string]bool{},
		devices:       map[string]bool{},
	}
	for _, rec := range snapshot {
		acc.sources[rec.Source] = true
		acc.inputActions[rec.InputAction] = true
		acc.outcomes[rec.Outcome] = true
		acc.reasons[rec.Reason] = true
		acc.devices[rec.Device] = true
		for _, v := range rec.OutputActions {
			acc.outputActions[v] = true
		}
	}
	return ActionLogFacets{
		Sources:       sortedKeys(acc.sources),
		InputActions:  sortedKeys(acc.inputActions),
		OutputActions: sortedKeys(acc.outputActions),
		Outcomes:      sortedKeys(acc.outcomes),
		Reasons:       sortedKeys(acc.reasons),
		Devices:       sortedKeys(acc.devices),
	}
}

// facetAccumulator is the set-valued half of Facets: distinct values are
// collected here and only turned into the presentation slices once, at the end.
// The empty string is never a facet — "no value" is the absence of a filter,
// not a choice a filter bar should offer.
type facetAccumulator struct {
	sources       map[string]bool
	inputActions  map[string]bool
	outputActions map[string]bool
	outcomes      map[string]bool
	reasons       map[string]bool
	devices       map[string]bool
}

// ActionLogFacets is the distinct-value vocabulary of one log, per filter axis.
type ActionLogFacets struct {
	Sources       []string `json:"sources"`
	InputActions  []string `json:"inputActions"`
	OutputActions []string `json:"outputActions"`
	Outcomes      []string `json:"outcomes"`
	Reasons       []string `json:"reasons"`
	Devices       []string `json:"devices"`
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// itoaSmall renders a small non-negative counter without importing strconv into
// this file's dependencies; the counter is process-local and bounded by the ring.
func itoaSmall(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
