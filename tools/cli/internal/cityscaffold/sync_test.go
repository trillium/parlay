package cityscaffold

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestMirrorMatchesAuthoredCity enforces that the embedded mirror under this
// package's city/ is byte-for-byte the repo's authored city/ tree. tools/cli
// is its own Go module, so it cannot go:embed ../../city directly; the mirror
// is the workaround and this test is what keeps it a mirror rather than a
// fork. On failure the fix is always the same direction: edit the authored
// city/ at the repo root, then re-copy it here —
//
//	rm -rf tools/cli/internal/cityscaffold/city
//	cp -R city tools/cli/internal/cityscaffold/city
func TestMirrorMatchesAuthoredCity(t *testing.T) {
	authored := filepath.Join("..", "..", "..", "..", "city")
	if _, err := os.Stat(filepath.Join(authored, "city.toml")); err != nil {
		t.Fatalf("authored city/ not found relative to this package (%s): %v — was the repo layout moved?", authored, err)
	}

	authoredFiles := map[string][]byte{}
	err := filepath.WalkDir(authored, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(authored, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		authoredFiles[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	mirrorFiles := map[string][]byte{}
	err = fs.WalkDir(sourceFS, "city", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := sourceFS.ReadFile(path)
		if err != nil {
			return err
		}
		mirrorFiles[strings.TrimPrefix(path, "city/")] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var all []string
	for p := range authoredFiles {
		all = append(all, p)
	}
	for p := range mirrorFiles {
		if _, ok := authoredFiles[p]; !ok {
			all = append(all, p)
		}
	}
	sort.Strings(all)

	for _, p := range all {
		a, inAuthored := authoredFiles[p]
		m, inMirror := mirrorFiles[p]
		switch {
		case !inMirror:
			t.Errorf("%s exists in the authored city/ but not in the embedded mirror — re-copy city/ over internal/cityscaffold/city/", p)
		case !inAuthored:
			t.Errorf("%s exists in the embedded mirror but not in the authored city/ — re-copy city/ over internal/cityscaffold/city/", p)
		case !bytes.Equal(a, m):
			t.Errorf("%s differs between the authored city/ and the embedded mirror — edit the authored tree, then re-copy it over internal/cityscaffold/city/", p)
		}
	}
}
