package store

import (
	"testing"
	"time"
)

func TestCompareStampsOrdersByInstantNotByString(t *testing.T) {
	// The bare-second form sorts lexically AFTER the fractional one.
	const whole, frac = "2026-10-18T12:00:05Z", "2026-10-18T12:00:05.1Z"
	if whole <= frac {
		t.Fatal("fixture no longer demonstrates the lexical inversion")
	}
	if got := CompareStamps(whole, frac); got != -1 {
		t.Errorf("CompareStamps(%q, %q) = %d, want -1", whole, frac, got)
	}
	if got := CompareStamps(frac, whole); got != 1 {
		t.Errorf("reverse = %d, want 1", got)
	}
	if got := CompareStamps(whole, whole); got != 0 {
		t.Errorf("equal = %d, want 0", got)
	}
	// Fractions of different width: .5 is later than .45.
	if got := CompareStamps("2026-10-18T12:00:05.5Z", "2026-10-18T12:00:05.45Z"); got != 1 {
		t.Errorf("width-mismatched fractions = %d, want 1", got)
	}
}

func TestCompareStampsFallsBackToLexicalForGarbage(t *testing.T) {
	if got := CompareStamps("a", "b"); got != -1 {
		t.Errorf("got %d, want -1", got)
	}
}

func TestActionLogListIsNewestFirstAcrossStampWidths(t *testing.T) {
	clock := time.Date(2026, 10, 18, 12, 0, 5, 0, time.UTC)
	al := NewActionLog(ActionLogConfig{Now: func() time.Time { return clock }})
	al.Append(ActionRecord{Source: "whole"}) // stamped ...:05Z
	clock = clock.Add(100 * time.Millisecond)
	al.Append(ActionRecord{Source: "frac"}) // stamped ...:05.1Z
	got := al.List(ActionLogFilter{})
	if len(got) != 2 || got[0].Source != "frac" {
		t.Errorf("List order = %+v, want frac (newer) first", got)
	}
}
