// Tests for identity --park: pin the handoff pointer (exactly like --submit)
// then shut down WITHOUT --reboot, leaving the bead OPEN. The middle of the
// three-exit model (decision-q3x): --submit restarts, --park pauses,
// --complete terminates.
//
// SAFETY: --park spawns ContextResetCmd() with inherited stdio, blocking.
// These tests stub a no-op "reincarnate" onto PATH (withFakeContextReset)
// instead of depending on a real context-reset/reincarnate binary, and pass
// --dry as a second guard — mirroring the caution in
// packages/cli/src/commands-identity-park.test.ts (which instead strips
// CLAUDECODE from the child's env so a real context-reset exits early).
package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParkWithExplicitIDPinsPointerAndReportsNoRestart(t *testing.T) {
	withFakeContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")

	logs := captureStdout(t, func() {
		CmdIdentity([]string{"--park", "handoff-abc", "--dry"})
	})

	raw, err := os.ReadFile(filepath.Join(home, "worker", "identity.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "> 📎 Handoff: handoff-abc — run `handoff show handoff-abc` for full session state") {
		t.Errorf("expected pinned pointer, got:\n%s", raw)
	}

	if !strings.Contains(logs, "identity parked for worker") {
		t.Errorf("expected parked message, got: %s", logs)
	}
	if !strings.Contains(logs, "handoff-abc") {
		t.Errorf("expected handoff id in output, got: %s", logs)
	}
	if !strings.Contains(logs, "OPEN") {
		t.Errorf("expected OPEN in output, got: %s", logs)
	}
	if !strings.Contains(logs, "WITHOUT restart") {
		t.Errorf("expected WITHOUT restart in output, got: %s", logs)
	}
	if strings.Contains(logs, "context reset") {
		t.Errorf("--park must never claim a context reset like --submit does, got: %s", logs)
	}
}

func TestParkIsIdentityOnly(t *testing.T) {
	withFakeContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")

	_, code, exited := runCapturingExit(t, func() {
		CmdScratchpad([]string{"--park", "handoff-abc"})
	})
	if !exited {
		t.Fatal("expected scratchpad --park to die")
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}
