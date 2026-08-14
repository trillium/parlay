// Tests for `parlay mechanic on|off|status`.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMechanicOffCreatesSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)

	out := captureStdout(t, func() { Mechanic([]string{"off"}) })
	if !strings.Contains(out, "OFF") {
		t.Fatalf("expected OFF in output, got: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "mechanic-dispatch.off")); err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
}

func TestMechanicOnRemovesSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	sentinelPath := filepath.Join(dir, "mechanic-dispatch.off")
	if err := os.WriteFile(sentinelPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { Mechanic([]string{"on"}) })
	if !strings.Contains(out, "ON") {
		t.Fatalf("expected ON in output, got: %q", out)
	}
	if _, err := os.Stat(sentinelPath); !os.IsNotExist(err) {
		t.Fatalf("sentinel still present after mechanic on")
	}
}

func TestMechanicOnIdempotentWhenNoSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	// no sentinel — should succeed without error
	out := captureStdout(t, func() { Mechanic([]string{"on"}) })
	if !strings.Contains(out, "ON") {
		t.Fatalf("expected ON, got: %q", out)
	}
}

// TestMechanicStatusTable covers all state combinations via mechStateInfo.
func TestMechanicStatusTable(t *testing.T) {
	cases := []struct {
		name        string
		envVal      string
		hasSentinel bool
		wantOff     bool
		wantSubstr  string
	}{
		{
			name:    "on_no_sentinel_no_env",
			wantOff: false, wantSubstr: "mechanic-dispatch.off",
		},
		{
			name:   "on_env_on_no_sentinel",
			envVal: "on", wantOff: false, wantSubstr: "mechanic-dispatch.off",
		},
		{
			name:        "off_via_sentinel",
			hasSentinel: true, wantOff: true, wantSubstr: "sentinel file present",
		},
		{
			name:   "off_via_env",
			envVal: "off", wantOff: true, wantSubstr: "PARLAY_MECHANIC_DISPATCH=off",
		},
		{
			name:   "off_env_on_with_sentinel",
			envVal: "on", hasSentinel: true,
			wantOff: true, wantSubstr: "PARLAY_MECHANIC_DISPATCH=on ignored",
		},
		{
			name:   "off_env_off_with_sentinel",
			envVal: "off", hasSentinel: true,
			wantOff: true, wantSubstr: "sentinel also present",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PARLAY_STATE_HOME", dir)
			t.Setenv("PARLAY_MECHANIC_DISPATCH", tc.envVal)
			if tc.hasSentinel {
				if err := os.WriteFile(filepath.Join(dir, "mechanic-dispatch.off"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			s := mechStateInfo()
			if s.Off != tc.wantOff {
				t.Fatalf("Off=%v, want %v", s.Off, tc.wantOff)
			}
			combined := s.Reason + " " + s.Path
			if !strings.Contains(combined, tc.wantSubstr) {
				t.Fatalf("want %q in reason/path, got reason=%q path=%q", tc.wantSubstr, s.Reason, s.Path)
			}
		})
	}
}

func TestMechanicStatusOutputContainsPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_MECHANIC_DISPATCH", "")

	out := captureStdout(t, func() { Mechanic([]string{"status"}) })
	if !strings.Contains(out, "mechanic-dispatch.off") {
		t.Fatalf("expected sentinel path in status output, got: %q", out)
	}
}

func TestMechanicUnknownSubcommandExits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	code, exited := withExitTrap(t, func() { Mechanic([]string{"bogus"}) })
	if !exited {
		t.Fatal("expected exit on unknown subcommand")
	}
	if code != 2 {
		t.Fatalf("expected exit 2 (usage), got %d", code)
	}
}
