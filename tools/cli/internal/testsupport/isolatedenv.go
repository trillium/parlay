package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// systemPath is the PATH a hermetic test starts from: the OS tool directories
// only, so a developer's PATH shims (a `sleep` guard that exits 1, a `git`
// wrapper, a stub earlier on PATH) cannot change what a fixture's subprocess
// does.
const systemPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// scrubbedPrefixes are the ambient variable families that silently redirect or
// reconfigure the tools a test execs: GIT_* (a hook's GIT_DIR/GIT_INDEX_FILE
// points git at the wrong repo), PARLAY_* (state homes, server URLs), and BD_*/
// BEADS_*/BRAIN_* (the live bead federation).
var scrubbedPrefixes = []string{"GIT_", "PARLAY_", "BD_", "BEADS_", "BRAIN_"}

// scrubbedExact are single variables that make a shell run arbitrary code or
// change its behavior before the script starts.
var scrubbedExact = []string{"BASH_ENV", "ENV", "CDPATH"}

// IsolatedEnv makes the process environment hermetic for a test that execs
// subprocesses, restoring everything when the test ends. It pins PATH to the
// system directories plus exact approved tool paths and removes the GIT_*,
// PARLAY_*, BD_*, BEADS_* and BRAIN_* families and the shell-startup variables.
// A test that later prepends a stub directory to PATH still works: it layers on
// top of this PATH.
//
// Exported shell functions (BASH_FUNC_*) are removed too: they shadow commands
// by name inside bash regardless of PATH.
func IsolatedEnv(t *testing.T, approvedTools ...string) {
	t.Helper()
	toolDirs := make([]string, 0, len(approvedTools))
	for _, tool := range approvedTools {
		if !filepath.IsAbs(tool) {
			t.Fatalf("approved tool path must be absolute: %q", tool)
		}
		info, err := os.Stat(tool)
		if err != nil {
			t.Fatalf("stat approved tool %q: %v", tool, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("approved tool is not executable: %q", tool)
		}
		toolDir := t.TempDir()
		if err := os.Symlink(tool, filepath.Join(toolDir, filepath.Base(tool))); err != nil {
			t.Fatalf("link approved tool %q: %v", tool, err)
		}
		toolDirs = append(toolDirs, toolDir)
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if shouldScrub(k) {
			t.Setenv(k, "") // registers the restore
			_ = os.Unsetenv(k)
		}
	}
	t.Setenv("PATH", strings.Join(append(toolDirs, systemPath), string(os.PathListSeparator)))
}

func shouldScrub(k string) bool {
	if strings.HasPrefix(k, "BASH_FUNC_") {
		return true
	}
	for _, e := range scrubbedExact {
		if k == e {
			return true
		}
	}
	for _, p := range scrubbedPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}
