// Poll activity on the presence tracker: who asked a channel for messages,
// which is a different fact from when the channel last had messages in it
// (lastSeen). The distinction is what lets the input view tell "nothing is
// listening" apart from "a listener is attached and took nothing", so both
// halves are pinned here.
package store

import "testing"

func TestPresenceTrackerTouchPollIsNotLastSeen(t *testing.T) {
	p := newPresenceTracker()
	p.Touch("c0", "2026-10-07T10:00:00Z")
	p.TouchPoll("c0", "2026-10-07T11:00:00Z", true)

	snap := p.Snapshot()
	if len(snap.Presence) != 1 || snap.Presence[0].LastSeen != "2026-10-07T10:00:00Z" {
		t.Errorf("Presence = %+v, want lastSeen untouched by a poll", snap.Presence)
	}
	if len(snap.PollActivity) != 1 {
		t.Fatalf("PollActivity = %+v, want one entry for c0", snap.PollActivity)
	}
	if snap.PollActivity[0].LastPoll != "2026-10-07T11:00:00Z" {
		t.Errorf("LastPoll = %q, want the poll's own timestamp", snap.PollActivity[0].LastPoll)
	}
}

func TestPresenceTrackerPollActivitySurvivesThePollerLeaving(t *testing.T) {
	p := newPresenceTracker()
	p.TouchPoll("c0", "2026-10-07T11:00:00Z", true)
	p.AddPoller("c0")

	if got := p.Snapshot().PollActivity; len(got) != 1 || got[0].ActivePollers != 1 {
		t.Fatalf("PollActivity = %+v, want c0 with one parked poller", got)
	}
	p.RemovePoller("c0")

	// The waiter is gone, the fact that something asked is not: this is
	// exactly the gap PollChannels alone cannot express.
	snap := p.Snapshot()
	if len(snap.PollActivity) != 1 || snap.PollActivity[0].LastPoll == "" {
		t.Fatalf("PollActivity = %+v, want c0 still listed after the poller left", snap.PollActivity)
	}
	if snap.PollActivity[0].ActivePollers != 0 {
		t.Errorf("ActivePollers = %d, want 0", snap.PollActivity[0].ActivePollers)
	}
	for _, c := range snap.PollChannels {
		if c.Channel == "c0" {
			t.Errorf("PollChannels still lists c0 after its poller left: %+v", snap.PollChannels)
		}
	}
}

// A channel that has never been polled is ABSENT, not present with a zero
// timestamp: the view reads absence as "never polled" and must not have to
// tell that apart from an empty string it was handed.
func TestPresenceTrackerNeverPolledChannelIsAbsent(t *testing.T) {
	p := newPresenceTracker()
	p.Touch("c0", "2026-10-07T10:00:00Z")
	if got := p.Snapshot().PollActivity; len(got) != 0 {
		t.Errorf("PollActivity = %+v, want empty for a channel that was never polled", got)
	}
}

func TestPresenceTrackerPollActivityIsSortedByChannel(t *testing.T) {
	p := newPresenceTracker()
	for _, ch := range []string{"c2", "c0", "c1"} {
		p.TouchPoll(ch, "2026-10-07T11:00:00Z", true)
	}
	got := p.Snapshot().PollActivity
	if len(got) != 3 {
		t.Fatalf("PollActivity = %+v, want three entries", got)
	}
	for i, want := range []string{"c0", "c1", "c2"} {
		if got[i].Channel != want {
			t.Errorf("PollActivity[%d].Channel = %q, want %q (map order would make a view and a test unstable)", i, got[i].Channel, want)
		}
	}
}

// The cursor fact is what turns "attached and not taking it" into a mechanism:
// a poll that carried no `after=` can never be handed a message that was
// already queued when it arrived. The tracker keeps it per channel, and the
// LAST poll wins — a listener that learned to pass a cursor is not blind any
// more, and the view must not keep saying it is.
func TestPresenceTrackerRemembersWhetherTheLastPollCarriedACursor(t *testing.T) {
	p := newPresenceTracker()
	p.TouchPoll("blind", "2026-10-07T11:00:00Z", false)
	p.TouchPoll("cursored", "2026-10-07T11:00:00Z", true)

	byChannel := map[string]PollActivityEntry{}
	for _, a := range p.Snapshot().PollActivity {
		byChannel[a.Channel] = a
	}
	if len(byChannel) != 2 {
		t.Fatalf("PollActivity = %+v, want one entry per polled channel", byChannel)
	}
	if byChannel["blind"].CarriedCursor {
		t.Error("a poll with no cursor was recorded as carrying one")
	}
	if !byChannel["cursored"].CarriedCursor {
		t.Error("a cursored poll was recorded as carrying none")
	}

	// The next poll overwrites the fact, cursor included: a channel does not
	// stay blind for ever.
	p.TouchPoll("blind", "2026-10-07T11:30:00Z", true)
	for _, a := range p.Snapshot().PollActivity {
		if a.Channel == "blind" && !a.CarriedCursor {
			t.Error("an older cursorless poll still decides the channel after a cursored one")
		}
	}
}
