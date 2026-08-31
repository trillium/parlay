package gctemplate

// Gated end-to-end proof for spawn-lift unit 4: a synthesised template placed
// in the materialised city scaffold is startable — `gc session new
// parlay.<id> --json --no-attach` exits 0 and reports ok:true.
//
// The gate needs real binaries, so it is off by default and in CI:
//
//	PARLAY_GC_INTEGRATION=1  \
//	PARLAY_GC=<pinned gc>    \  # tools/gc-build/build-gc.sh
//	PARLAY_BD=<upstream bd>  \  # see skip message below
//	go test ./internal/gctemplate/ -run TestSynthesisedTemplateStartsSession
//
// PARLAY_BD must be an UPSTREAM beads build matching the gc pin's vendored
// library version — the captain's bd fork speaks a different store schema and
// fails with column errors. Build one with:
//
//	CGO_ENABLED=0 go install github.com/steveyegge/beads/cmd/bd@<version in gascity go.mod>
//
// Store bootstrap order is load-bearing. gc's beads component (gc-beads-bd)
// owns the city's dolt sql-server: on first store contact it starts a
// managed server on an OS-assigned port serving `.beads/dolt` and records
// the port in `.beads/dolt-server.port`. bd must join THAT server
// (`bd init --server --server-port <recorded>`), never spawn its own: a
// store made with `bd init --proxied-server` first deterministically
// deadlocks later, because gc's managed server adopts the recorded port
// after bd's proxied dolt idles out, and every subsequent bd proxy respawn
// then times out against the foreign listener. So the test runs
// `gc beads health` FIRST (tolerating its non-zero exit — a CGO-free bd
// cannot answer the embedded-mode ping it ends with) purely for its
// bootstrap side effect, then points `bd init --server` at the recorded
// port. gc's session beads also need its custom types registered in the
// store's own config (`bd config set types.custom ...` — the
// `.beads/config.yaml` copy gc writes is not what create-validation reads).
//
// `dolt` and `tmux` must be on PATH. Everything lives under t.TempDir():
// the store, the scratch GC_HOME (supervisor redirected per contract §9.1 —
// never the machine-wide one), and the city. gc's managed-dolt watchdog
// reaps itself when the scope dir vanishes — but slowly (it polls on a
// coarse interval), so the test also reaps any process whose command line
// names its unique city dir, closes its session, and kills its exact tmux
// session by name: the tmux server (socket "city", named after the city
// dir) is per-user shared state.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/cityscaffold"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// gcTypesCustom mirrors the custom bead types gc's own bootstrap configures;
// without them registered via `bd config set` the upstream bd CLI refuses
// `bd create --type session`.
const gcTypesCustom = "molecule,convoy,message,event,gate,merge-request,agent,role,rig,session,spec,convergence,step"

// killProcessesReferencing TERM-kills every process whose full command line
// contains marker — a unique per-test temp path, so this cannot reach
// anything but processes this test spawned.
func killProcessesReferencing(t *testing.T, marker string) {
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

func integrationEnv(t *testing.T, bdDir, gcHome string) []string {
	t.Helper()
	env := []string{}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "GC_HOME", "GC_CITY", "GC_CITY_PATH", "BEADS_DIR", "BD_NAME", "PATH":
			continue
		}
		env = append(env, kv)
	}
	// bd first so gc's shell-outs hit the upstream binary, /usr/sbin for lsof.
	env = append(env, "PATH="+bdDir+":/usr/sbin:"+os.Getenv("PATH"))
	if gcHome != "" {
		env = append(env, "GC_HOME="+gcHome)
	}
	return env
}

