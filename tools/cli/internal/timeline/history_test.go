package timeline

import (
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/chathistory"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
)

const histStamp = "2026-10-07T08:00:00Z"

func rec(id string) chathistory.Record {
	return chathistory.Record{ID: id, Ts: histStamp, Channel: "crew-1", Role: "user", From: "captain"}
}

// trail is the evidence a caller supplies after reading the relay's ledger in
// full: it can speak for messages at or after coveredFrom, and the reader's
// clock is now.
func trail(coveredFrom, now time.Time) HandoverEvidence {
	return HandoverEvidence{Read: true, Complete: true, HasCoveredFrom: true,
		CoveredFrom: coveredFrom, Grace: 90 * time.Second, Now: now}
}

// findSource picks one event by WHICH RECORD it came from, not just by message
// id: a recorded message and its hand-over share an id (and often a stamp), and
// a test that grabbed either would prove nothing about the other.
func findSource(t *testing.T, events []Event, src Source, msg string) Event {
	t.Helper()
	for _, e := range events {
		if e.Msg == msg && e.Source == src {
			return e
		}
	}
	t.Fatalf("no %s event for msg %q in %d event(s)", src, msg, len(events))
	return Event{}
}

func TestHistoryRecordedIsNotADelivery(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, histStamp)
	recs := Records{
		History:  []chathistory.Record{rec("m-1")},
		Delivery: []DeliveryRecord{spooled(histStamp, "crew-1", "m-1", "user")},
		Handover: trail(at.Add(-time.Hour), at.Add(time.Hour)),
	}
	events := Build(recs, onLine("crew-1", "m-1"))

	got := findSource(t, events, SourceHistory, "m-1")
	if got.Outcome != OutcomeRecorded {
		t.Fatalf("outcome = %q, want %q", got.Outcome, OutcomeRecorded)
	}
	if got.Source != SourceHistory {
		t.Errorf("source = %q, want %q", got.Source, SourceHistory)
	}
	if !strings.Contains(got.Detail, "the relay's own hand-over line") {
		t.Errorf("a recorded message that WAS handed over must point at that line: %q", got.Detail)
	}
	if strings.Contains(got.Detail, "NOTHING picked") {
		t.Errorf("a handed-over message must never read as unhanded: %q", got.Detail)
	}
}

// TestHistoryWithNoHandOverIsUnhanded is the case the whole source exists for:
// the server persisted it and the relay, which can be shown to have been
// watching, never took it.
func TestHistoryWithNoHandOverIsUnhanded(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, histStamp)
	recs := Records{
		History:  []chathistory.Record{rec("m-1")},
		Handover: trail(at.Add(-time.Hour), at.Add(10*time.Minute)),
	}
	events := Build(recs, nil)

	got := findSource(t, events, SourceHistory, "m-1")
	if got.Outcome != OutcomeUnhanded {
		t.Fatalf("outcome = %q, want %q — detail %q", got.Outcome, OutcomeUnhanded, got.Detail)
	}
	if !strings.Contains(got.Detail, "NOTHING picked this message up") {
		t.Errorf("unhanded must state the finding plainly: %q", got.Detail)
	}
	if !strings.Contains(got.Detail, "can be resent") {
		t.Errorf("unhanded should name the remedy, since the message still exists: %q", got.Detail)
	}
}

// TestHistoryGuardsKeepItFromAccusingTheRelay runs every reason a missing
// hand-over is NOT evidence. Each must land on Recorded with its own sentence:
// an accusation the trail cannot support is worse than no verdict.
func TestHistoryGuardsKeepItFromAccusingTheRelay(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, histStamp)
	cases := []struct {
		name string
		ev   HandoverEvidence
		want string
	}{
		{"caller never supplied evidence", HandoverEvidence{},
			"NO delivery trail could be read"},
		{"no trail at all (an older relay)", HandoverEvidence{Now: at.Add(time.Hour)},
			"NO delivery trail could be read"},
		{"trail rotated", HandoverEvidence{Read: true, Now: at.Add(time.Hour),
			Reason: "it has rotated"},
			"it has rotated"},
		{"trail truncated", HandoverEvidence{Read: true, Now: at.Add(time.Hour),
			Reason: "the file exceeded this reader's cap"},
			"exceeded this reader's cap"},
		{"no clock given", HandoverEvidence{Read: true, Complete: true},
			"was not told the time"},
		{"younger than the hand-over window", trail(at.Add(-time.Hour), at.Add(30*time.Second)),
			"younger than the hand-over window"},
		{"trail starts after the message", trail(at.Add(time.Minute), at.Add(time.Hour)),
			"The delivery trail begins at"},
		{"trail holds no dated line", HandoverEvidence{Read: true, Complete: true, Now: at.Add(time.Hour)},
			"no dated line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := Build(Records{History: []chathistory.Record{rec("m-1")}, Handover: tc.ev}, nil)
			got := findSource(t, events, SourceHistory, "m-1")
			if got.Outcome != OutcomeRecorded {
				t.Fatalf("outcome = %q, want %q (a guard failed): %q", got.Outcome, OutcomeRecorded, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.want) {
				t.Errorf("detail %q does not name the guard %q", got.Detail, tc.want)
			}
		})
	}
}

