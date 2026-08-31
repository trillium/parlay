package staleness

import (
	"errors"
	"reflect"
	"testing"
)

func why(t *testing.T, g *Graph, id ID) []Cause {
	t.Helper()
	causes, err := g.Why(id)
	if err != nil {
		t.Fatalf("Why(%s): %v", id, err)
	}
	return causes
}

func TestWhyDirectCause(t *testing.T) {
	g := New()
	mustAdd(t, g, "code", "v1")
	mustAdd(t, g, "test", "v1")
	mustDep(t, g, "test", "code")
	if err := g.Bump("code", "v2", "edit"); err != nil {
		t.Fatal(err)
	}

	causes := why(t, g, "test")
	if len(causes) != 1 {
		t.Fatalf("causes = %v, want exactly one", causes)
	}
	want := Cause{
		Record: "test", Upstream: "code",
		Acked: "v1", Current: "v2", Reason: "edit",
		Path: []ID{"test"},
	}
	if !reflect.DeepEqual(causes[0], want) {
		t.Fatalf("cause = %+v, want %+v", causes[0], want)
	}
}

// The "what edge made it so" question, transitively: the stale edge sits
// two hops upstream, and the path names the chain that carried it.
func TestWhyTransitiveCauseCarriesPath(t *testing.T) {
	g := New()
	mustAdd(t, g, "code", "v1")
	mustAdd(t, g, "test", "v1")
	mustAdd(t, g, "release", "v1")
	mustDep(t, g, "test", "code")
	mustDep(t, g, "release", "test")
	if err := g.Bump("code", "v2", "superseded:major"); err != nil {
		t.Fatal(err)
	}

	causes := why(t, g, "release")
	if len(causes) != 1 {
		t.Fatalf("causes = %v, want exactly one", causes)
	}
	c := causes[0]
	if c.Record != "test" || c.Upstream != "code" {
		t.Fatalf("stale edge = %s→%s, want test→code", c.Record, c.Upstream)
	}
	if c.Reason != "superseded:major" {
		t.Fatalf("Reason = %q, want the upstream's bump reason", c.Reason)
	}
	if !reflect.DeepEqual(c.Path, []ID{"release", "test"}) {
		t.Fatalf("Path = %v, want [release test]", c.Path)
	}
}

// Why is non-empty exactly when IsStale is true; a fresh record explains
// to nothing.
func TestWhyFreshRecordIsEmpty(t *testing.T) {
	g := New()
	mustAdd(t, g, "up", "v1")
	mustAdd(t, g, "down", "v1")
	mustDep(t, g, "down", "up")
	if causes := why(t, g, "down"); len(causes) != 0 {
		t.Fatalf("fresh record has causes: %v", causes)
	}
}

// A record consulted via RecordRead must never appear in an explanation.
func TestWhyNeverWalksReads(t *testing.T) {
	g := New()
	mustAdd(t, g, "context", "v1")
	mustAdd(t, g, "task", "v1")
	if err := g.RecordRead("task", "context"); err != nil {
		t.Fatal(err)
	}
	if err := g.Bump("context", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	if causes := why(t, g, "task"); len(causes) != 0 {
		t.Fatalf("read attachment surfaced in Why: %v", causes)
	}
}

// On a diamond both stale edges are reported, each exactly once, in
// deterministic order, with correct per-branch paths.
func TestWhyDiamondReportsEachEdgeOnce(t *testing.T) {
	g := New()
	for _, id := range []ID{"a", "b", "c", "d"} {
		mustAdd(t, g, id, "v1")
	}
	mustDep(t, g, "b", "a")
	mustDep(t, g, "c", "a")
	mustDep(t, g, "d", "b")
	mustDep(t, g, "d", "c")
	if err := g.Bump("a", "v2", "edit"); err != nil {
		t.Fatal(err)
	}

	causes := why(t, g, "d")
	if len(causes) != 2 {
		t.Fatalf("causes = %+v, want exactly two (b→a and c→a)", causes)
	}
	// Sorted expansion: b's branch explored before c's.
	if causes[0].Record != "b" || !reflect.DeepEqual(causes[0].Path, []ID{"d", "b"}) {
		t.Fatalf("first cause = %+v, want edge b→a via [d b]", causes[0])
	}
	if causes[1].Record != "c" || !reflect.DeepEqual(causes[1].Path, []ID{"d", "c"}) {
		t.Fatalf("second cause = %+v, want edge c→a via [d c]", causes[1])
	}
}

func TestWhyTerminatesOnCycle(t *testing.T) {
	g := New()
	mustAdd(t, g, "root", "v1")
	mustAdd(t, g, "a", "v1")
	mustAdd(t, g, "b", "v1")
	mustDep(t, g, "a", "b")
	mustDep(t, g, "b", "a")
	mustDep(t, g, "a", "root")
	if err := g.Bump("root", "v2", "edit"); err != nil {
		t.Fatal(err)
	}

	// Query from inside the cycle: must terminate and find the one
	// stale edge (a→root).
	causes := why(t, g, "b")
	if len(causes) != 1 || causes[0].Record != "a" || causes[0].Upstream != "root" {
		t.Fatalf("causes = %+v, want exactly the a→root edge", causes)
	}
	if !reflect.DeepEqual(causes[0].Path, []ID{"b", "a"}) {
		t.Fatalf("Path = %v, want [b a]", causes[0].Path)
	}
}

// Why and IsStale must agree on every record of a mixed graph.
func TestWhyAgreesWithIsStale(t *testing.T) {
	g := New()
	for _, id := range []ID{"a", "b", "c", "d", "e"} {
		mustAdd(t, g, id, "v1")
	}
	mustDep(t, g, "b", "a")
	mustDep(t, g, "c", "b")
	mustDep(t, g, "e", "d") // separate island, stays fresh
	if err := g.Bump("a", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{"a", "b", "c", "d", "e"} {
		stale := isStale(t, g, id)
		causes := why(t, g, id)
		if stale != (len(causes) > 0) {
			t.Fatalf("%s: IsStale=%v but Why returned %d causes", id, stale, len(causes))
		}
	}
}

func TestWhyUnknownRecord(t *testing.T) {
	g := New()
	if _, err := g.Why("ghost"); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("Why(ghost): got %v, want ErrUnknownRecord", err)
	}
}
