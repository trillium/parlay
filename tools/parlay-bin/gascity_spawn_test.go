package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestGascityStartStopPingLifecycle(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	workdir := t.TempDir()

	if err := gascitySpawn(stateDir, "gascity-lifecycle-test", "sleep 30", workdir, nil, "", ""); err != nil {
		t.Fatalf("gascitySpawn: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool { return gascityAlive(stateDir) })

	pid := readPID(stateDir)
	if pid == 0 {
		t.Fatal("expected a recorded pid after spawn")
	}
	if !pidAlive(pid) {
		t.Fatal("expected the spawned process to be alive")
	}

	if err := gascityStop(stateDir); err != nil {
		t.Fatalf("gascityStop: %v", err)
	}
	if gascityAlive(stateDir) {
		t.Fatal("expected session to be stopped")
	}
	if pidAlive(pid) {
		t.Fatal("expected process to be terminated after stop")
	}
	if _, err := os.Stat(pidFilePath(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("expected pid file to be cleaned up, stat err=%v", err)
	}
}

func TestGascitySpawnRefusesDuplicateSession(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	workdir := t.TempDir()

	if err := gascitySpawn(stateDir, "dup-test", "sleep 30", workdir, nil, "", ""); err != nil {
		t.Fatalf("first gascitySpawn: %v", err)
	}
	t.Cleanup(func() { _ = gascityStop(stateDir) })

	waitFor(t, 2*time.Second, func() bool { return gascityAlive(stateDir) })

	if err := gascitySpawn(stateDir, "dup-test", "sleep 30", workdir, nil, "", ""); err == nil {
		t.Fatal("expected second spawn against the same state dir to fail")
	}
}

func TestGascityStopIsIdempotentWhenNoSessionRecorded(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := gascityStop(stateDir); err != nil {
		t.Fatalf("expected nil error stopping a session that was never started, got: %v", err)
	}
}

func TestGascityStopCleansUpStalePidFile(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(pidFilePath(stateDir), []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gascityStop(stateDir); err != nil {
		t.Fatalf("expected nil error on a stale pid, got: %v", err)
	}
	if _, err := os.Stat(pidFilePath(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("expected stale pid file to be removed, stat err=%v", err)
	}
}

func TestGascityStopEscalatesToSigkillWhenProcessIgnoresSigterm(t *testing.T) {
	orig := stopGrace
	stopGrace = 300 * time.Millisecond
	t.Cleanup(func() { stopGrace = orig })

	stateDir := filepath.Join(t.TempDir(), "state")
	workdir := t.TempDir()

	if err := gascitySpawn(stateDir, "ignores-sigterm", "trap '' TERM; sleep 30", workdir, nil, "", ""); err != nil {
		t.Fatalf("gascitySpawn: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return gascityAlive(stateDir) })
	pid := readPID(stateDir)

	if err := gascityStop(stateDir); err != nil {
		t.Fatalf("gascityStop: %v", err)
	}
	if pidAlive(pid) {
		t.Fatal("expected SIGKILL escalation to terminate a process that ignores SIGTERM")
	}
}

// TestGascityTreehouseSidecarWrittenAndReturnedOnStop verifies the
// treehouse-return-before-teardown integration: gascity-spawn writes the
// sidecar when --worktree-path is supplied, and gascity-stop invokes
// `treehouse return <path>` (via a PATH-stubbed shim, so this test never
// touches a real treehouse pool) before signalling the process, then
// removes the sidecar.
func TestGascityTreehouseSidecarWrittenAndReturnedOnStop(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	workdir := t.TempDir()
	worktreePath := t.TempDir()

	shimDir := t.TempDir()
	callLog := filepath.Join(shimDir, "calls.log")
	shimScript := "#!/bin/sh\necho \"$@\" >> " + shimLogQuote(callLog) + "\nexit 0\n"
	shimPath := filepath.Join(shimDir, "treehouse")
	if err := os.WriteFile(shimPath, []byte(shimScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := gascitySpawn(stateDir, "treehouse-sidecar-test", "sleep 30", workdir, nil, worktreePath, ""); err != nil {
		t.Fatalf("gascitySpawn: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return gascityAlive(stateDir) })

	sidecar, err := os.ReadFile(treehousePathFile(stateDir))
	if err != nil {
		t.Fatalf("expected treehouse sidecar to be written: %v", err)
	}
	if strings.TrimSpace(string(sidecar)) != worktreePath {
		t.Fatalf("sidecar = %q, want %q", strings.TrimSpace(string(sidecar)), worktreePath)
	}

	if err := gascityStop(stateDir); err != nil {
		t.Fatalf("gascityStop: %v", err)
	}

	logData, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("expected treehouse shim to have been invoked: %v", err)
	}
	if !strings.Contains(string(logData), "return "+worktreePath) {
		t.Fatalf("expected shim log to contain %q, got %q", "return "+worktreePath, string(logData))
	}

	if _, err := os.Stat(treehousePathFile(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("expected treehouse sidecar to be removed after stop, stat err=%v", err)
	}
}

// shimLogQuote avoids embedding an unquoted path with spaces into the shim
// script; test temp dirs on macOS never contain spaces, but quoting is cheap.
func shimLogQuote(path string) string {
	return "\"" + path + "\""
}
