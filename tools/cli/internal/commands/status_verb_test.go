// Mirrors packages/cli/src/commands-status.test.ts's cases: buildStatusLine
// is the load-bearing piece (its output must parse under firstmate's
// fm-classify-lib.sh grammar), plus statusSink's env indirection and the
// CLI wrapper's read/append behavior.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildStatusLineBareVerbAndNote(t *testing.T) {
	got := buildStatusLine("working", "", "building the verb")
	want := "working: building the verb\n"
	if got != want {
		t.Errorf("buildStatusLine() = %q, want %q", got, want)
	}
}

func TestBuildStatusLineKeyedPutsTokenBetweenVerbAndColon(t *testing.T) {
	got := buildStatusLine("needs-decision", "api-shape", "which shape")
	want := "needs-decision [key=api-shape]: which shape\n"
	if got != want {
		t.Errorf("buildStatusLine() = %q, want %q", got, want)
	}
}

func TestBuildStatusLineResolvedKeyedClosesMatchingDecision(t *testing.T) {
	got := buildStatusLine("resolved", "api-shape", "went dual-mode")
	want := "resolved [key=api-shape]: went dual-mode\n"
	if got != want {
		t.Errorf("buildStatusLine() = %q, want %q", got, want)
	}
}

func TestBuildStatusLineEmptyNoteOmitsTrailingSpace(t *testing.T) {
	got := buildStatusLine("done", "", "")
	want := "done:\n"
	if got != want {
		t.Errorf("buildStatusLine() = %q, want %q", got, want)
	}
}

func TestBuildStatusLineKeyedEmptyNoteKeepsTokenDropsSpace(t *testing.T) {
	got := buildStatusLine("blocked", "deploy", "")
	want := "blocked [key=deploy]:\n"
	if got != want {
		t.Errorf("buildStatusLine() = %q, want %q", got, want)
	}
}

func TestStatusSinkPrefersEnvFile(t *testing.T) {
	t.Setenv("PARLAY_STATUS_FILE", "/tmp/fm-injected.status")
	t.Setenv("PARLAY_AGENT_ID", "")

	_, file := statusSink()
	if file != "/tmp/fm-injected.status" {
		t.Errorf("statusSink().file = %q, want /tmp/fm-injected.status", file)
	}
}

func TestStatusSinkFallsBackToAgentHome(t *testing.T) {
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	t.Setenv("PARLAY_AGENT_ID", "unit-test-agent")

	_, file := statusSink()
	if !strings.HasSuffix(file, filepath.Join("unit-test-agent", "status")) {
		t.Errorf("statusSink().file = %q, want suffix .../unit-test-agent/status", file)
	}
}

func TestStatusVerbAppendsAndBareReadReturnsIt(t *testing.T) {
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	t.Setenv("PARLAY_AGENT_ID", "test-status-agent")

	out := captureStdout(t, func() { StatusVerb([]string{"working", "starting", "task"}) })
	if !strings.Contains(out, "status working") {
		t.Errorf("StatusVerb(working) output = %q, want a confirmation", out)
	}

	out = captureStdout(t, func() { StatusVerb(nil) })
	if out != "working: starting task\n" {
		t.Errorf("StatusVerb(nil) = %q, want the appended line back", out)
	}
}

func TestStatusVerbKeyedAppend(t *testing.T) {
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	t.Setenv("PARLAY_AGENT_ID", "test-status-agent-keyed")

	captureStdout(t, func() { StatusVerb([]string{"needs-decision", "--key", "api-shape", "which", "shape"}) })
	out := captureStdout(t, func() { StatusVerb(nil) })
	if out != "needs-decision [key=api-shape]: which shape\n" {
		t.Errorf("StatusVerb read = %q, want keyed line", out)
	}
}

func TestStatusVerbNoStatusYetMessage(t *testing.T) {
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	t.Setenv("PARLAY_AGENT_ID", "test-status-agent-empty")

	out := captureStdout(t, func() { StatusVerb(nil) })
	if !strings.Contains(out, "no status yet") {
		t.Errorf("StatusVerb(nil) with no file = %q, want a no-status-yet message", out)
	}
}

func TestStatusVerbRejectsUnknownVerb(t *testing.T) {
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	t.Setenv("PARLAY_AGENT_ID", "test-status-agent-bad-verb")

	code, exited := withExitTrap(t, func() { StatusVerb([]string{"bogus", "note"}) })
	if !exited || code != 2 {
		t.Errorf("StatusVerb(bogus) exit = (%d, %v), want (2, true)", code, exited)
	}
}

func TestStatusVerbRejectsInvalidKey(t *testing.T) {
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	t.Setenv("PARLAY_AGENT_ID", "test-status-agent-bad-key")

	code, exited := withExitTrap(t, func() { StatusVerb([]string{"working", "--key", "not a slug", "note"}) })
	if !exited || code != 2 {
		t.Errorf("StatusVerb(--key invalid) exit = (%d, %v), want (2, true)", code, exited)
	}
}

func TestStatusVerbDiesWithNoIdentity(t *testing.T) {
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_AGENT_ID", "")

	code, exited := withExitTrap(t, func() { StatusVerb([]string{"working", "note"}) })
	if !exited || code != 2 {
		t.Errorf("StatusVerb() with no identity exit = (%d, %v), want (2, true)", code, exited)
	}
}

func TestStatusFileForAgentIgnoresCallersOwnIdentity(t *testing.T) {
	// Fidelity fix regression: crew-state's resolution must be keyed by the
	// PASSED agentID, not the caller's own PARLAY_AGENT_ID/PARLAY_STATUS_FILE.
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_STATUS_FILE", "/tmp/some-other-callers-file.status")
	t.Setenv("PARLAY_AGENT_ID", "the-caller")

	got := statusFileForAgent("the-target")
	want := filepath.Join(home, "the-target", "status")
	if got != want {
		t.Errorf("statusFileForAgent(%q) = %q, want %q", "the-target", got, want)
	}

	// Sanity: the caller's own sink resolves somewhere else entirely.
	_, callerFile := statusSink()
	if callerFile == got {
		t.Errorf("caller's own statusSink() (%q) unexpectedly matches target's file", callerFile)
	}
}

func TestStatusVerbHelpDoesNotPanic(t *testing.T) {
	out := captureStdout(t, func() { StatusVerb([]string{"--help"}) })
	if !strings.Contains(out, "parlay status") {
		t.Errorf("StatusVerb(--help) = %q, want the status help text", out)
	}
}

func TestStatusSinkMkdirsAgentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_AGENT_ID", "mkdir-agent")

	_, file := statusSink()
	dir := filepath.Dir(file)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("statusSink() did not create agent dir %q", dir)
	}
}