func TestSynthesisedTemplateStartsSession(t *testing.T) {
	if os.Getenv("PARLAY_GC_INTEGRATION") != "1" {
		t.Skip("set PARLAY_GC_INTEGRATION=1 with PARLAY_GC (pinned gc: tools/gc-build/build-gc.sh) and PARLAY_BD (upstream bd matching the pin's vendored beads version; CGO_ENABLED=0 go install github.com/steveyegge/beads/cmd/bd@<gascity go.mod version>) to run")
	}
	gc := os.Getenv("PARLAY_GC")
	bd := os.Getenv("PARLAY_BD")
	if gc == "" || bd == "" {
		t.Skip("PARLAY_GC and PARLAY_BD must both name binaries")
	}
	// Canonicalise the temp state home (macOS /tmp is a symlink to
	// /private/tmp): gc and bd each resolve the store root their own way,
	// and two spellings of one root mean two Dolt proxies fighting over one
	// database lock — the loser times out waiting for its proxy.
	state := testsupport.TempStateHome(t)
	canon, err := filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", state, err)
	}
	t.Setenv("PARLAY_STATE_HOME", canon)

	res, err := cityscaffold.Materialize()
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	// gc and bd both spawn managed dolt servers scoped to the city; their
	// self-reaping is too slow for a test run. The city dir is a unique temp
	// path, so exact-substring matching the full command line is a safely
	// scoped sweep (never a name-pattern pkill). Trim /private so the marker
	// matches whichever /tmp spelling a process was started with.
	t.Cleanup(func() { killProcessesReferencing(t, strings.TrimPrefix(res.Dir, "/private")) })
	spec := LaunchSpec{
		ID:     "inert-probe",
		Name:   "Inert Probe",
		Prompt: "integration probe; the session runs an inert sleep, never a real agent",
		// An inert command instead of a real claude: the bar is "gc starts
		// the session from the synthesised template", not "an agent runs".
		StartCommand: "/bin/sh",
		Args:         []string{"-c", "sleep 300"},
	}
	if _, err := WriteInto(filepath.Join(res.Dir, "packs", "parlay"), spec); err != nil {
		t.Fatalf("WriteInto: %v", err)
	}

	bdDir := filepath.Dir(bd)
	env := integrationEnv(t, bdDir, "")

	// Contract §9.1: every gc invocation gets a scratch GC_HOME with the
	// supervisor port redirected off the machine-wide singleton.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "supervisor.toml"), []byte("[supervisor]\nport = 18372\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gcEnv := integrationEnv(t, bdDir, home)
	runGC := func(timeout time.Duration, args ...string) (string, string, error) {
		cmd := exec.Command(gc, append([]string{"--city", res.Dir}, args...)...)
		cmd.Dir = home
		cmd.Env = gcEnv
		var stderr strings.Builder
		cmd.Stderr = &stderr
		done := time.AfterFunc(timeout, func() { _ = cmd.Process.Kill() })
		out, err := cmd.Output()
		done.Stop()
		return string(out), stderr.String(), err
	}

	// gc owns the store's dolt sql-server (see the header comment): run
	// `gc beads health` first purely for its bootstrap side effect — it
	// starts the managed server on an OS-assigned port and records it in
	// `.beads/dolt-server.port`. Its exit status is noise (a CGO-free bd
	// cannot answer the embedded-mode ping the probe ends with), so only
	// the recorded port is checked.
	if hout, herr, err := runGC(300*time.Second, "beads", "health"); err != nil {
		t.Logf("gc beads health bootstrap exited non-zero (expected with a CGO-free bd): %v\n%s\nstderr:\n%s", err, hout, herr)
	}
	portBytes, err := os.ReadFile(filepath.Join(res.Dir, ".beads", "dolt-server.port"))
	if err != nil {
		t.Fatalf("gc beads health did not record the managed dolt port: %v", err)
	}
	port := strings.TrimSpace(string(portBytes))

	runBD := func(args ...string) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = res.Dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	// Join gc's managed server — never spawn a bd-owned one. "pa" matches
	// the issue_prefix gc's bootstrap wrote into the store config.
	runBD("init", "--prefix", "pa", "--server", "--server-port", port, "--non-interactive")
	runBD("config", "set", "types.custom", gcTypesCustom)
	// Settle first real store contact outside `session new`.
	runBD("list", "--json")

	out, errOut, err := runGC(300*time.Second, "session", "new", "parlay."+spec.ID, "--json", "--no-attach")
	var created struct {
		OK          bool   `json:"ok"`
		SessionID   string `json:"session_id"`
		SessionName string `json:"session_name"`
		Template    string `json:"template"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &created); jsonErr != nil {
		t.Fatalf("session new output is not JSON (err=%v): %v\n%s\nstderr:\n%s", err, jsonErr, out, errOut)
	}
	if created.SessionID != "" {
		t.Cleanup(func() {
			if _, _, err := runGC(60*time.Second, "session", "close", created.SessionID, "--json"); err != nil {
				t.Logf("session close %s: %v", created.SessionID, err)
			}
			if created.SessionName != "" {
				// tmux socket is named after the city dir; kill only our
				// exact session in case close left the runtime behind.
				kill := exec.Command("tmux", "-L", filepath.Base(res.Dir), "kill-session", "-t", "="+created.SessionName)
				kill.Env = gcEnv
				_ = kill.Run()
			}
		})
	}
	if err != nil || !created.OK {
		t.Fatalf("gc session new failed (err=%v, ok=%v): %s\nstderr:\n%s", err, created.OK, out, errOut)
	}
	if created.Template != "parlay."+spec.ID {
		t.Errorf("session template = %q, want parlay.%s", created.Template, spec.ID)
	}
	if created.SessionID == "" || created.SessionName == "" {
		t.Errorf("session new returned empty identifiers: %s", out)
	}
	fmt.Printf("session %s (%s) started from synthesised template\n", created.SessionID, created.SessionName)
}
