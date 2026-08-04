// Mirrors packages/cli/src/commands-robots-watch.test.ts: the pure diff
// core is the risky part (seed vs transition, no history replay).
package robotswatch

import (
	"reflect"
	"testing"
)

func TestDetectEventsFirstSightingSeedsAndFiresNothing(t *testing.T) {
	curr := StoreState{"robots-1": "open", "robots-2": "closed"}
	events, seeded := detectEvents(nil, curr, "robots", []EventKind{EventCreated})
	if !seeded {
		t.Fatalf("want seeded=true")
	}
	if len(events) != 0 {
		t.Fatalf("want no events, got %v", events)
	}
}

func TestDetectEventsNewOpenBeadFiresCreated(t *testing.T) {
	prev := StoreState{"robots-1": "open"}
	curr := StoreState{"robots-1": "open", "robots-2": "open"}
	events, _ := detectEvents(prev, curr, "robots", []EventKind{EventCreated})
	want := []RouteEvent{{Store: "robots", Kind: EventCreated, ID: "robots-2", Status: "open"}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("got %v, want %v", events, want)
	}
}

func TestDetectEventsAlreadyClosedBeadDoesNotFireCreated(t *testing.T) {
	prev := StoreState{"robots-1": "open"}
	curr := StoreState{"robots-1": "open", "robots-2": "closed"}
	events, _ := detectEvents(prev, curr, "robots", []EventKind{EventCreated})
	if len(events) != 0 {
		t.Fatalf("want no events, got %v", events)
	}
}

func TestDetectEventsOpenToClosedFiresClosed(t *testing.T) {
	prev := StoreState{"task-1": "open"}
	curr := StoreState{"task-1": "closed"}
	events, _ := detectEvents(prev, curr, "task", []EventKind{EventClosed})
	want := []RouteEvent{{Store: "task", Kind: EventClosed, ID: "task-1", Status: "closed"}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("got %v, want %v", events, want)
	}
}

func TestDetectEventsInProgressToClosedFiresClosed(t *testing.T) {
	prev := StoreState{"task-1": "in_progress"}
	curr := StoreState{"task-1": "closed"}
	events, _ := detectEvents(prev, curr, "task", []EventKind{EventClosed})
	if len(events) != 1 || events[0].Kind != EventClosed {
		t.Fatalf("got %v", events)
	}
}

func TestDetectEventsAlreadyClosedDoesNotRefire(t *testing.T) {
	prev := StoreState{"task-1": "closed"}
	curr := StoreState{"task-1": "closed"}
	events, _ := detectEvents(prev, curr, "task", []EventKind{EventClosed})
	if len(events) != 0 {
		t.Fatalf("want no events, got %v", events)
	}
}

func TestDetectEventsKindsFilter(t *testing.T) {
	prev := StoreState{"q-1": "open"}
	curr := StoreState{"q-1": "open", "q-2": "open"}
	events, _ := detectEvents(prev, curr, "questions", []EventKind{EventClosed})
	if len(events) != 0 {
		t.Fatalf("want no events, got %v", events)
	}
}

func TestDetectEventsReopenDoesNotFireCreated(t *testing.T) {
	prev := StoreState{"task-1": "closed"}
	curr := StoreState{"task-1": "open"}
	events, _ := detectEvents(prev, curr, "task", []EventKind{EventCreated, EventClosed})
	if len(events) != 0 {
		t.Fatalf("want no events, got %v", events)
	}
}

func TestNotifyChannelsExtractsFromLabels(t *testing.T) {
	got := notifyChannels([]string{"cat-work", "notify:mayor", "notify:shepherd"})
	want := []string{"mayor", "shepherd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNotifyChannelsNoLabel(t *testing.T) {
	if got := notifyChannels([]string{"cat-work", "zone:beads"}); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
	if got := notifyChannels(nil); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestNotifyChannelsTrimsAndIgnoresEmpty(t *testing.T) {
	got := notifyChannels([]string{"notify: mayor ", "notify:"})
	want := []string{"mayor"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
