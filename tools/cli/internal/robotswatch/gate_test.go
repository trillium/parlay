// Tests for the mechanic dispatch kill switch: gate ON/OFF via sentinel and
// env, offset advances when gate is OFF, and env/sentinel precedence.
package robotswatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSentinel creates the mechanic-dispatch.off sentinel inside the given
// state dir (mirrors mechanicDispatchSentinelPath).
func writeSentinel(t *testing.T, stateDir string) string {
	t.Helper()
	path := filepath.Join(stateDir, "mechanic-dispatch.off")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("writeSentinel: %v", err)
	}
	return path
}

func TestDispatchMechanicGateOnAttemptsSpawn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_MECHANIC_DISPATCH", "") // ensure env is neutral

	out := captureStderr(t, func() { dispatchMechanic("robots-aaa", false) })
	// mechanic-dispatch binary won't exist in tests; we just need proof the
	// gate didn't block it — either "not runnable" or "exited N" lands.
	if strings.Contains(out, "mechanic dispatch is OFF") {
		t.Fatalf("gate should be ON but got: %s", out)
	}
}

func TestDispatchMechanicGateOffViaSentinelSkips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_MECHANIC_DISPATCH", "") // no env override
	writeSentinel(t, dir)

	out := captureStderr(t, func() { dispatchMechanic("robots-bbb", false) })
	if !strings.Contains(out, "mechanic dispatch is OFF") {
		t.Fatalf("expected OFF skip line, got: %q", out)
	}
	if !strings.Contains(out, "robots-bbb") {
		t.Fatalf("expected id in skip line, got: %q", out)
	}
}

func TestDispatchMechanicGateOffViaEnvSkips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_MECHANIC_DISPATCH", "off")
	// no sentinel — env alone is enough

	out := captureStderr(t, func() { dispatchMechanic("robots-ccc", false) })
	if !strings.Contains(out, "mechanic dispatch is OFF") {
		t.Fatalf("expected OFF skip line, got: %q", out)
	}
}

func TestDispatchMechanicEnvOnDoesNotOverrideSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_MECHANIC_DISPATCH", "on") // env=on must NOT override sentinel
	writeSentinel(t, dir)

	out := captureStderr(t, func() { dispatchMechanic("robots-ddd", false) })
	if !strings.Contains(out, "mechanic dispatch is OFF") {
		t.Fatalf("sentinel must win over env=on, got: %q", out)
	}
}

// TestTickAdvancesOffsetWhenGateOff verifies that the tailer advances its
// byte offset even when the dispatch gate is OFF — so re-enabling does NOT
// replay the backlog.
func TestTickAdvancesOffsetWhenGateOff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_MECHANIC_DISPATCH", "off") // gate off via env

	evFile := filepath.Join(dir, "events.jsonl")
	line := `{"id":"robots-fff"}` + "\n"
	if err := os.WriteFile(evFile, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROBOTS_EVENTS_FILE", evFile)

	// prime offset at 0 so tick() reads the line
	writeOffset(0)

	captureStderr(t, func() { tick(false) })

	// offset must have advanced to len(line) regardless of gate
	got := readOffset(0)
	want := int64(len(line))
	if got != want {
		t.Fatalf("offset after tick with gate OFF: got %d, want %d", got, want)
	}
}

// TestMechanicDispatchOffPrecedence table covers all sentinel×env combinations.
func TestMechanicDispatchOffPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		envVal      string
		hasSentinel bool
		wantOff     bool
	}{
		{"no_env_no_sentinel", "", false, false},
		{"env_on_no_sentinel", "on", false, false},
		{"env_off_no_sentinel", "off", false, true},
		{"no_env_sentinel", "", true, true},
		{"env_on_sentinel", "on", true, true}, // sentinel wins over env=on
		{"env_off_sentinel", "off", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PARLAY_STATE_HOME", dir)
			t.Setenv("PARLAY_MECHANIC_DISPATCH", tc.envVal)
			if tc.hasSentinel {
				writeSentinel(t, dir)
			}
			got := mechanicDispatchOff()
			if got != tc.wantOff {
				t.Fatalf("mechanicDispatchOff() = %v, want %v", got, tc.wantOff)
			}
		})
	}
}
