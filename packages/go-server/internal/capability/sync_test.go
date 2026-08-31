package capability

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This package is a mirror of tools/cli/internal/capability — the normative
// interface-capability engine (docs/interface-capabilities.md). That package
// is internal to a different Go module (github.com/trillium/parlay/tools/cli),
// so this module cannot import it; the mirror is the workaround (the
// internal/sourcecontracts precedent) and these tests are what keep it a
// mirror rather than a fork. On failure the fix is always the same direction:
// change the canonical package under tools/cli first, then re-copy the files
// here (validate.go additionally gets validateRewrites applied — see below).

// canonicalDir is the canonical engine, relative to this package.
var canonicalDir = filepath.Join("..", "..", "..", "..", "tools", "cli", "internal", "capability")

// mirrorOnly are the files that exist only in the mirror, by design: the
// supersession shim and this sync test.
var mirrorOnly = map[string]bool{
	"version.go":   true,
	"sync_test.go": true,
}

// validateRewrites is the complete, exact set of differences between the
// canonical validate.go and the mirror's: the cross-module
// supersession.ParseVersion dependency swapped for the local shim in
// version.go. Each pair is (canonical text, mirror text); the canonical text
// must still be present, so a canonical refactor that moves these anchors
// fails loudly instead of silently comparing garbage.
var validateRewrites = [][2]string{
	{
		"\t\"regexp\"\n\n\t\"github.com/trillium/parlay/tools/cli/internal/supersession\"\n)",
		"\t\"regexp\"\n)",
	},
	{
		"supersession.ParseVersion(d.Schema)",
		"parseSchemaVersion(d.Schema)",
	},
}

func TestMirrorMatchesCanonicalEngine(t *testing.T) {
	entries, err := os.ReadDir(canonicalDir)
	if err != nil {
		t.Fatalf("canonical tools/cli/internal/capability not found relative to this package (%s): %v — was the repo layout moved?", canonicalDir, err)
	}

	canonical := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(canonicalDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		canonical[e.Name()] = data
	}
	if len(canonical) == 0 {
		t.Fatalf("no .go files under %s — was the canonical package moved?", canonicalDir)
	}

	for name, want := range canonical {
		if name == "validate.go" {
			for _, rw := range validateRewrites {
				if !bytes.Contains(want, []byte(rw[0])) {
					t.Fatalf("canonical validate.go no longer contains the rewrite anchor %q — re-derive validateRewrites alongside the re-copy", rw[0])
				}
				want = bytes.ReplaceAll(want, []byte(rw[0]), []byte(rw[1]))
			}
		}
		got, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("%s exists in the canonical engine but not in this mirror — re-copy the tree: %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the canonical engine — change tools/cli/internal/capability first, then re-copy it here", name)
		}
	}

	local, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range local {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || mirrorOnly[name] {
			continue
		}
		if _, ok := canonical[name]; !ok {
			t.Errorf("%s exists only in the mirror — engine changes happen in tools/cli/internal/capability, not here", name)
		}
	}
}

// canonicalParseVersion is supersession.ParseVersion verbatim — the function
// the version.go shim reproduces. Pinned here so drift on either side fails.
const canonicalParseVersion = `func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version %q: want MAJOR.MINOR.PATCH", s)
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return Version{}, fmt.Errorf("version %q: field %q is not a plain non-negative integer", s, p)
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("version %q: %v", s, err)
		}
		n[i] = v
	}
	return Version{Major: n[0], Minor: n[1], Patch: n[2]}, nil
}`

func TestVersionShimMatchesSupersessionParseVersion(t *testing.T) {
	supPath := filepath.Join("..", "..", "..", "..", "tools", "cli", "internal", "supersession", "supersession.go")
	sup, err := os.ReadFile(supPath)
	if err != nil {
		t.Fatalf("canonical supersession.go not found (%s): %v — was the repo layout moved?", supPath, err)
	}
	if !bytes.Contains(sup, []byte(canonicalParseVersion)) {
		t.Fatal("supersession.ParseVersion no longer matches the pinned text — re-derive version.go's parseSchemaVersion from the new implementation, then update canonicalParseVersion here")
	}

	// The shim is the same body with only the names swapped: the function
	// renamed, and the cross-module Version type replaced by the local
	// schemaVersion. Anything else differing is a fork, not a shim.
	want := strings.Replace(canonicalParseVersion,
		"func ParseVersion(s string) (Version, error)",
		"func parseSchemaVersion(s string) (schemaVersion, error)", 1)
	want = strings.ReplaceAll(want, "Version{", "schemaVersion{")
	shim, err := os.ReadFile("version.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(shim, []byte(want)) {
		t.Fatal("version.go's parseSchemaVersion is not the pinned supersession.ParseVersion body with names swapped — re-derive it rather than patching it independently")
	}
}
