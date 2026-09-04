package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/procscan"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// spawnMarkedProcess starts a real, trivial long-running process carrying
// key=value in its environment — the same shape a gc subprocess-provider
// session child carries GC_SESSION_ID in. It re-execs the test binary itself
// (see testhelpers_test.go's gcTeardownTestChildEnv) rather than /bin/sleep,
// because a sealed platform binary's environment is invisible to `ps eww` on
// modern macOS. It is killed unconditionally at test cleanup so a failing
// assertion never leaks a live process.
func spawnMarkedProcess(t *testing.T, key, value string) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), gcTeardownTestChildEnv+"=1", key+"="+value)
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

// waitForMarked blocks until procscan.ByEnv reports pid, or fails the test —
// a scan racing the fixture's own fork/exec is a flake, not a defect.
func waitForMarked(t *testing.T, key, value string, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		pids, err := procscan.ByEnv(key, value)
		if err != nil {
			t.Fatalf("ByEnv: %v", err)
		}
		if containsPID(pids, pid) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never appeared in ByEnv(%s=%s) scan", pid, key, value)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func containsPID(pids []int, pid int) bool {
	for _, p := range pids {
		if p == pid {
			return true
		}
	}
	return false
}

// writeTeardownFakeGC writes a fake gc answering every verb gc-teardown's
// full flow can issue: `beads show` (gc-resolve rule 1), `session list`
// (gc-resolve rule 2), and `session close` (this verb's own call) — each
// with independent canned stdout/exit and its own argv record, so a test can
// tell which legs actually ran.
func writeTeardownFakeGC(t *testing.T, beadsOut string, beadsExit int, listOut string, closeOut string, closeExit int) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	for name, body := range map[string]string{
		"stdout-beads": beadsOut,
		"stdout-list":  listOut,
		"stdout-close": closeOut,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
rec="` + dir + `"
case "$*" in
*"beads show"*)
  printf '%s\n' "$@" > "$rec/argv-beads"
  cat "$rec/stdout-beads"
  exit ` + strconv.Itoa(beadsExit) + ` ;;
*"session close"*)
  printf '%s\n' "$@" > "$rec/argv-close"
  cat "$rec/stdout-close"
  exit ` + strconv.Itoa(closeExit) + ` ;;
*)
  printf '%s\n' "$@" > "$rec/argv-list"
  cat "$rec/stdout-list"
  exit 0 ;;
