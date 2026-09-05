package spawn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubStoreCLI puts a fake binary named `name` first on PATH that always
// prints jsonOut on stdout and exits 0, for the duration of the test.
func stubStoreCLI(t *testing.T, name, jsonOut string) {
	t.Helper()
	binDir := t.TempDir()
	stub := filepath.Join(binDir, name)
	body := "#!/usr/bin/env bash\ncat <<'EOF'\n" + jsonOut + "\nEOF\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s stub: %v", name, err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// emptyPATH points PATH at a directory with nothing in it, so exec.LookPath
// fails for every binary — simulating "no store CLI installed at all".
func emptyPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestBeadGateRequiredNoBeadRefuses(t *testing.T) {
	t.Setenv("PARLAY_STATE_HOME", t.TempDir())
	err := beadGate("", true, false)
	if err == nil {
		t.Fatal("expected a refusal when beads are required and no --bead given")
	}
	bge, ok := err.(*beadGateError)
	if !ok {
		t.Fatalf("expected *beadGateError, got %T", err)
	}
	if bge.exitCode != 2 {
		t.Errorf("expected exit code 2, got %d", bge.exitCode)
	}
}

func TestBeadGateForceDowngradesRequired(t *testing.T) {
	if err := beadGate("", true, true); err != nil {
		t.Errorf("--force should downgrade beads-required to off, got error: %v", err)
	}
}

func TestBeadGateNotRequiredNoBeadPasses(t *testing.T) {
	if err := beadGate("", false, false); err != nil {
		t.Errorf("expected no error when beads are not required, got %v", err)
	}
}

func TestBeadGateSkipCheckEnvBypassesVerification(t *testing.T) {
	t.Setenv("PARLAY_SPAWN_SKIP_BEAD_CHECK", "1")
	stubStoreCLI(t, "task", `{"id":"task-fake1","status":"closed"}`)
	if err := beadGate("task-fake1", false, false); err != nil {
		t.Errorf("PARLAY_SPAWN_SKIP_BEAD_CHECK should bypass verification entirely, got %v", err)
	}
}

func TestBeadGateNoStoreCLIProceedsUnverified(t *testing.T) {
	emptyPATH(t)
	if err := beadGate("task-fake1", false, false); err != nil {
		t.Errorf("no store CLI on PATH should proceed unverified, got %v", err)
	}
}

func TestBeadGateClosedBeadRefuses(t *testing.T) {
	stubStoreCLI(t, "task", `{"id":"task-fake1","status":"closed"}`)
	err := beadGate("task-fake1", false, false)
	if err == nil {
		t.Fatal("expected a refusal for a closed bead")
	}
	bge, ok := err.(*beadGateError)
	if !ok {
		t.Fatalf("expected *beadGateError, got %T", err)
	}
	if bge.exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", bge.exitCode)
	}
}

func TestBeadGateOpenBeadPasses(t *testing.T) {
	stubStoreCLI(t, "task", `{"id":"task-fake1","status":"open"}`)
	if err := beadGate("task-fake1", false, false); err != nil {
		t.Errorf("expected no error for an open bead, got %v", err)
	}
}

func TestBeadGateUnresolvableStatusRefuses(t *testing.T) {
	stubStoreCLI(t, "task", `{"id":"some-other-id","status":"open"}`)
	err := beadGate("task-fake1", false, false)
	if err == nil {
		t.Fatal("expected a refusal when the bead's status could not be resolved")
	}
	bge, ok := err.(*beadGateError)
	if !ok {
		t.Fatalf("expected *beadGateError, got %T", err)
	}
	if bge.exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", bge.exitCode)
	}
}

func TestResolveBeadStatusPrefersStorePrefixOverBd(t *testing.T) {
	stubStoreCLI(t, "bd", `{"id":"task-fake1","status":"wrong-binary"}`)
	stubStoreCLI(t, "task", `{"id":"task-fake1","status":"open"}`)
	status, resolvable := resolveBeadStatus("task-fake1")
	if !resolvable {
		t.Fatal("expected resolvable=true")
	}
	if status != "open" {
		t.Errorf("expected the task-prefixed binary to win over bd, got status=%q", status)
	}
}

func TestExtractBeadStatus(t *testing.T) {
	cases := []struct {
		name   string
		json   string
		beadID string
		want   string
	}{
		{"bare object", `{"id":"task-a1","status":"open"}`, "task-a1", "open"},
		{"array", `[{"id":"task-a1","status":"in_progress"},{"id":"task-b2","status":"closed"}]`, "task-a1", "in_progress"},
		{"wrapped envelope", `{"result":{"id":"task-a1","status":"closed"}}`, "task-a1", "closed"},
		{"no match", `{"id":"task-other","status":"open"}`, "task-a1", ""},
		{"empty bytes", ``, "task-a1", ""},
		{"invalid json", `not json`, "task-a1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractBeadStatus([]byte(tc.json), tc.beadID)
			if got != tc.want {
				t.Errorf("extractBeadStatus(%q, %q) = %q, want %q", tc.json, tc.beadID, got, tc.want)
			}
		})
	}
}

func TestBeadsRequiredErrorTemplateUserOverrideWins(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", stateHome)
	tmplDir := filepath.Join(stateHome, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const custom = "custom refusal text\n"
	if err := os.WriteFile(filepath.Join(tmplDir, "beads-required-error.txt"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	got := beadsRequiredErrorTemplate()
	if strings.TrimSpace(got) != strings.TrimSpace(custom) {
		t.Errorf("expected the user override template, got %q", got)
	}
}
