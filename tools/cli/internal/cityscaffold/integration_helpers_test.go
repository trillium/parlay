package cityscaffold

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGCSessionList invokes the real gc against the scaffold with GC_HOME and
// the supervisor port redirected away from the shared machine-wide singleton
// (contract §9.1) — this helper must never touch the default GC_HOME.
func runGCSessionList(t *testing.T, gc, cityDir string) (string, int) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "supervisor.toml"), []byte("[supervisor]\nport = 18372\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(gc, "--city", cityDir, "session", "list", "--json")
	cmd.Dir = home
	env := os.Environ()
	cmd.Env = append(env, "GC_HOME="+home)
	out, err := cmd.Output()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v", gc, err)
	}
	return string(out), code
}

// jsonHasEmptySessions parses gc's typed session-list JSON and reports
// whether it declares zero sessions.
func jsonHasEmptySessions(t *testing.T, out string) bool {
	t.Helper()
	var parsed struct {
		OK       bool              `json:"ok"`
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("gc session list output is not JSON: %v\n%s", err, out)
	}
	return parsed.OK && len(parsed.Sessions) == 0
}
