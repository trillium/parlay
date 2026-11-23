package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatedEnvPinsPathAndScrubsAmbientVariables(t *testing.T) {
	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "sleep"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_DIR", "/nonexistent")
	t.Setenv("PARLAY_STATE_HOME", "/nonexistent")
	t.Setenv("BD_NAME", "live")
	t.Setenv("BASH_ENV", "/nonexistent")

	IsolatedEnv(t)

	if strings.Contains(os.Getenv("PATH"), shim) {
		t.Errorf("PATH still carries the shim dir: %s", os.Getenv("PATH"))
	}
	for _, k := range []string{"GIT_DIR", "PARLAY_STATE_HOME", "BD_NAME", "BASH_ENV"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s survived isolation (%q)", k, v)
		}
	}
	// The shim's `sleep` (which exits 1) must not be the one that runs.
	if err := exec.Command("sleep", "0").Run(); err != nil {
		t.Errorf("sleep resolved to the shim: %v", err)
	}
}

func TestIsolatedEnvRejectsShimNamedForSystemTool(t *testing.T) {
	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

	IsolatedEnv(t)

	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Errorf("git resolved to the shim: %v", err)
	}
}

func TestIsolatedEnvPinsOnlyApprovedTool(t *testing.T) {
	tools := t.TempDir()
	approved := filepath.Join(tools, "bd")
	if err := os.WriteFile(approved, []byte("#!/bin/sh\nprintf approved\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "git"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))

	IsolatedEnv(t, approved)

	if output, err := exec.Command("bd").Output(); err != nil || string(output) != "approved" {
		t.Errorf("approved tool output = %q, %v; want approved", output, err)
	}
	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Errorf("unapproved sibling tool survived isolation: %v", err)
	}
}

func TestIsolatedEnvRestoresOnCleanup(t *testing.T) {
	t.Setenv("GIT_DIR", "/restore-me")
	t.Run("inner", func(t *testing.T) { IsolatedEnv(t) })
	if got := os.Getenv("GIT_DIR"); got != "/restore-me" {
		t.Errorf("GIT_DIR after cleanup = %q, want /restore-me", got)
	}
}
