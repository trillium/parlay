// The live tail's decisions: what it shows, what it refuses to show silently,
// and where it puts its cursor afterwards. These are the rules that turned a
// newest-N window tail into one that cannot be outrun.
package commands

import (
	"strings"
	"testing"
	"time"
)

func watchEvent(seq uint64, id, stage, class string) inputEvent {
	return inputEvent{
		Seq: seq, InputID: id, Stage: stage, Class: class, Source: "send",
		Ts: "2026-10-07T02:29:54.107Z",
	}
}

func page(events ...inputEvent) inputPage {
	return inputPage{Events: events, Stats: inputStats{Retained: uint64(len(events)), Written: uint64(len(events))}}
}

// TestWatchTailAsksForwardFromItsCursor is the contract that replaced the
// newest-N window: the request carries the cursor, so a burst larger than the
// page size is read in full over successive polls instead of being truncated
// at its front with nothing said.
func TestWatchTailAsksForwardFromItsCursor(t *testing.T) {
	if got := watchQuery(41, 40); got != "?afterSeq=41&limit=40" {
		t.Errorf("tail query = %q, want a forward cursor", got)
	}
}

// TestWatchTailNamesWhatItsCursorSkipped: when the ledger evicted hops before
// the tail read them, the tail must say how many and which seqs. Silence here
// is the original defect — a hole in the seam read as a quiet night.
func TestWatchTailNamesWhatItsCursorSkipped(t *testing.T) {
	st := &inputTail{cursor: 40, lastHop: map[string]time.Time{}}
	lines := st.lines(page(watchEvent(101, "m1", "queued", "ok"), watchEvent(102, "m2", "delivered", "ok")))
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want a GAP line plus two hops:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	gap := lines[0]
	for _, want := range []string{"GAP", "60 hop(s)", "seq 41–100", "NOT shown"} {
		if !strings.Contains(gap, want) {
			t.Errorf("gap line %q does not name %q", gap, want)
		}
	}
	if st.cursor != 102 {
		t.Errorf("cursor = %d, want 102 (advanced past everything shown)", st.cursor)
	}
}

// TestWatchTailStaysSilentOnlyWhenThereIsNothingNew pins the other half: an
// ordinary quiet poll and a poll that fell off the end must not look the same.
func TestWatchTailStaysSilentOnlyWhenThereIsNothingNew(t *testing.T) {
	st := &inputTail{cursor: 102, lastHop: map[string]time.Time{}}
	live := inputPage{Events: []inputEvent{}}
	live.Stats = inputStats{Retained: 102, Written: 102, NewestSeq: 102}
	if lines := st.lines(live); len(lines) != 0 {
		t.Errorf("an empty page from a live ledger printed %v, want silence", lines)
	}
	// The ledger is behind the cursor: a server that came up against a fresh
	// ledger. Printing nothing here would leave a dead tail looking calm.
	stale := inputPage{Events: []inputEvent{}}
	stale.Stats = inputStats{Retained: 3, Written: 3, NewestSeq: 3}
	lines := st.lines(stale)
	if len(lines) != 1 || !strings.Contains(lines[0], "CURSOR AHEAD") {
		t.Fatalf("a cursor ahead of the ledger printed %v, want a CURSOR AHEAD line", lines)
	}
	if st.cursor != 3 {
		t.Errorf("cursor = %d after re-joining, want 3", st.cursor)
	}
}

// TestWatchTailReportsTheLedgersOwnLosses: stats travel with every page, and
// a tail that ignored them would be the one view able to show a hole and call
// it a quiet night. The first observation reports the running totals, so a
// tail that joined after the loss still sees it.
func TestWatchTailReportsTheLedgersOwnLosses(t *testing.T) {
	st := &inputTail{cursor: 5, lastHop: map[string]time.Time{}}
	p := page(watchEvent(6, "m1", "queued", "ok"))
	p.Stats = inputStats{Retained: 1, Written: 6, NewestSeq: 6, Dropped: 4, Rejected: 1}
	lines := st.lines(p)
	if len(lines) != 2 || !strings.Contains(lines[0], "OBSERVER LOSS") {
		t.Fatalf("lines = %v, want an OBSERVER LOSS line first", lines)
	}
	for _, want := range []string{"5 record(s)", "dropped=4", "rejected=1"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("loss line %q does not carry %q", lines[0], want)
		}
	}
	// Counters that did not move are not re-announced on every poll.
	next := page(watchEvent(7, "m2", "queued", "ok"))
	next.Stats = inputStats{Retained: 2, Written: 7, NewestSeq: 7, Dropped: 4, Rejected: 1}
	if lines := st.lines(next); len(lines) != 1 || strings.Contains(lines[0], "OBSERVER LOSS") {
		t.Errorf("unchanged counters printed %v, want only the hop", lines)
	}
}

// TestWatchJoinLineNamesTheHistoryItIsNotShowing: a tail follows the live
// edge, so the hops before it are a deliberate starting point, not an
// omission. Saying so is the difference.
func TestWatchJoinLineNamesTheHistoryItIsNotShowing(t *testing.T) {
	line := watchJoinLine(120, inputStats{Retained: 40})
	for _, want := range []string{"seq 120", "39 retained hop(s) before this tail are NOT shown"} {
		if !strings.Contains(line, want) {
			t.Errorf("join line %q does not name %q", line, want)
		}
	}
	if got := watchJoinLine(0, inputStats{}); !strings.Contains(got, "written nothing yet") {
		t.Errorf("join line on an empty ledger = %q, want it to say nothing is written yet", got)
	}
}

// TestWatchTailDoesNotReplayHistoryAfterALateJoin covers the join that was
// deferred because the server was down at startup. A tail follows the live
// edge: on the first page it does get it must adopt that edge and say where it
// started, NOT dump the whole retained window into the live view.
func TestWatchTailDoesNotReplayHistoryAfterALateJoin(t *testing.T) {
	st := &inputTail{lastHop: map[string]time.Time{}}
	p := page(watchEvent(1, "m0", "queued", "ok"), watchEvent(2, "m1", "queued", "ok"))
	p.Stats = inputStats{Retained: 2, Written: 2, NewestSeq: 2}
	line := st.join(p)
	if st.cursor != 2 {
		t.Errorf("cursor = %d after a late join, want the live edge (2)", st.cursor)
	}
	for _, want := range []string{"JOINED", "seq 2", "1 retained hop(s) before this tail are NOT shown"} {
		if !strings.Contains(line, want) {
			t.Errorf("late join line %q does not carry %q", line, want)
		}
	}
	if lines := st.lines(p); len(lines) != 0 {
		t.Errorf("a late join replayed retained history into the live tail: %v", lines)
	}
}
