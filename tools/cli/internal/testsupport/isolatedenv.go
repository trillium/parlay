package testsupport

import (
	"os"
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
// system directories and removes the GIT_*, PARLAY_*, BD_*, BEADS_* and
// BRAIN_* families and the shell-startup variables. A test that later prepends
// a stub directory to PATH still works: it layers on top of this PATH.
//
// Exported shell functions (BASH_FUNC_*) are removed too: they shadow commands
// by name inside bash regardless of PATH.
func IsolatedEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if shouldScrub(k) {
			t.Setenv(k, "") // registers the restore
			_ = os.Unsetenv(k)
		}
	}
	t.Setenv("PATH", systemPath)
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
