// The cursor half of the listener join: when the server says the last poll on
// a channel carried no backlog cursor, a waiting input can name the mechanism
// that kept it waiting instead of shrugging. These tests pin the truth table
// (including every case where the claim must NOT be made) and both renderings,
// because a view that brands a listener blind on the strength of a field the
// server never sent would be worse than the shrug.
package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

// A listener fact with an explicit cursor answer, d seconds old.
func cursoredFact(now time.Time, ago time.Duration, carried *bool) channelListener {
	return channelListener{
		Channel:          "c0",
		LastPollTs:       now.Add(-ago).Format(time.RFC3339Nano),
		LastPollCursored: carried,
	}
}

func TestMissedTheBacklogTruthTable(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	older := inputRow{ID: "m1", Channel: "c0", State: stateQueued, At: now.Add(-time.Minute)}
	newer := inputRow{ID: "m2", Channel: "c0", State: stateQueued, At: now.Add(-time.Second)}

	cases := []struct {
		name string
		fact channelListener
		row  inputRow
		want bool
	}{
		{"a cursorless poll that arrived after the input was queued", cursoredFact(now, 5*time.Second, boolPtr(false)), older, true},
		{"an input queued in the same instant the poll arrived", cursoredFact(now, 5*time.Second, boolPtr(false)),
			inputRow{ID: "m3", At: now.Add(-5 * time.Second)}, true},
		{"a cursored poll could have returned it", cursoredFact(now, 5*time.Second, boolPtr(true)), older, false},
		{"the server does not report the fact at all", cursoredFact(now, 5*time.Second, nil), older, false},
		{"the input arrived after the cursorless poll, so that poll is not why", cursoredFact(now, 30*time.Second, boolPtr(false)), newer, false},
		{"an unreadable poll stamp proves nothing", channelListener{
			Channel: "c0", LastPollTs: "yesterday", LastPollCursored: boolPtr(false),
		}, older, false},
		{"an input with no usable timestamp proves nothing", cursoredFact(now, 5*time.Second, boolPtr(false)), inputRow{ID: "m4"}, false},
	}
	for _, tc := range cases {
		st := classifyListener(tc.fact, true, defaultPollHold, now)
		if got := missedTheBacklog(tc.fact, tc.row, st); got != tc.want {
			t.Errorf("%s: missedTheBacklog = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The clause appears in exactly the states it is true in, and the row's
// wording stays byte-identical where the server reports nothing.
func TestListenerNoteNamesTheCursorlessPoll(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	waiting := inputRow{ID: "m1", Channel: "c0", State: stateQueued, Why: "waiting for a listener", At: now.Add(-time.Minute)}

	blind := inputPage{PollHoldMs: 25000, Listeners: []channelListener{cursoredFact(now, 5*time.Second, boolPtr(false))}}
	if note := blind.listenerNote(waiting, now); !strings.Contains(note, "carried no backlog cursor") {
		t.Errorf("an attached listener that cannot replay the backlog = %q, want the mechanism named", note)
	}

	// The same listener, parked on the channel right now.
	parked := blind
	parked.Listeners = []channelListener{cursoredFact(now, 5*time.Second, boolPtr(false))}
	parked.Listeners[0].ActivePollers = 1
	if note := parked.listenerNote(waiting, now); !strings.Contains(note, "parked") || !strings.Contains(note, "carried no backlog cursor") {
		t.Errorf("a parked cursorless poll = %q, want the mechanism named too", note)
	}

	// A listener that DID ask for the backlog: no claim to make.
	canReplay := inputPage{PollHoldMs: 25000, Listeners: []channelListener{cursoredFact(now, 5*time.Second, boolPtr(true))}}
	note := canReplay.listenerNote(waiting, now)
	if strings.Contains(note, "cursor") {
		t.Errorf("a cursored poll produced a cursor claim: %q", note)
	}
	if note != "a listener is attached (last poll 5.0s ago) and has not taken it" {
		t.Errorf("a cursored poll changed the row's wording to %q", note)
	}

	// An older server: bytes must be exactly what they were before.
	silent := inputPage{PollHoldMs: 25000, Listeners: []channelListener{cursoredFact(now, 5*time.Second, nil)}}
	if got := silent.listenerNote(waiting, now); got != note {
		t.Errorf("an unreporting server produced %q, want the unchanged %q", got, note)
	}
}

// The fleet-wide block says which channels could not replay a waiting input,
// so an operator can see the shape of the problem without reading every row.
func TestRenderInputListenersNamesCursorlessChannels(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	page := inputPage{PollHoldMs: 25000, Listeners: []channelListener{
		cursoredFact(now, 5*time.Second, boolPtr(false)),
		{Channel: "good", LastPollTs: now.Add(-5 * time.Second).Format(time.RFC3339Nano), ActivePollers: 1, LastPollCursored: boolPtr(true)},
		{Channel: "old-srv", LastPollTs: now.Add(-90 * time.Second).Format(time.RFC3339Nano)},
	}}
	var buf bytes.Buffer
	renderInputListeners(&buf, page, now)
	out := buf.String()

	if got := strings.Count(out, "carried no backlog cursor"); got != 1 {
		t.Errorf("the block named %d channel(s) as unable to replay, want exactly the one:\n%s", got, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "  c0") {
			continue
		}
		if !strings.Contains(line, "a listener is attached (but its last poll carried no backlog cursor)") {
			t.Errorf("c0's line does not name the mechanism: %q", line)
		}
	}
	// A quiet channel is not "attached", so a stale cursorless poll does not
	// get the parenthetical: nothing is there anyway, and the cursor would be
	// a misleading cause.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "old-srv") && strings.Contains(line, "cursor") {
			t.Errorf("a quiet channel was branded with the cursor clause: %q", line)
		}
	}
}
