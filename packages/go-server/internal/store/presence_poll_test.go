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
	p.TouchPoll("c0", "2026-10-07T11:00:00Z")

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
	p.TouchPoll("c0", "2026-10-07T11:00:00Z")
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
		p.TouchPoll(ch, "2026-10-07T11:00:00Z")
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
