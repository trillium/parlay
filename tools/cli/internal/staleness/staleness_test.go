package staleness

import (
	"errors"
	"testing"
)

// mustAdd/mustDep keep test bodies about the property under test, not
// error plumbing.
func mustAdd(t *testing.T, g *Graph, id ID, v Version) {
	t.Helper()
	if err := g.Add(id, v); err != nil {
		t.Fatalf("Add(%s, %s): %v", id, v, err)
	}
}

func mustDep(t *testing.T, g *Graph, dependent, upstream ID) {
	t.Helper()
	if err := g.DeclareDependency(dependent, upstream); err != nil {
		t.Fatalf("DeclareDependency(%s, %s): %v", dependent, upstream, err)
	}
}

func directlyStale(t *testing.T, g *Graph, id ID) bool {
	t.Helper()
	stale, err := g.DirectlyStale(id)
	if err != nil {
		t.Fatalf("DirectlyStale(%s): %v", id, err)
	}
	return stale
}

func TestDependencyEdgeCarriesStaleness(t *testing.T) {
	g := New()
	mustAdd(t, g, "code", "v1")
	mustAdd(t, g, "test", "v1")
	mustDep(t, g, "test", "code")

	if directlyStale(t, g, "test") {
		t.Fatal("test stale immediately after declaring against current version")
	}
	if err := g.Bump("code", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	if !directlyStale(t, g, "test") {
		t.Fatal("test not stale after its declared upstream moved")
	}
}

// The #128 §23 guarantee: a read is not a dependency. An identical shape
// to the test above, with the edge recorded as a read instead — the
// downstream record must NOT go stale.
func TestReadDoesNotCarryStaleness(t *testing.T) {
	g := New()
	mustAdd(t, g, "context", "v1")
	mustAdd(t, g, "task", "v1")
	if err := g.RecordRead("task", "context"); err != nil {
		t.Fatal(err)
	}
	if err := g.Bump("context", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	if directlyStale(t, g, "task") {
		t.Fatal("read attachment carried staleness; §23 forbids that")
	}
	reads, err := g.Reads("task")
	if err != nil {
		t.Fatal(err)
	}
	if len(reads) != 1 || reads[0] != "context" {
		t.Fatalf("read provenance lost: %v", reads)
	}
}

func TestRevalidateClearsDirectStaleness(t *testing.T) {
	g := New()
	mustAdd(t, g, "up", "v1")
	mustAdd(t, g, "down", "v1")
	mustDep(t, g, "down", "up")
	if err := g.Bump("up", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	if !directlyStale(t, g, "down") {
		t.Fatal("precondition: down should be stale")
	}
	if err := g.Revalidate("down"); err != nil {
		t.Fatal(err)
	}
	if directlyStale(t, g, "down") {
		t.Fatal("down still stale after revalidating against current upstream")
	}
}

// Bumping to the identical version is the "reran, same output" case and
// must not disturb dependents.
func TestSameVersionBumpIsNoOp(t *testing.T) {
	g := New()
	mustAdd(t, g, "up", "v1")
	mustAdd(t, g, "down", "v1")
	mustDep(t, g, "down", "up")
	if err := g.Bump("up", "v1", "rerun"); err != nil {
		t.Fatal(err)
	}
	if directlyStale(t, g, "down") {
		t.Fatal("identical-version bump made dependent stale")
	}
}

func TestRedeclareReacks(t *testing.T) {
	g := New()
	mustAdd(t, g, "up", "v1")
	mustAdd(t, g, "down", "v1")
	mustDep(t, g, "down", "up")
	if err := g.Bump("up", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	mustDep(t, g, "down", "up") // re-declare: acks v2
	if directlyStale(t, g, "down") {
		t.Fatal("re-declared edge should have re-acked to current version")
	}
}

func TestRemoveDependencyStopsStaleness(t *testing.T) {
	g := New()
	mustAdd(t, g, "up", "v1")
	mustAdd(t, g, "down", "v1")
	mustDep(t, g, "down", "up")
	if err := g.Bump("up", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveDependency("down", "up"); err != nil {
		t.Fatal(err)
	}
	if directlyStale(t, g, "down") {
		t.Fatal("removed edge still carries staleness")
	}
	// Removing a non-existent edge between real records is a no-op.
	if err := g.RemoveDependency("down", "up"); err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	g := New()
	mustAdd(t, g, "a", "v1")

	if err := g.Add("a", "v2"); !errors.Is(err, ErrRecordExists) {
		t.Fatalf("duplicate Add: got %v, want ErrRecordExists", err)
	}
	if err := g.DeclareDependency("a", "a"); !errors.Is(err, ErrSelfDependency) {
		t.Fatalf("self edge: got %v, want ErrSelfDependency", err)
	}
	if err := g.DeclareDependency("a", "ghost"); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("edge to unknown: got %v, want ErrUnknownRecord", err)
	}
	if err := g.Bump("ghost", "v1", ""); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("bump unknown: got %v, want ErrUnknownRecord", err)
	}
	if _, err := g.DirectlyStale("ghost"); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("stale unknown: got %v, want ErrUnknownRecord", err)
	}
	if err := g.RecordRead("a", "ghost"); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("read of unknown: got %v, want ErrUnknownRecord", err)
	}
	if err := g.Revalidate("ghost"); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("revalidate unknown: got %v, want ErrUnknownRecord", err)
	}
	if _, err := g.Version("ghost"); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("version unknown: got %v, want ErrUnknownRecord", err)
	}
}

func TestVersionReadback(t *testing.T) {
	g := New()
	mustAdd(t, g, "a", "v1")
	if err := g.Bump("a", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	v, err := g.Version("a")
	if err != nil {
		t.Fatal(err)
	}
	if v != "v2" {
		t.Fatalf("Version = %s, want v2", v)
	}
}
