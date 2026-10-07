package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The ring is bounded, and it sheds the OLDEST record first — a log that
// dropped its newest entries would be a log nobody could read the end of.
func TestActionLogRingDropsOldestFirst(t *testing.T) {
	al := NewActionLog(ActionLogConfig{MaxRecords: 3})
	for _, id := range []string{"a", "b", "c", "d"} {
		al.Append(ActionRecord{Source: id, Outcome: OutcomeDelivered})
	}
	got := al.List(ActionLogFilter{})
	if len(got) != 3 {
		t.Fatalf("ring holds %d records, want 3", len(got))
	}
	// Newest first.
	for i, want := range []string{"d", "c", "b"} {
		if got[i].Source != want {
			t.Errorf("record %d source = %q, want %q", i, got[i].Source, want)
		}
	}
}

// Every filter axis the spec names must actually narrow the list, and an
// unknown value must narrow it to nothing rather than being ignored.
func TestActionLogFilterAxes(t *testing.T) {
	al := NewActionLog(ActionLogConfig{})
	al.Append(ActionRecord{Source: "test-site", Device: "d1", InputAction: "clear", OutputActions: []string{"clear"}, Outcome: OutcomeDelivered})
	al.Append(ActionRecord{Source: "panel", Device: "d2", InputAction: "submit", OutputActions: []string{"armTimer", "noop"}, Outcome: OutcomeQueued, Reason: "submit-armed"})
	al.Append(ActionRecord{Source: "panel", Device: "d2", InputAction: "submit", OutputActions: []string{"submitNow"}, Outcome: OutcomeRefused, Reason: "off-action"})

	cases := []struct {
		name   string
		filter ActionLogFilter
		want   int
	}{
		{"no filter", ActionLogFilter{}, 3},
		{"input action", ActionLogFilter{InputAction: "clear"}, 1},
		{"input action, case-insensitive", ActionLogFilter{InputAction: "CLEAR"}, 1},
		{"output action", ActionLogFilter{OutputAction: "armTimer"}, 1},
		{"output action, second of two", ActionLogFilter{OutputAction: "noop"}, 1},
		{"outcome queued", ActionLogFilter{Outcome: OutcomeQueued}, 1},
		{"outcome refused and input action", ActionLogFilter{Outcome: OutcomeRefused, InputAction: "submit"}, 1},
		{"source", ActionLogFilter{Source: "panel"}, 2},
		{"device", ActionLogFilter{Device: "d2"}, 2},
		{"reason", ActionLogFilter{Reason: "off-action"}, 1},
		{"contradictory", ActionLogFilter{Outcome: OutcomeDelivered, Device: "d2"}, 0},
		{"unknown input action", ActionLogFilter{InputAction: "nope"}, 0},
		{"unknown outcome", ActionLogFilter{Outcome: "exploded"}, 0},
	}
	for _, tc := range cases {
		if got := al.List(tc.filter); len(got) != tc.want {
			t.Errorf("%s: got %d records, want %d", tc.name, len(got), tc.want)
		}
	}
}

// A windowed query must place every record inside the window, and must not
// include a record whose timestamp it cannot read.
func TestActionLogTimeWindow(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	clock := base
	al := NewActionLog(ActionLogConfig{Now: func() time.Time { return clock }})
	al.Append(ActionRecord{Source: "old", Outcome: OutcomeDelivered})
	clock = base.Add(time.Hour)
	al.Append(ActionRecord{Source: "new", Outcome: OutcomeDelivered})

	if got := al.List(ActionLogFilter{Since: base.Add(30 * time.Minute)}); len(got) != 1 || got[0].Source != "new" {
		t.Errorf("since-30m = %+v, want only the newer record", got)
	}
	if got := al.List(ActionLogFilter{Until: base.Add(30 * time.Minute)}); len(got) != 1 || got[0].Source != "old" {
		t.Errorf("until-30m = %+v, want only the older record", got)
	}
	if got := al.List(ActionLogFilter{Since: base, Until: base.Add(2 * time.Hour)}); len(got) != 2 {
		t.Errorf("full window = %d records, want 2", len(got))
	}
}

