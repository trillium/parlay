package parlaybeads

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRealStoreRoundTrip exercises the client against a REAL embedded-Dolt
// store. Opt-in (PARLAY_BEADS_INTEGRATION=1) because it needs a CGO build and
// takes seconds, which would break the CI go job's assumptions — CI stays on
// the hermetic fake-backed tests. Run it whenever the beads dependency is
// bumped:
//
//	PARLAY_BEADS_INTEGRATION=1 go test ./internal/parlaybeads/ -run RealStore -v
func TestRealStoreRoundTrip(t *testing.T) {
	if os.Getenv("PARLAY_BEADS_INTEGRATION") != "1" {
		t.Skip("set PARLAY_BEADS_INTEGRATION=1 to run the real-store round-trip")
	}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), ".beads")

	c, err := Init(ctx, Config{Dir: dir, Actor: "itest"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer c.Close()

	id, err := c.Create(ctx, Bead{
		Title:    "itest crew bead",
		Status:   StatusOpen,
		Assignee: "itest-agent",
		Labels:   []string{"parlay-crew"},
		Metadata: map[string]string{"state": "working"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := c.MergeMetadata(ctx, id, map[string]string{"state": "blocked", "key": "topology"}); err != nil {
		t.Fatalf("MergeMetadata: %v", err)
	}
	if err := c.SetStatus(ctx, id, StatusInProgress); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	b, err := c.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if b.Status != StatusInProgress || b.Metadata["state"] != "blocked" || b.Metadata["key"] != "topology" {
		t.Errorf("round-trip = status %q metadata %v", b.Status, b.Metadata)
	}
	if len(b.Labels) != 1 || b.Labels[0] != "parlay-crew" {
		t.Errorf("labels = %v", b.Labels)
	}

	if AffirmativelyClosed(ctx, c, id) {
		t.Error("open bead reported closed")
	}
	if err := c.CloseBead(ctx, id, "itest done"); err != nil {
		t.Fatalf("CloseBead: %v", err)
	}
	if !AffirmativelyClosed(ctx, c, id) {
		t.Error("closed bead not reported closed")
	}

	// Re-open the same directory through Open (the existing-store path).
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c2, err := Open(ctx, Config{Dir: dir})
	if err != nil {
		t.Fatalf("Open of existing store: %v", err)
	}
	defer c2.Close()
	b2, err := c2.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if !b2.Closed() {
		t.Errorf("reopened store lost the close: status %q", b2.Status)
	}
}
