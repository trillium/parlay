package commands

// Gated end-to-end proof for spawn-lift unit 5: the `parlay gc-spawn` core
// launches a real Gas City session from a synthesised template, against the
// SUBPROCESS session provider (city/city.toml [session] — the task-4cfpv.9
// requirement that spawn-path tests run without tmux).
//
// Gate and store-bootstrap recipe are the same as unit 4's
// internal/gctemplate/integration_test.go (its header comment is the full
// rationale — managed dolt server first via `gc beads health`, then an
// UPSTREAM bd joins that server; the captain's bd fork speaks a different
// schema and must never be used here):
//
//	PARLAY_GC_INTEGRATION=1  \
//	PARLAY_GC=<pinned gc>    \  # tools/gc-build/build-gc.sh
//	PARLAY_BD=<upstream bd>  \
//	go test ./internal/commands/ -run TestGCSpawnRunStartsSubprocessSession
//
// `dolt` must be on PATH. tmux is deliberately NOT required: if this test
// only passes with a tmux server available, the subprocess-provider claim is
// broken.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/cityscaffold"
	"github.com/trillium/parlay/tools/cli/internal/gctemplate"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

const gcSpawnTypesCustom = "molecule,convoy,message,event,gate,merge-request,agent,role,rig,session,spec,convergence,step"

// reapByMarker TERM-kills every process whose command line contains marker (a
// unique per-test temp path) — same safely-scoped sweep as unit 4's test,
// never a name-pattern pkill.
func reapByMarker(t *testing.T, marker string) {
	t.Helper()
	out, err := exec.Command("ps", "ax", "-o", "pid=,command=").Output()
	if err != nil {
		t.Logf("ps for cleanup sweep: %v", err)
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if kill := exec.Command("kill", fields[0]); kill.Run() == nil {
			t.Logf("reaped leftover process %s", strings.TrimSpace(line))
		}
	}
}

func TestGCSpawnRunStartsSubprocessSession(t *testing.T) {
	if os.Getenv("PARLAY_GC_INTEGRATION") != "1" {
		t.Skip("set PARLAY_GC_INTEGRATION=1 with PARLAY_GC (pinned gc: tools/gc-build/build-gc.sh) and PARLAY_BD (upstream bd; see internal/gctemplate/integration_test.go) to run")
	}
	gc := os.Getenv("PARLAY_GC")
	bd := os.Getenv("PARLAY_BD")
	if gc == "" || bd == "" {
		t.Skip("PARLAY_GC and PARLAY_BD must both name binaries")
	}

	// Canonicalise the temp state home (macOS /tmp symlinks /private/tmp):
	// two spellings of one store root mean two dolt proxies fighting over one
	// lock — see unit 4's test header.
	state := testsupport.TempStateHome(t)
	canon, err := filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", state, err)
	}
	t.Setenv("PARLAY_STATE_HOME", canon)
	// gc's bd shell-outs must hit the upstream binary first; /usr/sbin for
	// lsof. Ambient store context is dropped by gcSpawnEnv for the gc child,
	// but bd runs directly from this test, so clear it here too.
	t.Setenv("PATH", filepath.Dir(bd)+":/usr/sbin:"+os.Getenv("PATH"))
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BD_NAME", "")
	t.Setenv("PARLAY_GC", gc)

	// Materialize first (idempotent — gcSpawnRun re-runs it) so the store can
	// be bootstrapped in the city before the launch.
	scaffold, err := cityscaffold.Materialize()
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { reapByMarker(t, strings.TrimPrefix(scaffold.Dir, "/private")) })

	home, err := gcSpawnHome()
	if err != nil {
		t.Fatal(err)
	}
	runGC := func(timeout time.Duration, args ...string) (string, string, error) {
		cmd := exec.Command(gc, append([]string{"--city", scaffold.Dir}, args...)...)
		cmd.Dir = home
		cmd.Env = gcSpawnEnv(home)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		done := time.AfterFunc(timeout, func() { _ = cmd.Process.Kill() })
		out, err := cmd.Output()
		done.Stop()
		return string(out), stderr.String(), err
	}

	// Store bootstrap, exactly the unit-4 recipe: `gc beads health` first for
	// its managed-dolt side effect (its exit status is noise with a CGO-free
	// bd), then upstream bd joins the recorded server.
	if hout, herr, err := runGC(300*time.Second, "beads", "health"); err != nil {
		t.Logf("gc beads health bootstrap exited non-zero (expected with a CGO-free bd): %v\n%s\nstderr:\n%s", err, hout, herr)
	}
	portBytes, err := os.ReadFile(filepath.Join(scaffold.Dir, ".beads", "dolt-server.port"))
	if err != nil {
		t.Fatalf("gc beads health did not record the managed dolt port: %v", err)
	}
	port := strings.TrimSpace(string(portBytes))
	runBD := func(args ...string) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = scaffold.Dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	runBD("init", "--prefix", "pa", "--server", "--server-port", port, "--non-interactive")
	runBD("config", "set", "types.custom", gcSpawnTypesCustom)
	runBD("list", "--json")

	// The launch itself, through the verb's core — an inert command instead
	// of a real claude (the bar is "the spawn path starts a session on the
	// subprocess provider", not "an agent runs"). sleep self-terminates even
	// if every cleanup layer fails.
	res, err := gcSpawnRun(gctemplate.LaunchSpec{
		ID:           "spawn-probe",
		Name:         "Spawn Probe",
		Prompt:       "integration probe; inert sleep, never a real agent",
		Server:       "http://localhost:14242",
		StartCommand: "/bin/sh",
		Args:         []string{"-c", "sleep 300"},
	})
	if res.SessionID != "" {
		t.Cleanup(func() {
			if _, _, err := runGC(60*time.Second, "session", "close", res.SessionID, "--json"); err != nil {
				t.Logf("session close %s: %v", res.SessionID, err)
			}
		})
	}
	if err != nil {
		t.Fatalf("gcSpawnRun: %v", err)
	}
	if !res.OK || res.SessionID == "" || res.SessionName == "" {
		t.Fatalf("result = %+v", res)
	}
	if res.Template != "parlay.spawn-probe" {
		t.Errorf("template = %q, want parlay.spawn-probe", res.Template)
	}

	// Subprocess-provider proof: the session runtime exists as a plain
	// process (the inert sleep) and NO tmux session was created for this
	// city. The tmux socket would be named after the city dir.
	if out, err := exec.Command("tmux", "-L", filepath.Base(scaffold.Dir), "list-sessions").CombinedOutput(); err == nil {
		t.Errorf("a tmux server answered for this city — session did not run on the subprocess provider:\n%s", out)
	}
}