// The vocabulary is closed and complete: the four outcomes are exactly what the
// log can report, and they include values no record has produced yet, so a
// filter bar never hides one of its own axes.
func TestActionOutcomesVocabularyIsClosed(t *testing.T) {
	want := []string{OutcomeDelivered, OutcomeQueued, OutcomeDropped, OutcomeRefused}
	if len(ActionOutcomes) != len(want) {
		t.Fatalf("ActionOutcomes = %v, want %v", ActionOutcomes, want)
	}
	for i := range want {
		if ActionOutcomes[i] != want[i] {
			t.Errorf("ActionOutcomes[%d] = %q, want %q", i, ActionOutcomes[i], want[i])
		}
	}
}

// Facets report what is present, and never offer the empty string as a choice.
func TestActionLogFacets(t *testing.T) {
	al := NewActionLog(ActionLogConfig{})
	al.Append(ActionRecord{Source: "panel", InputAction: "", OutputActions: []string{"noop", "clear"}, Outcome: OutcomeDropped, Reason: "no-match", Device: "d1"})
	al.Append(ActionRecord{Source: "panel", InputAction: "clear", OutputActions: []string{"clear"}, Outcome: OutcomeDelivered, Device: "d1"})

	f := al.Facets()
	if len(f.Sources) != 1 || f.Sources[0] != "panel" {
		t.Errorf("sources = %v, want [panel]", f.Sources)
	}
	if len(f.InputActions) != 1 || f.InputActions[0] != "clear" {
		t.Errorf("inputActions = %v, want [clear] (the empty input action is not a facet)", f.InputActions)
	}
	if len(f.OutputActions) != 2 {
		t.Errorf("outputActions = %v, want two distinct verbs", f.OutputActions)
	}
	if len(f.Devices) != 1 || f.Devices[0] != "d1" {
		t.Errorf("devices = %v, want [d1]", f.Devices)
	}
}

// The log stores no evaluated text, and an identifier field cannot smuggle a
// hostile value into a rendered surface.
func TestActionLogSanitizesIdentifiersAndHasNoTextField(t *testing.T) {
	al := NewActionLog(ActionLogConfig{})
	rec := al.Append(ActionRecord{
		Source:        "panel",
		Device:        "d1<script>alert(1)</script>",
		InputAction:   "clear",
		OutputActions: []string{"clear", "not a verb!", "--token=s3cr3t"},
		Outcome:       OutcomeDelivered,
	})

	if strings.ContainsAny(rec.Device, "<>()") {
		t.Errorf("device kept hostile characters: %q", rec.Device)
	}
	for _, v := range rec.OutputActions {
		if strings.ContainsAny(v, " !=") {
			t.Errorf("verb kept a non-verb character: %q", v)
		}
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"text", "phrase", "\"body\"", "s3cr3t"} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(forbidden)) {
			t.Errorf("a log record carries %q:\n%s", forbidden, raw)
		}
	}
}

// Newest-first must follow INSERTION order, not a comparison of the formatted
// timestamps. time.RFC3339Nano drops the trailing zeros of an exact second, and
// 'Z' (0x5A) sorts after '.' (0x2E), so the formatted strings of an
// exact-second record compare GREATER than those of a later fractional one — a
// string sort would present the older row as newer. The ring is appended in
// order, so walking it backwards is both correct and comparison-free.
func TestActionLogNewestFirstFollowsInsertionNotFormattedString(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	clock := base
	al := NewActionLog(ActionLogConfig{Now: func() time.Time { return clock }})
	al.Append(ActionRecord{Source: "exact-second", Outcome: OutcomeDelivered})
	clock = base.Add(100 * time.Millisecond)
	al.Append(ActionRecord{Source: "fractional", Outcome: OutcomeDelivered})

	got := al.List(ActionLogFilter{})
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].Source != "fractional" || got[1].Source != "exact-second" {
		t.Errorf("newest-first = [%s, %s], want [fractional, exact-second]", got[0].Source, got[1].Source)
	}

	// Test the test: if these two formatted stamps did NOT compare backwards, the
	// case above would pass for the wrong reason and prove nothing. This asserts
	// the hazard is real, so the ordering assertion above is load-bearing.
	if !(got[1].At > got[0].At) {
		t.Fatalf("this fixture does not exercise the hazard — %q does not sort after %q", got[1].At, got[0].At)
	}
}
