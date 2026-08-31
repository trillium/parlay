// Package cityscaffold materialises parlay's Gas City city under
// $PARLAY_STATE_HOME (spawn-lift unit 3, epic task-4cfpv.9).
//
// The authored source of the city is the repo's city/ tree (PR #135) — the
// deployment layer (city.toml), the root city pack (pack.toml), and the
// parlay pack (packs/parlay/). This package carries a byte-for-byte embedded
// mirror of that tree under city/ here: tools/cli is its own Go module, so it
// cannot go:embed across the module boundary to the repo root. The mirror is
// NOT a fork — sync_test.go fails the build the moment the two trees differ,
// so the authored city/ stays the single place edits happen.
//
// Materialize is reconciliation, not installation: it writes managed files
// (create or overwrite-on-drift), ensures the .gc/ state directory exists,
// and touches nothing else — .gc/ contents in particular are machine-local
// state written by gc itself (site.toml, session state) and are never
// clobbered. `gc init`/`gc supervisor install` remain the install unit's job
// (P12); nothing here starts a city or talks to the shared supervisor.
package cityscaffold

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// all: so nothing in the mirror is silently skipped if a dot- or
// underscore-prefixed file ever lands in the authored tree.
//
//go:embed all:city
var sourceFS embed.FS

// Dir is where the scaffold lives: $PARLAY_STATE_HOME/gascity/city. The
// gascity/ parent leaves room for siblings (a built gc, logs) without them
// living inside the city root gc scans.
func Dir() string {
	return filepath.Join(config.StateHome(), "gascity", "city")
}

// FileOutcome says what Materialize did to one managed file.
type FileOutcome string

const (
	Created   FileOutcome = "created"
	Updated   FileOutcome = "updated"
	Unchanged FileOutcome = "unchanged"
)

// Result reports one Materialize run: the scaffold root and each managed
// file's outcome, keyed by path relative to the root.
type Result struct {
	Dir   string
	Files map[string]FileOutcome
}

// Materialize reconciles the scaffold at Dir(): every file of the embedded
// city tree is created or overwritten-on-drift, and the .gc/ state directory
// is created empty if absent. Unmanaged files (everything under .gc/, plus
// anything a human dropped alongside) are left alone. Idempotent — a second
// run reports every file Unchanged.
func Materialize() (Result, error) {
	root := Dir()
	res := Result{Dir: root, Files: map[string]FileOutcome{}}

	err := fs.WalkDir(sourceFS, "city", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, "city")
		rel = strings.TrimPrefix(rel, "/")
		dest := filepath.Join(root, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		want, err := sourceFS.ReadFile(path)
		if err != nil {
			return err
		}
		// The embed FS drops the executable bit; the doctor pack's run.sh
		// must come back executable for gc to run it.
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}

		have, readErr := os.ReadFile(dest)
		switch {
		case readErr == nil && bytes.Equal(have, want):
			res.Files[rel] = Unchanged
			// A previous run (or a hand chmod) may have lost the mode.
			return os.Chmod(dest, mode)
		case readErr == nil:
			res.Files[rel] = Updated
		default:
			res.Files[rel] = Created
		}
		if err := os.WriteFile(dest, want, mode); err != nil {
			return err
		}
		return os.Chmod(dest, mode)
	})
	if err != nil {
		return res, fmt.Errorf("materialize city scaffold at %s: %w", root, err)
	}

	// The .gc/ state directory: gc owns everything inside it, we only make
	// sure the directory exists. MkdirAll is a no-op on an existing dir and
	// never truncates contents.
	if err := os.MkdirAll(filepath.Join(root, ".gc"), 0o755); err != nil {
		return res, fmt.Errorf("materialize city scaffold at %s: %w", root, err)
	}

	// Seed the authored workspace identity into .gc/site.toml, create-only.
	// The pinned gc deprecates identity fields in city.toml (they belong in
	// machine-local .gc/site.toml), so the authored city/ no longer declares
	// workspace.name — without this seed a fresh scaffold's identity would
	// fall back to the directory basename ("city"). gc owns the file from
	// here: an existing site.toml, whatever its content, is never touched.
	sitePath := filepath.Join(root, ".gc", "site.toml")
	if _, err := os.Stat(sitePath); os.IsNotExist(err) {
		if err := os.WriteFile(sitePath, []byte("workspace_name = \"parlay\"\n"), 0o644); err != nil {
			return res, fmt.Errorf("materialize city scaffold at %s: %w", root, err)
		}
	} else if err != nil {
		return res, fmt.Errorf("materialize city scaffold at %s: %w", root, err)
	}
	return res, nil
}
