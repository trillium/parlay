package sourcecontract

import (
	"reflect"
	"strings"
	"testing"
)

func raws(t *testing.T, cs ...Contract) map[string][]byte {
	t.Helper()
	m := map[string][]byte{}
	for _, c := range cs {
		m[c.Name+".json"] = mustJSON(t, c)
	}
	return m
}

func TestLoadAndQueries(t *testing.T) {
	r, err := Load(raws(t, observability(), content(), control()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3", r.Len())
	}
	if got := r.Names(); !reflect.DeepEqual(got, []string{"cursorless", "hook-tailer", "tool-tailer"}) {
		t.Fatalf("Names = %v", got)
	}
	c, ok := r.ByName("tool-tailer")
	if !ok || c.Trust != TrustObservability {
		t.Fatalf("ByName(tool-tailer) = %+v, %v", c, ok)
	}
	if _, ok := r.ByName("nobody"); ok {
		t.Fatal("ByName(nobody) should miss")
	}

	// The derived allowlist: exactly the enrolled observability emits —
	// content and control surfaces contribute nothing.
	if got := r.IngressEventNames(); !reflect.DeepEqual(got, []string{"tool_event"}) {
		t.Fatalf("IngressEventNames = %v, want [tool_event]", got)
	}

	if got := r.Capable(Originate); !reflect.DeepEqual(got, []string{"hook-tailer"}) {
		t.Fatalf("Capable(originate) = %v", got)
	}
	if got := r.Capable(View); !reflect.DeepEqual(got, []string{"cursorless"}) {
		t.Fatalf("Capable(view) = %v", got)
	}
	if got := r.Capable(Send); len(got) != 0 {
		t.Fatalf("Capable(send) = %v, want none", got)
	}

	want := "sourcecontract.Registry{cursorless(control) hook-tailer(content) tool-tailer(observability)}"
	if got := r.String(); got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
}

// TestEmptyRegistryFailsClosed: no contracts, no allowlist — never a
// pass-through (docs/source-contracts.md "The security story" lock 3).
func TestEmptyRegistryFailsClosed(t *testing.T) {
	r, err := Load(nil)
	if err != nil {
		t.Fatalf("Load(nil): %v", err)
	}
	if got := r.IngressEventNames(); len(got) != 0 {
		t.Fatalf("empty registry derived %v", got)
	}
}

func TestLoadRejectsFilenameIdentityMismatch(t *testing.T) {
	c := observability()
	_, err := Load(map[string][]byte{"renamed.json": mustJSON(t, c)})
	if err == nil || !strings.Contains(err.Error(), `"tool-tailer.json"`) {
		t.Fatalf("want filename-mismatch error naming the required file, got %v", err)
	}
}

// TestLoadRejectsSecondClaimant: one name per real producer is
// registry-enforced — two contracts claiming one event name cannot load.
func TestLoadRejectsSecondClaimant(t *testing.T) {
	a := observability()
	b := observability()
	b.Name = "other-tailer"
	_, err := Load(raws(t, a, b))
	if err == nil || !strings.Contains(err.Error(), "one name per real producer") {
		t.Fatalf("want duplicate-claim refusal, got %v", err)
	}
	// Sorted key order makes the reported claimant deterministic:
	// other-tailer.json loads first, tool-tailer.json is refused.
	if !strings.Contains(err.Error(), "tool-tailer.json") || !strings.Contains(err.Error(), `"other-tailer"`) {
		t.Fatalf("refusal should name the loser and the owner, got %v", err)
	}
}

func TestLoadReportsFileForInvalidContract(t *testing.T) {
	_, err := Load(map[string][]byte{"broken.json": []byte("{nope")})
	if err == nil || !strings.Contains(err.Error(), "broken.json") {
		t.Fatalf("want error naming broken.json, got %v", err)
	}
}
