// Tests for the two mechanisms the input-seam recording depends on: the
// policy hold, which must reach a terminal outcome without typing anything,
// and the accept hook, which must run before any worker can pick the
// submission up (otherwise the ledger would print a submission's outcome
// before the boundary hop that produced it).
package remoteinput

import (
	"sync"
	"testing"
	"time"
)

func TestHoldIsTerminalAndPreservesText(t *testing.T) {
	fake := &FakeTalon{}
	var settled []Outcome
	svc := NewService(fake, 0, func(o Outcome) { settled = append(settled, o) })
	defer svc.Stop()

	id := svc.Hold(Submission{Device: "phone-1", Text: "ship it maybe", Mode: ModeInject}, "confidence 0.30 is below the minimum 0.80")
	o, ok := svc.Get(id)
	if !ok {
		t.Fatalf("held outcome for %s not retained", id)
	}
	if o.Status != StatusHeld {
		t.Errorf("status = %q, want %q", o.Status, StatusHeld)
	}
	if !o.PreserveText || o.InjectAttempted {
		t.Errorf("held outcome = %+v, want PreserveText and no injection attempted", o)
	}
	if !o.Terminal() {
		t.Error("held must be terminal: the phone waits for a terminal outcome to stop polling")
	}
	if len(fake.Inserts) != 0 || len(fake.FocusAppCalls) != 0 {
		t.Errorf("a hold touched Talon: inserts=%v focus=%v", fake.Inserts, fake.FocusAppCalls)
	}
	if len(settled) != 1 || settled[0].Status != StatusHeld {
		t.Errorf("settle callbacks = %+v, want exactly one held outcome", settled)
	}
}

func TestOnAcceptedRunsBeforeTheOutcomeIsSettled(t *testing.T) {
	fake := &FakeTalon{}
	var mu sync.Mutex
	var order []string
	svc := NewService(fake, 0, func(o Outcome) {
		if o.Terminal() {
			mu.Lock()
			order = append(order, "settled:"+o.Status)
			mu.Unlock()
		}
	})
	defer svc.Stop()
	svc.SetOnAccepted(func(sub Submission) {
		if sub.ID == "" {
			t.Error("the accept hook must see an assigned id: the ledger keys the boundary hop on it")
		}
		mu.Lock()
		order = append(order, "accepted:"+sub.ID)
		mu.Unlock()
	})

	id := svc.Submit(Submission{Device: "phone-1", Text: "ship it", App: "Terminal"})
	waitTerminal(t, svc, id)

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "accepted:"+id || order[1] != "settled:"+StatusInjected {
		t.Fatalf("order = %v, want the accept hop before the settled outcome", order)
	}
}

// waitTerminal blocks until the service has settled id.
func waitTerminal(t *testing.T, svc *Service, id string) Outcome {
	t.Helper()
	for i := 0; i < 400; i++ {
		if o, ok := svc.Get(id); ok && o.Terminal() {
			return o
		}
		sleepSettle(5 * time.Millisecond) // through the same seam the focus gate uses
	}
	t.Fatalf("submission %s never reached a terminal outcome", id)
	return Outcome{}
}
