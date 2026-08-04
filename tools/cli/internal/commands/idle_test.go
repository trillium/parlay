// Tests for idle.go (ticket B9, ported from packages/cli/src/commands/
// idle.ts, which has no dedicated TS test file — these cases were derived
// directly from reading the implementation).
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdleDefaultHoursWritesPausedStatus(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	t.Setenv("PARLAY_STATUS_FILE", statusFile)
	t.Setenv("PARLAY_AGENT_ID", "agent-a")

	out := captureStdout(t, func() { Idle(nil) })
	if !strings.Contains(out, "status paused →") {
		t.Errorf("Idle() output = %q, want status-paused confirmation", out)
	}
	if !strings.Contains(out, "going quiet for 1h") {
		t.Errorf("Idle() output = %q, want whole-hour label '1h'", out)
	}

	data, err := os.ReadFile(statusFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "paused: idle for 1h") {
		t.Errorf("status file content = %q, want a 'paused: idle for 1h' line", data)
	}
}

func TestIdleFractionalHoursLabel(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	t.Setenv("PARLAY_STATUS_FILE", statusFile)
	t.Setenv("PARLAY_AGENT_ID", "agent-a")

	out := captureStdout(t, func() { Idle([]string{"2.5"}) })
	if !strings.Contains(out, "2.5h") {
		t.Errorf("Idle([2.5]) output = %q, want fractional label '2.5h'", out)
	}

	data, _ := os.ReadFile(statusFile)
	if !strings.Contains(string(data), "idle for 2.5h") {
		t.Errorf("status file content = %q, want 'idle for 2.5h'", data)
	}
}

func TestIdleWholeHoursNoTrailingDecimal(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	t.Setenv("PARLAY_STATUS_FILE", statusFile)
	t.Setenv("PARLAY_AGENT_ID", "agent-a")

	captureStdout(t, func() { Idle([]string{"3"}) })
	data, _ := os.ReadFile(statusFile)
	if !strings.Contains(string(data), "idle for 3h") || strings.Contains(string(data), "3.0h") {
		t.Errorf("status file content = %q, want 'idle for 3h' with no trailing .0", data)
	}
}

func TestIdleInvalidHoursDies(t *testing.T) {
	for _, bad := range []string{"0", "-1", "notanumber"} {
		t.Run(bad, func(t *testing.T) {
			statusFile := filepath.Join(t.TempDir(), "status")
			t.Setenv("PARLAY_STATUS_FILE", statusFile)
			t.Setenv("PARLAY_AGENT_ID", "agent-a")

			code, exited := withExitTrap(t, func() { Idle([]string{bad}) })
			if !exited || code != 2 {
				t.Errorf("Idle([%s]) exited=%v code=%d, want exit 2", bad, exited, code)
			}
			if _, err := os.Stat(statusFile); err == nil {
				t.Errorf("Idle([%s]) wrote a status file despite invalid input", bad)
			}
		})
	}
}

func TestIdlePrintsResumeAndParkGuidance(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	t.Setenv("PARLAY_STATUS_FILE", statusFile)
	t.Setenv("PARLAY_AGENT_ID", "agent-a")

	out := captureStdout(t, func() { Idle(nil) })
	if !strings.Contains(out, "parlay status working \"resuming\"") {
		t.Errorf("Idle() output = %q, want resume guidance", out)
	}
	if !strings.Contains(out, "parlay drawdown") || !strings.Contains(out, "identity --park") {
		t.Errorf("Idle() output = %q, want drawdown/identity --park alternative hint", out)
	}
}

func TestIdleUsesPARLAY_STATUS_FILE(t *testing.T) {
	dir := t.TempDir()
	statusFile := filepath.Join(dir, "custom-status-path")
	t.Setenv("PARLAY_STATUS_FILE", statusFile)
	t.Setenv("PARLAY_AGENT_ID", "agent-a")

	captureStdout(t, func() { Idle(nil) })
	if _, err := os.Stat(statusFile); err != nil {
		t.Errorf("Idle() did not write to PARLAY_STATUS_FILE override: %v", err)
	}
}
