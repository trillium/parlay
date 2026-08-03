package store

import "testing"

func TestPresenceTrackerPanelClients(t *testing.T) {
	p := newPresenceTracker()
	p.AddPanelClient()
	p.AddPanelClient()
	p.RemovePanelClient()
	if got := p.Snapshot().PanelClients; got != 1 {
		t.Errorf("PanelClients = %d, want 1", got)
	}
}

func TestPresenceTrackerRemovePanelClientNeverGoesNegative(t *testing.T) {
	p := newPresenceTracker()
	p.RemovePanelClient()
	p.RemovePanelClient()
	if got := p.Snapshot().PanelClients; got != 0 {
		t.Errorf("PanelClients = %d, want 0 (floor at zero)", got)
	}
}

func TestPresenceTrackerPollersPerChannel(t *testing.T) {
	p := newPresenceTracker()
	p.AddPoller("c0")
	p.AddPoller("c0")
	p.AddPoller("c1")

	snap := p.Snapshot()
	if snap.PollCount != 3 {
		t.Errorf("PollCount = %d, want 3", snap.PollCount)
	}
	byChannel := map[string]int{}
	for _, c := range snap.PollChannels {
		byChannel[c.Channel] = c.Count
	}
	if byChannel["c0"] != 2 || byChannel["c1"] != 1 {
		t.Errorf("PollChannels = %v, want c0=2 c1=1", byChannel)
	}

	p.RemovePoller("c0")
	p.RemovePoller("c0")
	snap = p.Snapshot()
	for _, c := range snap.PollChannels {
		if c.Channel == "c0" {
			t.Errorf("channel c0 still present after both pollers removed: %+v", c)
		}
	}
}

func TestPresenceTrackerTouch(t *testing.T) {
	p := newPresenceTracker()
	p.Touch("c0", "2026-08-02T00:00:00Z")
	snap := p.Snapshot()
	if len(snap.Presence) != 1 || snap.Presence[0].Channel != "c0" {
		t.Errorf("Presence = %+v, want one entry for c0", snap.Presence)
	}
}