// TestHistoryUnparseableStampIsNotJudged: a record that cannot be placed in
// time cannot be judged against a window, so it stays Recorded.
func TestHistoryUnparseableStampIsNotJudged(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, histStamp)
	bad := rec("m-1")
	bad.Ts = "not-a-stamp"
	events := Build(Records{History: []chathistory.Record{bad}, Handover: trail(at.Add(-time.Hour), at.Add(time.Hour))}, nil)

	got := findSource(t, events, SourceHistory, "m-1")
	if got.Outcome != OutcomeRecorded {
		t.Fatalf("outcome = %q, want %q", got.Outcome, OutcomeRecorded)
	}
	if !strings.Contains(got.Detail, "stamp does not parse") {
		t.Errorf("detail must say the stamp is the reason: %q", got.Detail)
	}
	if got.HasAt {
		t.Error("an unparseable stamp must not set HasAt")
	}
}

// TestFailedHandOverCountsAsHanded: a spool-failed means the relay DID take the
// message and failed to write it. That is the `dropped` line's story, and
// calling the message unhanded would put two contradictory verdicts on one axis.
func TestFailedHandOverCountsAsHanded(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, histStamp)
	recs := Records{
		History: []chathistory.Record{rec("m-1")},
		Delivery: []DeliveryRecord{{Entry: relayctl.DeliveryEntry{
			Ts: histStamp, Event: "spool-failed", Agent: "crew-1", Msg: "m-1"}}},
		Handover: trail(at.Add(-time.Hour), at.Add(time.Hour)),
	}
	got := findSource(t, Build(recs, nil), SourceHistory, "m-1")
	if got.Outcome != OutcomeRecorded {
		t.Fatalf("outcome = %q, want %q (the dropped line is the verdict, not this one)", got.Outcome, OutcomeRecorded)
	}
	if !strings.Contains(got.Detail, "the relay's own hand-over line") {
		t.Errorf("detail should point at the relay's failed attempt: %q", got.Detail)
	}
}

// TestAnotherAgentsHandOverDoesNotClearThisOne: the hand-over index is keyed by
// agent AND message, so a replay on one channel cannot excuse silence on another.
func TestAnotherAgentsHandOverDoesNotClearThisOne(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, histStamp)
	recs := Records{
		History:  []chathistory.Record{rec("m-1")},
		Delivery: []DeliveryRecord{spooled(histStamp, "crew-2", "m-1", "user")},
		Handover: trail(at.Add(-time.Hour), at.Add(time.Hour)),
	}
	if got := findSource(t, Build(recs, nil), SourceHistory, "m-1"); got.Outcome != OutcomeUnhanded {
		t.Fatalf("outcome = %q, want %q — crew-2's hand-over is not crew-1's", got.Outcome, OutcomeUnhanded)
	}
}

// TestHistoryRecordsAreFilterable: the new outcomes are members of the closed
// vocabulary, so --outcome can select them like any other.
func TestHistoryRecordsAreFilterable(t *testing.T) {
	for _, o := range []Outcome{OutcomeRecorded, OutcomeUnhanded} {
		parsed, ok := ParseOutcome(string(o))
		if !ok || parsed != o {
			t.Fatalf("ParseOutcome(%q) = %q, %v", o, parsed, ok)
		}
	}
	if _, ok := ParseOutcome("delivered"); ok {
		t.Fatal("`delivered` must stay out of the vocabulary: nothing acknowledges a read")
	}
}
