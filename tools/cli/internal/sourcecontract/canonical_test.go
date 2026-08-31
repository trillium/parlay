package sourcecontract

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestCanonicalTreeLoads holds the repo's checked-in contracts/sources/ tree
// to this engine: every canonical declaration must load, so an invalid
// enrollment is a red build here rather than a runtime surprise
// (docs/source-contracts.md "The registry engine"). The engine itself stays
// pure — the file reads live in this test, not in the package.
func TestCanonicalTreeLoads(t *testing.T) {
	canonical := filepath.Join("..", "..", "..", "..", "contracts", "sources")
	entries, err := os.ReadDir(canonical)
	if err != nil {
		t.Fatalf("canonical contracts/sources/ not found relative to this package (%s): %v — was the repo layout moved?", canonical, err)
	}

	raws := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			t.Fatalf("contracts/sources/%s: only .json declaration files belong in the canonical tree", e.Name())
		}
		raw, err := os.ReadFile(filepath.Join(canonical, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		raws[e.Name()] = raw
	}

	r, err := Load(raws)
	if err != nil {
		t.Fatalf("canonical tree does not load: %v", err)
	}

	// The first enrolled surface, and the derived allowlist the go-server
	// side must agree with (its sync test holds the mirror byte-identical;
	// this pins what the bytes mean).
	c, ok := r.ByName("tool-tailer")
	if !ok {
		t.Fatalf("tool-tailer is not enrolled; canonical tree has %v", r.Names())
	}
	if c.Trust != TrustObservability {
		t.Fatalf("tool-tailer trust = %q, want observability", c.Trust)
	}
	if got := r.IngressEventNames(); !reflect.DeepEqual(got, []string{"tool_event"}) {
		t.Fatalf("derived ingress allowlist = %v, want exactly [tool_event]", got)
	}
}
