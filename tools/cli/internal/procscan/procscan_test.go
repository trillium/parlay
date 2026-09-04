package procscan

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// procscanTestChildEnv, when "1" in this binary's own environment, makes
// TestMain skip the test suite entirely and idle forever instead — the
// re-exec trick spawnMarked uses to get a trivial, real, long-running
// fixture process that is a plain Go binary rather than a platform/SIP
// binary like /bin/sleep. On modern macOS a platform binary's environment
// is invisible to `ps` even to root (verified: /opt/homebrew-installed
// binaries show it, /bin/sleep does not, regardless of privilege), which
// would make this package's own darwin scanner untestable against the
// obvious fixture choice for reasons that have nothing to do with
// procscan's own correctness. Re-execing the test binary itself sidesteps
// that: it is still a trivial, inert, fake process (never a claude/agent
// harness), just not a sealed-system-volume one.
const procscanTestChildEnv = "PROCSCAN_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(procscanTestChildEnv) == "1" {
		time.Sleep(24 * time.Hour) // parked until the parent test kills us
		return
	}
	os.Exit(m.Run())
}

// spawnMarked starts a real, trivial long-running process carrying
// key=value in its environment, the same shape a gc subprocess-provider
// session child carries GC_SESSION_ID in. It is killed unconditionally at
// test cleanup so a failing assertion never leaks a live process.
func spawnMarked(t *testing.T, key, value string) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), procscanTestChildEnv+"=1", key+"="+value)
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn fixture: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return pid
}

func TestByEnvFindsMarkedProcess(t *testing.T) {
	key, value := "PROCSCAN_TEST_MARKER", "byenv-find"
	pid := spawnMarked(t, key, value)

	deadline := time.Now().Add(5 * time.Second)
	for {
		pids, err := ByEnv(key, value)
		if err != nil {
			t.Fatalf("ByEnv: %v", err)
		}
		if contains(pids, pid) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never appeared in ByEnv(%s=%s) scan", pid, key, value)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestByEnvExcludesUnmarkedProcess(t *testing.T) {
	key, value := "PROCSCAN_TEST_MARKER", "byenv-exclude"
	otherPID := spawnMarked(t, key, "a-different-value")

	pids, err := ByEnv(key, value)
	if err != nil {
		t.Fatalf("ByEnv: %v", err)
	}
	if contains(pids, otherPID) {
		t.Fatalf("ByEnv(%s=%s) matched pid %d, which carries a different value — false positive", key, value, otherPID)
	}
}

// TestReapKillsAndConfirms is the orphan regression proof at the procscan
// layer: a real process is left running (simulating gc session close's
// #203 failure to actually stop the subprocess) and Reap must terminate it
// and report it as reaped, not merely attempt a signal and hope.
func TestReapKillsAndConfirms(t *testing.T) {
	key, value := "PROCSCAN_TEST_MARKER", "reap-confirm"
	pid := spawnMarked(t, key, value)

	// Wait for the marked process to be visible before reaping it, same as
	// the find test — a scan racing the fork/exec is a flake, not a defect.
	deadline := time.Now().Add(5 * time.Second)
	for {
		pids, err := ByEnv(key, value)
		if err != nil {
			t.Fatalf("ByEnv: %v", err)
		}
		if contains(pids, pid) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never appeared before reap", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}

	reaped, survived, err := Reap(key, value, 2*time.Second, 2*time.Second)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(survived) != 0 {
		t.Fatalf("Reap left survivors: %v", survived)
	}
	if !contains(reaped, pid) {
		t.Fatalf("Reap did not report pid %d as reaped (reaped=%v)", pid, reaped)
	}

	pids, err := ByEnv(key, value)
	if err != nil {
		t.Fatalf("post-reap ByEnv: %v", err)
	}
	if contains(pids, pid) {
		t.Fatalf("pid %d still present in ByEnv scan after Reap reported it reaped", pid)
	}
}

// TestReapOnNoMatchIsNoop proves Reap never signals anything when nothing
// matches — the fail-closed posture must not become "kill whatever is
// nearby" when the target is simply already gone.
func TestReapOnNoMatchIsNoop(t *testing.T) {
	reaped, survived, err := Reap("PROCSCAN_TEST_MARKER", "no-such-value-in-any-process", time.Second, time.Second)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(reaped) != 0 || len(survived) != 0 {
		t.Fatalf("Reap on no match returned reaped=%v survived=%v, want both empty", reaped, survived)
	}
}

func contains(pids []int, pid int) bool {
	for _, p := range pids {
		if p == pid {
			return true
		}
	}
	return false
}
