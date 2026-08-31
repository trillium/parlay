package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/gctemplate"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// writeSpawnFakeGC drops an executable gc stand-in that records its argv and
// environment to files in its own directory and prints stdout for
// `session new`. It proves gcSpawnRun's isolation env without any real gc.
func writeSpawnFakeGC(t *testing.T, stdout string, exitCode int) (bin, recordDir string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "gc")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"" + dir + "/argv\"\n" +
		"env > \"" + dir + "/env\"\n" +
		"pwd > \"" + dir + "/cwd\"\n" +
		"printf '%s\\n' '" + stdout + "'\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

const fakeSessionNewOK = `{"schema_version":"1","ok":true,"session_id":"pa-123","session_name":"parlay-probe-x","template":"parlay.probe-x"}`

func TestGCSpawnRunHappyPath(t *testing.T) {
	state := testsupport.TempStateHome(t)
	bin, rec := writeSpawnFakeGC(t, fakeSessionNewOK, 0)
	t.Setenv("PARLAY_GC", bin)
	// Ambient context that must NOT leak into the child.
	t.Setenv("GC_HOME", "/somewhere/else")
	t.Setenv("GC_CITY", "/other/city")
	t.Setenv("BEADS_DIR", "/other/beads")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")

	res, err := gcSpawnRun(gctemplate.LaunchSpec{
		ID:     "probe-x",
		Name:   "Probe X",
		Prompt: "do the thing",
		Server: "http://localhost:14242",
	})
	if err != nil {
		t.Fatalf("gcSpawnRun: %v", err)
	}
	if !res.OK || res.SessionID != "pa-123" || res.SessionName != "parlay-probe-x" || res.Template != "parlay.probe-x" {
		t.Errorf("result = %+v", res)
	}
	if res.AgentID != "probe-x" || res.GC != bin {
		t.Errorf("result identity fields = %+v", res)
	}

	// The scaffold materialised and the template landed inside it.
	if res.CityDir != filepath.Join(state, "gascity", "city") {
		t.Errorf("CityDir = %s", res.CityDir)
	}
	agentTOML, err := os.ReadFile(filepath.Join(res.CityDir, "packs", "parlay", "agents", "probe-x", "agent.toml"))
	if err != nil {
		t.Fatalf("synthesised agent.toml missing: %v", err)
	}
	if !strings.Contains(string(agentTOML), `PARLAY_SERVER = "http://localhost:14242"`) {
		t.Errorf("agent.toml lacks PARLAY_SERVER env:\n%s", agentTOML)
	}
	// The city-level session provider is the subprocess provider (unit 5's
	// test requirement: spawn-path tests run against the subprocess provider).
	cityTOML, err := os.ReadFile(filepath.Join(res.CityDir, "city.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cityTOML), `provider = "subprocess"`) {
		t.Errorf("city.toml does not select the subprocess session provider:\n%s", cityTOML)
	}

	// gc argv: --city <scaffold> session new parlay.<id> --json --no-attach.
	argv, err := os.ReadFile(filepath.Join(rec, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := strings.Join([]string{"--city", res.CityDir, "session", "new", "parlay.probe-x", "--json", "--no-attach"}, "\n") + "\n"
	if string(argv) != wantArgv {
		t.Errorf("gc argv:\n%s\nwant:\n%s", argv, wantArgv)
	}

	// Child env: parlay-owned GC_HOME, ambient + nesting markers scrubbed.
	envBytes, err := os.ReadFile(filepath.Join(rec, "env"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(envBytes)
	wantHome := filepath.Join(state, "gascity", "home")
	if !strings.Contains(env, "GC_HOME="+wantHome+"\n") {
		t.Errorf("child GC_HOME not the parlay-owned home:\n%s", env)
	}
	for _, banned := range []string{"GC_CITY=", "BEADS_DIR=", "CLAUDECODE=", "CLAUDE_CODE_ENTRYPOINT="} {
		if strings.Contains(env, banned) {
			t.Errorf("child env leaks %s", banned)
		}
	}

	// The GC_HOME was seeded with the supervisor port redirected off the
	// shared :8372 singleton (contract §9.1).
	sup, err := os.ReadFile(filepath.Join(wantHome, "supervisor.toml"))
	if err != nil {
		t.Fatalf("supervisor.toml not seeded: %v", err)
	}
	if !strings.Contains(string(sup), "port = 18372") {
		t.Errorf("supervisor.toml = %q", sup)
	}
}

func TestGCSpawnRunRefusesWithoutGC(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_GC", "")
	t.Setenv("PATH", t.TempDir()) // nothing named gc on PATH

	_, err := gcSpawnRun(gctemplate.LaunchSpec{ID: "probe-x"})
	if err == nil {
		t.Fatal("expected a refusal without gc")
	}
	if !strings.Contains(err.Error(), "gc not found") || !strings.Contains(err.Error(), "build-gc.sh") {
		t.Errorf("refusal must name the condition and the install fix, got: %v", err)
	}
}

func TestGCSpawnRunSurfacesNonJSONFailure(t *testing.T) {
	testsupport.TempStateHome(t)
	bin, _ := writeSpawnFakeGC(t, "panic: store not bootstrapped", 1)
	t.Setenv("PARLAY_GC", bin)

	_, err := gcSpawnRun(gctemplate.LaunchSpec{ID: "probe-x"})
	if err == nil {
		t.Fatal("expected an error for non-JSON gc output")
	}
	for _, want := range []string{"typed JSON", "store not bootstrapped", "integration_test.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q, got: %v", want, err)
		}
	}
}

func TestGCSpawnRunSurfacesTypedRefusal(t *testing.T) {
	testsupport.TempStateHome(t)
	refusal := `{"schema_version":"1","ok":false,"error":"template parlay.probe-x not found"}`
	bin, _ := writeSpawnFakeGC(t, refusal, 1)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcSpawnRun(gctemplate.LaunchSpec{ID: "probe-x"})
	if err == nil {
		t.Fatal("expected an error for ok:false")
	}
	if res.OK {
		t.Error("result must not claim ok")
	}
	if !strings.Contains(err.Error(), "template parlay.probe-x not found") {
		t.Errorf("error should carry gc's own message, got: %v", err)
	}
}

func TestGCSpawnResultEnvelopeShape(t *testing.T) {
	// The --json envelope is a typed contract for bin/parlay-spawn; field
	// names are load-bearing.
	out, err := json.Marshal(gcSpawnResult{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ok", "agent_id", "session_id", "session_name", "template", "city_dir", "gc"} {
		if !strings.Contains(string(out), `"`+key+`"`) {
			t.Errorf("envelope lacks %q: %s", key, out)
		}
	}
	_ = config.ExitRuntime // anchor: the CLI wrapper dies with ExitRuntime on failure
}