esac
`
	bin = filepath.Join(dir, "gc")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

// TestGCTeardownVerifyAndReapReapsOrphan is the #203 regression proof at the
// verb layer, independent of any real gc binary: a real process is left
// running with the session's GC_SESSION_ID (simulating `gc session close`
// silently failing to kill its child) and gcTeardownVerifyAndReap must
// terminate it and report it reaped.
func TestGCTeardownVerifyAndReapReapsOrphan(t *testing.T) {
	sessionID := "gct-reap-" + strconv.Itoa(os.Getpid())
	pid := spawnMarkedProcess(t, gcTeardownSessionEnvKey, sessionID)
	waitForMarked(t, gcTeardownSessionEnvKey, sessionID, pid)

	res := gcTeardownResult{AgentID: "agent-orphan", SessionID: sessionID, Closed: true}
	out, err := gcTeardownVerifyAndReap(res)
	if err != nil {
		t.Fatalf("gcTeardownVerifyAndReap: %v", err)
	}
	if !out.OK {
		t.Fatalf("want OK after the orphan is reaped, got %+v", out)
	}
	if len(out.SurvivedPIDs) != 0 {
		t.Fatalf("want no survivors, got %+v", out)
	}
	if !containsPID(out.ReapedPIDs, pid) {
		t.Fatalf("want pid %d reported reaped, got %+v", pid, out)
	}

	pids, err := procscan.ByEnv(gcTeardownSessionEnvKey, sessionID)
	if err != nil {
		t.Fatalf("post-reap ByEnv: %v", err)
	}
	if containsPID(pids, pid) {
		t.Fatalf("pid %d still present after gcTeardownVerifyAndReap reported it reaped", pid)
	}
}

// TestGCTeardownVerifyAndReapNoOrphanIsOK proves the common case — gc's own
// close actually worked — reports OK with no orphan bookkeeping at all.
func TestGCTeardownVerifyAndReapNoOrphanIsOK(t *testing.T) {
	res := gcTeardownResult{AgentID: "agent-clean", SessionID: "gct-no-such-session-id"}
	out, err := gcTeardownVerifyAndReap(res)
	if err != nil {
		t.Fatalf("gcTeardownVerifyAndReap: %v", err)
	}
	if !out.OK || len(out.OrphanPIDs) != 0 || len(out.ReapedPIDs) != 0 {
		t.Errorf("want a clean OK with no orphan bookkeeping, got %+v", out)
	}
}

// TestGCTeardownVerifyAndReapRefusesOnScanError is the fail-closed doctrine
// proof: an unreadable process table must never be treated as "no orphan
// exists" — it must refuse and say why, touching nothing.
func TestGCTeardownVerifyAndReapRefusesOnScanError(t *testing.T) {
	orig := procscan.ByEnv
	defer func() { procscan.ByEnv = orig }()
	procscan.ByEnv = func(key, value string) ([]int, error) {
		return nil, fmt.Errorf("simulated: process table unreadable")
	}

	res := gcTeardownResult{AgentID: "agent-indeterminate", SessionID: "gct-whatever"}
	out, err := gcTeardownVerifyAndReap(res)
	if err != nil {
		t.Fatalf("gcTeardownVerifyAndReap: %v", err)
	}
	if out.OK {
		t.Fatalf("must not report OK when the scan itself failed, got %+v", out)
	}
	if !out.Refused {
		t.Fatalf("must set Refused on an indeterminate scan rather than guessing, got %+v", out)
	}
}

// TestGCTeardownRunReapsOrphanDespiteCloseClaimingSuccess is the full
// orchestration proof: gc's own `session close` reports {"ok":true} — the
// literal #203 lie — while the underlying process is still alive.
// gcTeardownRun must not trust that report; it must detect and reap the
// survivor anyway.
func TestGCTeardownRunReapsOrphanDespiteCloseClaimingSuccess(t *testing.T) {
	testsupport.TempStateHome(t)
	sessionID := "gct-orch-" + strconv.Itoa(os.Getpid())
	stampIdentityGCSession(t, "agent-orphan", sessionID)

	pid := spawnMarkedProcess(t, gcTeardownSessionEnvKey, sessionID)
	waitForMarked(t, gcTeardownSessionEnvKey, sessionID, pid)

	beadsOut := beadShowJSON(sessionID, "open", "parlay.agent-orphan", "active")
	closeOut := `{"schema_version":"1","ok":true}`
	bin, rec := writeTeardownFakeGC(t, beadsOut, 0, sessionListJSON(), closeOut, 0)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcTeardownRun("agent-orphan")
	if err != nil {
		t.Fatalf("gcTeardownRun: %v", err)
	}
	if !res.Closed {
		t.Fatalf("gc session close should have reported success (the #203 lie), got %+v", res)
	}
	if !res.OK {
		t.Fatalf("want OK once gc-teardown reaps the orphan gc's own close missed, got %+v", res)
	}
	if !containsPID(res.ReapedPIDs, pid) {
		t.Fatalf("want pid %d reported reaped, got %+v", pid, res)
	}
	if len(res.SurvivedPIDs) != 0 {
		t.Fatalf("want no survivors, got %+v", res)
	}

	pids, err := procscan.ByEnv(gcTeardownSessionEnvKey, sessionID)
	if err != nil {
		t.Fatalf("post-teardown ByEnv: %v", err)
	}
	if containsPID(pids, pid) {
		t.Fatalf("pid %d still alive after gc-teardown reported OK — the #203 defect is unfixed", pid)
	}

	if _, statErr := os.Stat(filepath.Join(rec, "argv-close")); statErr != nil {
		t.Errorf("gc session close never ran: %v", statErr)
	}
}

// TestGCTeardownRunOKWhenCloseFailsButNoOrphanExists proves the converse
// decoupling: gc's own close report is not trusted in the other direction
// either — a failed/unparsable close claim must not sour an otherwise-clean
// teardown when no matching process actually exists.
func TestGCTeardownRunOKWhenCloseFailsButNoOrphanExists(t *testing.T) {
	testsupport.TempStateHome(t)
	sessionID := "gct-noclose-" + strconv.Itoa(os.Getpid())
	stampIdentityGCSession(t, "agent-clean", sessionID)

	beadsOut := beadShowJSON(sessionID, "open", "parlay.agent-clean", "active")
	bin, _ := writeTeardownFakeGC(t, beadsOut, 0, sessionListJSON(), "not json", 1)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcTeardownRun("agent-clean")
	if err != nil {
		t.Fatalf("gcTeardownRun: %v", err)
	}
	if res.Closed {
		t.Fatalf("want Closed=false when gc session close's own output is unparsable, got %+v", res)
	}
	if res.CloseError == "" {
		t.Fatalf("want close_error populated when close fails, got %+v", res)
	}
	if !res.OK {
		t.Fatalf("verify-and-reap is close-independent: no matching process exists, so OK must be true regardless of gc's own report, got %+v", res)
	}
}

// TestGCTeardownRunReasonNamesResolveFailure proves an unresolvable agent
// never reaches the close/reap steps at all.
func TestGCTeardownRunReasonNamesResolveFailure(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir()) // no identity.md stamped
	bin, rec := writeTeardownFakeGC(t, "", 1, sessionListJSON(), `{"ok":true}`, 0)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcTeardownRun("agent-unknown")
	if err != nil {
		t.Fatalf("gcTeardownRun: %v", err)
	}
	if res.OK {
		t.Fatalf("an unresolvable agent must not report OK, got %+v", res)
	}
	if res.Closed {
		t.Fatalf("close must never be attempted when resolve fails, got %+v", res)
	}
	if !strings.Contains(res.Reason, "cannot tear down") {
		t.Errorf("reason = %q", res.Reason)
	}
	if _, statErr := os.Stat(filepath.Join(rec, "argv-close")); statErr == nil {
		t.Errorf("gc session close ran despite an unresolved agent")
	}
}
