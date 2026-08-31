package commands

// Gated end-to-end proof for spawn-lift units 5 and 7: the `parlay gc-spawn`
// core launches a real Gas City session from a synthesised template, against
// the SUBPROCESS session provider (city/city.toml [session] — the
// task-4cfpv.9 requirement that spawn-path tests run without tmux), and a
// stamped identity then resolves through the bead-backed directory — before
// and after the session retires, each time from a fresh gc process.
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
// never a name-pattern pkill. It then waits (bounded) for the kills to land:
// a dying gc watchdog with GC_HOME inside the temp tree can otherwise write a
// runtime file mid-RemoveAll and fail t.TempDir's cleanup with "directory not
// empty" (observed on the first run of this test).
func reapByMarker(t *testing.T, marker string) {
	t.Helper()
	markerPids := func() []string {
		out, err := exec.Command("ps", "ax", "-o", "pid=,command=").Output()
		if err != nil {
			t.Logf("ps for cleanup sweep: %v", err)
			return nil
		}
		var pids []string
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.Contains(line, marker) {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			pids = append(pids, fields[0])
		}
		return pids
	}
	for _, pid := range markerPids() {
		if kill := exec.Command("kill", pid); kill.Run() == nil {
			t.Logf("reaped leftover process %s", pid)
		}
	}
	for i := 0; i < 100; i++ {
		if len(markerPids()) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("processes matching %q survived the reap wait", marker)
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
	// subprocess provider", not "an agent runs"). The probe dumps its own
	// environment to a file and then sleeps; sleep self-terminates even if
	// every cleanup layer fails. The env dump is the emitted-output proof
	// (never a timing assertion) that the provider really executed the
	// FULL command line: gc's agent-level start_command is an escape hatch
	// that ignores a separate args field, so a synthesis bug that drops the
	// args leaves a bare /bin/sh that exits instantly without ever writing
	// the file (the exact defect the first revision of gctemplate had).
	envDump := filepath.Join(canon, "spawn-probe-env")
	res, err := gcSpawnRun(gctemplate.LaunchSpec{
		ID:           "spawn-probe",
		Name:         "Spawn Probe",
		Prompt:       "integration probe; inert env-dump + sleep, never a real agent",
		Server:       "http://localhost:14242",
		StartCommand: "/bin/sh",
		Args:         []string{"-c", "env > " + envDump + "; exec sleep 300"},
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

	// Subprocess-provider proof, from the probe's own emitted output. Two
	// earlier assertion strategies are structurally impossible here and must
	// not come back: (a) `tmux -L <city-basename> list-sessions` — it
	// false-positived on an ambient tmux socket coincidentally named "city",
	// and gc's tmux provider names its socket from config/city NAME, not the
	// city dir; (b) pinging the provider's per-session control socket — the
	// listener lives in the gc process, and a one-shot `gc session new` CLI
	// exits after create, unlinking the socket, so no socket artifact ever
	// survives for a later assertion. The probe's env dump is the artifact
	// that does survive, and it proves the two things that matter: the
	// template [env] reached the child (delivery through the provider), and
	// TMUX is absent from it (the child is not inside any tmux pane).
	deadline := time.Now().Add(60 * time.Second)
	var dump []byte
	for {
		var rerr error
		dump, rerr = os.ReadFile(envDump)
		if rerr == nil && len(dump) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe env dump %s never appeared — the provider did not execute the probe's full command line (read err: %v)", envDump, rerr)
		}
		time.Sleep(200 * time.Millisecond)
	}
	env := string(dump)
	if !strings.Contains(env, "PARLAY_SERVER=http://localhost:14242\n") {
		t.Errorf("probe env is missing the template-delivered PARLAY_SERVER:\n%s", env)
	}
	if strings.Contains(env, "\nTMUX=") || strings.HasPrefix(env, "TMUX=") {
		t.Errorf("probe env contains TMUX — the session ran inside a tmux pane, not on the subprocess provider:\n%s", env)
	}

	// Unit 7: bead-backed identity resolution against the REAL city. Stamp
	// the session pointer the way parlay-spawn's register does (identity.md
	// projection, worktree alongside), then resolve. Every gcResolveRun
	// spawns a fresh gc process reading the bead store from cold — there is
	// no long-lived supervisor in this sandbox at all, so each resolution IS
	// the restart case: nothing but the bead store connects the stamp to the
	// answer.
	agentHome := filepath.Join(canon, "agents")
	if err := os.MkdirAll(filepath.Join(agentHome, "spawn-probe"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARLAY_AGENT_HOME", agentHome)
	idFile := filepath.Join(agentHome, "spawn-probe", "identity.md")
	stamp := "---\nid: spawn-probe\nworktree: /tmp/wt/spawn-probe\ngc_session: " + res.SessionID + "\n---\n"
	if err := os.WriteFile(idFile, []byte(stamp), 0o644); err != nil {
		t.Fatal(err)
	}
	r1, err := gcResolveRun("spawn-probe")
	if err != nil {
		t.Fatalf("gcResolveRun (live): %v", err)
	}
	if !r1.OK || r1.SessionID != res.SessionID || r1.Via != "bead-id" {
		t.Errorf("live resolve = %+v, want session %s via bead-id", r1, res.SessionID)
	}

	// Retire the session, then resolve again from another fresh gc process:
	// an id stamped before the session retired must still resolve (the
	// AddressDirectory's closed-included exact-bead-id rule, the property
	// identity.md alone cannot provide).
	if _, cerr, err := runGC(60*time.Second, "session", "close", res.SessionID, "--json"); err != nil {
		t.Fatalf("session close %s: %v\nstderr:\n%s", res.SessionID, err, cerr)
	}
	r2, err := gcResolveRun("spawn-probe")
	if err != nil {
		t.Fatalf("gcResolveRun (retired): %v", err)
	}
	if !r2.OK || r2.SessionID != res.SessionID || r2.Via != "bead-id" {
		t.Errorf("retired resolve = %+v, want session %s via bead-id", r2, res.SessionID)
	}
	if !r2.Closed {
		t.Errorf("retired resolve should report closed=true, got %+v", r2)
	}
}
