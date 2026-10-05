package spawn

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

func TestSubprocessStartStopPingLifecycle(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	workdir := t.TempDir()

	if err := subprocessSpawn(stateDir, "subprocess-lifecycle-test", "sleep 30", workdir, nil, "", ""); err != nil {
		t.Fatalf("subprocessSpawn: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool { return subprocessAlive(stateDir) })

	pid := readPID(stateDir)
	if pid == 0 {
		t.Fatal("expected a recorded pid after spawn")
	}
	if !pidAlive(pid) {
		t.Fatal("expected the spawned process to be alive")
	}

	if err := subprocessStop(stateDir); err != nil {
		t.Fatalf("subprocessStop: %v", err)
	}
	if subprocessAlive(stateDir) {
		t.Fatal("expected session to be stopped")
	}
	if pidAlive(pid) {
		t.Fatal("expected process to be terminated after stop")
	}
	if _, err := os.Stat(pidFilePath(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("expected pid file to be cleaned up, stat err=%v", err)
	}
}

func TestSubprocessSpawnRefusesDuplicateSession(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	workdir := t.TempDir()

	if err := subprocessSpawn(stateDir, "dup-test", "sleep 30", workdir, nil, "", ""); err != nil {
		t.Fatalf("first subprocessSpawn: %v", err)
	}
	t.Cleanup(func() { _ = subprocessStop(stateDir) })

	waitFor(t, 2*time.Second, func() bool { return subprocessAlive(stateDir) })

	if err := subprocessSpawn(stateDir, "dup-test", "sleep 30", workdir, nil, "", ""); err == nil {
		t.Fatal("expected second spawn against the same state dir to fail")
	}
}

func TestSubprocessStopIsIdempotentWhenNoSessionRecorded(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := subprocessStop(stateDir); err != nil {
		t.Fatalf("expected nil error stopping a session that was never started, got: %v", err)
	}
}

func TestSubprocessStopCleansUpStalePidFile(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(pidFilePath(stateDir), []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := subprocessStop(stateDir); err != nil {
		t.Fatalf("expected nil error on a stale pid, got: %v", err)
	}
	if _, err := os.Stat(pidFilePath(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("expected stale pid file to be removed, stat err=%v", err)
	}
}

func TestSubprocessStopEscalatesToSigkillWhenProcessIgnoresSigterm(t *testing.T) {
	orig := stopGrace
	stopGrace = 300 * time.Millisecond
	t.Cleanup(func() { stopGrace = orig })

	stateDir := filepath.Join(t.TempDir(), "state")
	workdir := t.TempDir()

	if err := subprocessSpawn(stateDir, "ignores-sigterm", "trap '' TERM; sleep 30", workdir, nil, "", ""); err != nil {
		t.Fatalf("subprocessSpawn: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return subprocessAlive(stateDir) })
	pid := readPID(stateDir)

	if err := subprocessStop(stateDir); err != nil {
		t.Fatalf("subprocessStop: %v", err)
	}
	if pidAlive(pid) {
		t.Fatal("expected SIGKILL escalation to terminate a process that ignores SIGTERM")
	}
}

// TestSubprocessOldNameSessionStillStopsAfterRename is the rename-compat
// guarantee (Gas City spawn lift unit 1, DoD #5): an agent spawned under the
// OLD launcher name must still be stoppable after the rename. The mechanism
// is the on-disk state dir, which keeps its literal "gascity" segment (see
// defaultSubprocessStateDir) — renaming the directory would orphan every
// session already running under the old name. So the default state dir must
// still resolve to .../<agent-id>/gascity, and a session spawned at that
// pre-rename path must be found and stopped by the renamed stop path.
func TestSubprocessOldNameSessionStillStopsAfterRename(t *testing.T) {
	agentHome := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", agentHome)

	stateDir := defaultSubprocessStateDir("compat-rename")
	if filepath.Base(stateDir) != "gascity" {
		t.Fatalf("default state dir base = %q, want the unchanged literal 'gascity' (on-disk compat for pre-rename sessions)", filepath.Base(stateDir))
	}
	workdir := t.TempDir()

	if err := subprocessSpawn(stateDir, "compat-rename", "sleep 30", workdir, nil, "", ""); err != nil {
		t.Fatalf("subprocessSpawn at the pre-rename state dir: %v", err)
	}
	t.Cleanup(func() {
		pid := readPID(stateDir)
		if pid != 0 && pidAlive(pid) {
			_ = subprocessStop(stateDir)
		}
	})
	waitFor(t, 2*time.Second, func() bool { return subprocessAlive(stateDir) })
	pid := readPID(stateDir)
	if pid == 0 {
		t.Fatal("expected a recorded pid at the pre-rename state dir")
	}

	if err := subprocessStop(stateDir); err != nil {
		t.Fatalf("subprocessStop after the rename: %v", err)
	}
	if pidAlive(pid) {
		t.Fatal("expected the pre-rename session to be terminated by the renamed stop path")
	}
	if _, err := os.Stat(pidFilePath(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("expected pid file to be cleaned up, stat err=%v", err)
	}
}

// TestSubprocessTreehouseSidecarWrittenAndReturnedOnStop verifies the
// treehouse-return-before-teardown integration: subprocess-spawn writes the
// sidecar when --worktree-path is supplied, and subprocess-stop invokes
// `treehouse return <path>` (via a PATH-stubbed shim, so this test never
// touches a real treehouse pool) before signalling the process, then
// removes the sidecar.
func TestSubprocessTreehouseSidecarWrittenAndReturnedOnStop(t *testing.T) {
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

	if err := subprocessSpawn(stateDir, "treehouse-sidecar-test", "sleep 30", workdir, nil, worktreePath, ""); err != nil {
		t.Fatalf("subprocessSpawn: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return subprocessAlive(stateDir) })

	sidecar, err := os.ReadFile(treehousePathFile(stateDir))
	if err != nil {
		t.Fatalf("expected treehouse sidecar to be written: %v", err)
	}
	if strings.TrimSpace(string(sidecar)) != worktreePath {
		t.Fatalf("sidecar = %q, want %q", strings.TrimSpace(string(sidecar)), worktreePath)
	}

	if err := subprocessStop(stateDir); err != nil {
		t.Fatalf("subprocessStop: %v", err)
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

// (captureStdout is the shared helper from spawn_test.go.)
//
// TestSubprocessHelpIsAnsweredNotExecuted is the regression pin for the rough
// edge that made these three verbs undiscoverable: `--help` was parsed as the
// leading agent-id positional, so
//
//	parlay subprocess-stop --help   exited 0 in SILENCE, having "stopped" a
//	                                session literally named --help
//	parlay subprocess-ping --help   exited 1 with no output at all
//	parlay subprocess-spawn --help  dumped usage to stderr and exited 2
//
// while the top-level usage text promised "Any subcommand accepts --help".
// Each verb must now print the shared usage to stdout and exit 0.
func TestSubprocessHelpIsAnsweredNotExecuted(t *testing.T) {
	// A state dir that would be catastrophic to act on, so a regression that
	// actually EXECUTED the verb cannot pass by accident.
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())

	cases := []struct {
		name string
		run  func() int
	}{
		{"spawn", func() int { return runSubprocessSpawnCommand([]string{"--help"}) }},
		{"stop", func() int { return runSubprocessStopCommand([]string{"--help"}) }},
		{"ping", func() int { return runSubprocessPingCommand([]string{"--help"}) }},
		{"stop -h", func() int { return runSubprocessStopCommand([]string{"-h"}) }},
	}
	for _, tc := range cases {
		out, code := captureStdout(t, tc.run)
		if code != 0 {
			t.Errorf("subprocess %s --help exited %d, want 0 (asking for help is not a usage error)", tc.name, code)
		}
		if !strings.Contains(out, "Usage: parlay subprocess-spawn") {
			t.Errorf("subprocess %s --help printed %q, want the shared subprocess usage", tc.name, out)
		}
		// The usage names all three verbs, so a reader who typed stop/ping can
		// see the family; assert it explicitly so a future trim cannot drop it.
		for _, verb := range []string{"subprocess-stop", "subprocess-ping"} {
			if !strings.Contains(out, verb) {
				t.Errorf("subprocess %s --help output never mentions %s", tc.name, verb)
			}
		}
	}
}

// TestSubprocessStopAndPingRefuseAFlagAsAnAgentID covers the other half of the
// same defect: without the guard, any unrecognized leading flag was taken as an
// agent id. `--help` was the visible symptom; a typo'd `--stae-dir` is the one
// that would have aimed a stop at a session named `--stae-dir`.
func TestSubprocessStopAndPingRefuseAFlagAsAnAgentID(t *testing.T) {
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())

	for _, args := range [][]string{
		{"--stae-dir", "/tmp/nope"},
		{"-x"},
		{"--agent", "demo"},
	} {
		if _, code := captureStdout(t, func() int { return runSubprocessStopCommand(args) }); code != 2 {
			t.Errorf("subprocess-stop %v exited %d, want 2", args, code)
		}
		if _, code := captureStdout(t, func() int { return runSubprocessPingCommand(args) }); code != 2 {
			t.Errorf("subprocess-ping %v exited %d, want 2", args, code)
		}
	}
}

// TestSubprocessUsageNamesNoDeletedScript pins the second fix in this file: the
// user-facing usage text told readers to "pass this explicitly from
// bin/parlay-spawn", a script deleted with the Go spawner fold-in (task-42qot).
// The on-disk "gascity" directory segment stays (it is what pre-rename sessions
// are found under), so only the dead script reference is asserted away.
func TestSubprocessUsageNamesNoDeletedScript(t *testing.T) {
	for _, dead := range []string{"bin/parlay-spawn", "AGENT_DIR"} {
		if strings.Contains(subprocessSpawnUsage, dead) {
			t.Errorf("subprocessSpawnUsage still names %q, which no longer exists; a reader "+
				"cannot follow an instruction that points at a deleted file", dead)
		}
	}
	if !strings.Contains(subprocessSpawnUsage, "gascity") {
		t.Error("subprocessSpawnUsage lost the on-disk \"gascity\" segment name; sessions " +
			"started before the rename are found under it")
	}
}
