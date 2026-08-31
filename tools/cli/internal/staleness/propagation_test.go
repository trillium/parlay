package staleness

import (
	"errors"
	"fmt"
	"testing"
)

func isStale(t *testing.T, g *Graph, id ID) bool {
	t.Helper()
	stale, err := g.IsStale(id)
	if err != nil {
		t.Fatalf("IsStale(%s): %v", id, err)
	}
	return stale
}

// Propagation along a dependency chain: code <- test <- release. A bump
// at the root makes every transitive dependent stale, while only the
// direct dependent is DIRECTLY stale.
func TestPropagationAlongChain(t *testing.T) {
	g := New()
	mustAdd(t, g, "code", "v1")
	mustAdd(t, g, "test", "v1")
	mustAdd(t, g, "release", "v1")
	mustDep(t, g, "test", "code")
	mustDep(t, g, "release", "test")

	if isStale(t, g, "release") {
		t.Fatal("release stale before any change")
	}
	if err := g.Bump("code", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	if !isStale(t, g, "test") {
		t.Fatal("test not stale after code moved")
	}
	if !isStale(t, g, "release") {
		t.Fatal("release not transitively stale after code moved")
	}
	if directlyStale(t, g, "release") {
		t.Fatal("release directly stale — its own edge to test never moved")
	}
	// The root has no dependencies; it can never be stale.
	if isStale(t, g, "code") {
		t.Fatal("root record reported stale")
	}
}

// Termination on a cycle: a <-> b, with a also depending on an external
// root. The visited-once walk must terminate and still find the
// directly-stale record from anywhere in the cycle.
func TestTerminationOnCycle(t *testing.T) {
	g := New()
	mustAdd(t, g, "root", "v1")
	mustAdd(t, g, "a", "v1")
	mustAdd(t, g, "b", "v1")
	mustDep(t, g, "a", "b")
	mustDep(t, g, "b", "a")
	mustDep(t, g, "a", "root")

	// Nothing changed: the cycle must terminate and report fresh.
	if isStale(t, g, "a") || isStale(t, g, "b") {
		t.Fatal("cycle reported stale with no change anywhere")
	}
	if err := g.Bump("root", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	if !isStale(t, g, "a") {
		t.Fatal("a not stale after its root moved")
	}
	if !isStale(t, g, "b") {
		t.Fatal("b not stale — it reaches a, which is directly stale")
	}
}

// Termination on a diamond: d depends on b and c, both depend on a. The
// affected set must contain each record exactly once.
func TestTerminationOnDiamond(t *testing.T) {
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
	if !isStale(t, g, "d") {
		t.Fatal("diamond apex not stale after root moved")
	}
	aff, err := g.AffectedBy("a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if aff.Truncated {
		t.Fatal("tiny diamond truncated under default budget")
	}
	seen := map[ID]int{}
	for _, id := range aff.IDs {
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("record %s appears %d times in affected set; visited-once violated", id, n)
		}
	}
	if len(aff.IDs) != 3 || seen["b"] != 1 || seen["c"] != 1 || seen["d"] != 1 {
		t.Fatalf("affected set = %v, want exactly {b, c, d}", aff.IDs)
	}
	if aff.Visited != 4 {
		t.Fatalf("Visited = %d, want 4 (origin + 3 dependents)", aff.Visited)
	}
}

// AffectedBy must terminate on a dependency cycle too: reverse edges
// form the mirror cycle.
func TestAffectedByTerminatesOnCycle(t *testing.T) {
	g := New()
	mustAdd(t, g, "a", "v1")
	mustAdd(t, g, "b", "v1")
	mustDep(t, g, "a", "b")
	mustDep(t, g, "b", "a")
	aff, err := g.AffectedBy("a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(aff.IDs) != 1 || aff.IDs[0] != "b" {
		t.Fatalf("affected set = %v, want {b}", aff.IDs)
	}
}

// The cost bound must actually bound: on a chain far longer than the
// budget, the pass stops at the budget and says so.
func TestBudgetBoundsThePass(t *testing.T) {
	g := New()
	const n = 50
	mustAdd(t, g, "r0", "v1")
	for i := 1; i < n; i++ {
		id := ID(fmt.Sprintf("r%d", i))
		mustAdd(t, g, id, "v1")
		mustDep(t, g, id, ID(fmt.Sprintf("r%d", i-1)))
	}

	const budget = 10
	aff, err := g.AffectedBy("r0", budget)
	if err != nil {
		t.Fatal(err)
	}
	if !aff.Truncated {
		t.Fatalf("pass over %d-node chain not truncated at budget %d", n, budget)
	}
	if aff.Visited > budget {
		t.Fatalf("Visited = %d exceeds budget %d", aff.Visited, budget)
	}
	if len(aff.IDs) >= n-1 {
		t.Fatalf("truncated pass returned the whole chain (%d IDs)", len(aff.IDs))
	}

	// The same pass under the default budget completes.
	full, err := g.AffectedBy("r0", 0)
	if err != nil {
		t.Fatal(err)
	}
	if full.Truncated || len(full.IDs) != n-1 {
		t.Fatalf("full pass: truncated=%v len=%d, want false/%d", full.Truncated, len(full.IDs), n-1)
	}
}

// Early cutoff, the heart of the adopted model: revalidating the middle
// of a chain WITHOUT moving its own version shields everything
// downstream of it.
func TestEarlyCutoffShieldsDownstream(t *testing.T) {
	g := New()
	mustAdd(t, g, "a", "v1")
	mustAdd(t, g, "b", "v1")
	mustAdd(t, g, "c", "v1")
	mustDep(t, g, "b", "a")
	mustDep(t, g, "c", "b")

	if err := g.Bump("a", "v2", "edit"); err != nil {
		t.Fatal(err)
	}
	if !isStale(t, g, "c") {
		t.Fatal("precondition: c should be transitively stale")
	}
	// b re-checks against the new a and its output is unchanged: no Bump.
	if err := g.Revalidate("b"); err != nil {
		t.Fatal(err)
	}
	if isStale(t, g, "b") {
		t.Fatal("b still stale after revalidation")
	}
	if isStale(t, g, "c") {
		t.Fatal("c still stale — early cutoff failed to shield downstream")
	}
	// Had b's output changed, the caller would Bump — and c goes stale
	// via its own edge.
	if err := g.Bump("b", "v2", "revalidation changed output"); err != nil {
		t.Fatal(err)
	}
	if !isStale(t, g, "c") {
		t.Fatal("c not stale after b's version actually moved")
	}
}

// Deterministic output: repeated passes over the same graph emit the
// same order.
func TestAffectedByDeterministicOrder(t *testing.T) {
	g := New()
	mustAdd(t, g, "root", "v1")
	for _, id := range []ID{"z", "m", "a", "q"} {
		mustAdd(t, g, id, "v1")
		mustDep(t, g, id, "root")
	}
	first, err := g.AffectedBy("root", 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := g.AffectedBy("root", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(again.IDs) != len(first.IDs) {
			t.Fatalf("pass %d: length changed", i)
		}
		for j := range first.IDs {
			if again.IDs[j] != first.IDs[j] {
				t.Fatalf("pass %d: order changed: %v vs %v", i, again.IDs, first.IDs)
			}
		}
	}
}

func TestPropagationErrors(t *testing.T) {
	g := New()
	if _, err := g.IsStale("ghost"); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("IsStale(ghost): got %v, want ErrUnknownRecord", err)
	}
	if _, err := g.AffectedBy("ghost", 0); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("AffectedBy(ghost): got %v, want ErrUnknownRecord", err)
	}
}
