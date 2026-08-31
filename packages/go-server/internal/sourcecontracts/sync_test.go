package sourcecontracts

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestMirrorMatchesCanonicalContracts enforces that the embedded mirror
// under this package's contracts/ is byte-for-byte the repo's canonical
// contracts/sources/ tree — the cityscaffold pattern: this module cannot
// go:embed across the module boundary, the mirror is the workaround, and
// this test is what keeps it a mirror rather than a fork. On failure the fix
// is always the same direction: enroll/supersede in the canonical
// contracts/sources/ at the repo root, then re-copy it here (see the package
// comment for the exact commands).
func TestMirrorMatchesCanonicalContracts(t *testing.T) {
	canonical := filepath.Join("..", "..", "..", "..", "contracts", "sources")
	entries, err := os.ReadDir(canonical)
	if err != nil {
		t.Fatalf("canonical contracts/sources/ not found relative to this package (%s): %v — was the repo layout moved?", canonical, err)
	}

	canonicalFiles := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(canonical, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		canonicalFiles[e.Name()] = data
	}

	mirrorFiles := map[string][]byte{}
	err = fs.WalkDir(contractsFS, "contracts", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := contractsFS.ReadFile(path)
		if err != nil {
			return err
		}
		mirrorFiles[strings.TrimPrefix(path, "contracts/")] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var all []string
	for p := range canonicalFiles {
		all = append(all, p)
	}
	for p := range mirrorFiles {
		if _, ok := canonicalFiles[p]; !ok {
			all = append(all, p)
		}
	}
	sort.Strings(all)

	for _, p := range all {
		c, inCanonical := canonicalFiles[p]
		m, inMirror := mirrorFiles[p]
		switch {
		case !inMirror:
			t.Errorf("%s exists in canonical contracts/sources/ but not in the embedded mirror — re-copy the tree", p)
		case !inCanonical:
			t.Errorf("%s exists only in the embedded mirror — enrollment happens in canonical contracts/sources/, not here", p)
		case !bytes.Equal(c, m):
			t.Errorf("%s differs between canonical contracts/sources/ and the mirror — re-copy the tree", p)
		}
	}
}
