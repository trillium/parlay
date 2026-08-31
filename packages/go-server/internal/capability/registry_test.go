package capability

import (
	"fmt"
	"sync"
	"testing"
)

func TestRegisterRejectsInvalid(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("c1", &Declaration{Schema: "nope", Surface: Surface{Kind: "panel"}}); err == nil {
		t.Fatal("invalid declaration registered")
	}
	if err := r.Register("", declare(t)); err == nil {
		t.Fatal("empty connection id registered")
	}
	if err := r.Register("c1", nil); err == nil {
		t.Fatal("nil declaration registered — an undeclared client must simply never be registered")
	}
	if got := r.Get("c1"); got != nil {
		t.Fatalf("rejected registration left state behind: %+v", got)
	}
}

func TestRegistryLifecycle(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("c1", declare(t, "navigate")); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Declared: gated event not accepted → suppressed, and counted.
	if d := r.Decide("c1", "reload"); d.Verdict != VerdictSuppress {
		t.Fatalf("declared client got unaccepted reload: %+v", d)
	}
	if d := r.Decide("c1", "navigate"); d.Verdict != VerdictDeliver || d.Reason != ReasonAccepted {
		t.Fatalf("declared client denied accepted navigate: %+v", d)
	}

	// A connection the registry never saw is legacy.
	if d := r.Decide("stranger", "reload"); d.Verdict != VerdictDeliver || d.Reason != ReasonLegacy {
		t.Fatalf("unregistered client not legacy: %+v", d)
	}

	// Deregistered: back to legacy — the declaration died with the connection.
	r.Deregister("c1")
	r.Deregister("c1") // idempotent: teardown paths fire best-effort
	if d := r.Decide("c1", "reload"); d.Verdict != VerdictDeliver || d.Reason != ReasonLegacy {
		t.Fatalf("deregistered client still gated: %+v", d)
	}

	if got := r.Suppressed(); got["reload"] != 1 || len(got) != 1 {
		t.Fatalf("suppression counters = %v, want map[reload:1]", got)
	}
}

func TestReRegisterReplacesDeclaration(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("c1", declare(t, "navigate")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("c1", declare(t, "reload")); err != nil {
		t.Fatal(err)
	}
	if d := r.Decide("c1", "navigate"); d.Verdict != VerdictSuppress {
		t.Fatalf("old declaration survived re-registration: %+v", d)
	}
	if d := r.Decide("c1", "reload"); d.Verdict != VerdictDeliver {
		t.Fatalf("new declaration not in force: %+v", d)
	}
}

func TestSnapshotSortedAndCopied(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"zeta", "alpha", "mid"} {
		if err := r.Register(id, declare(t, "draft")); err != nil {
			t.Fatal(err)
		}
	}
	snap := r.Snapshot()
	if len(snap) != 3 || snap[0].ID != "alpha" || snap[1].ID != "mid" || snap[2].ID != "zeta" {
		t.Fatalf("snapshot order: %+v", snap)
	}

	// Suppressed() hands out a copy — mutating it must not touch the registry.
	r.Decide("alpha", "navigate")
	counts := r.Suppressed()
	counts["navigate"] = 999
	if r.Suppressed()["navigate"] != 1 {
		t.Fatal("Suppressed() exposed internal state")
	}
}

// The registry serves every broadcast on the hot path — hammer it from
// goroutines so the race detector (CI runs go test -race) can see any
// locking mistake.
func TestRegistryConcurrency(t *testing.T) {
	r := NewRegistry()
	decl := declare(t, "navigate") // built on the test goroutine: declare may t.Fatalf
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("c%d", i)
			for j := 0; j < 100; j++ {
				_ = r.Register(id, decl)
				r.Decide(id, "reload")
				r.Snapshot()
				r.Suppressed()
				r.Deregister(id)
			}
		}(i)
	}
	wg.Wait()
}
