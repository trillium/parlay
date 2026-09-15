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
	bdBin, _ := fakeBDEnv(t, fakeUpstreamVersion, "0")
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
	// The city-level session provider is the herdr provider (unit 5's test
	// requirement: spawn-path tests run against the authored provider).
	cityTOML, err := os.ReadFile(filepath.Join(res.CityDir, "city.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cityTOML), `provider = "herdr"`) {
		t.Errorf("city.toml does not select the herdr session provider:\n%s", cityTOML)
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

	// Child env: parlay-owned GC_HOME, ambient + nesting markers scrubbed,
	// upstream bd dir first on PATH (so gc's own bd shell-outs agree with
	// the bootstrap's binary choice).
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
	for _, kv := range strings.Split(env, "\n") {
		if k, v, _ := strings.Cut(kv, "="); k == "PATH" {
			if !strings.HasPrefix(v, filepath.Dir(bdBin)+string(os.PathListSeparator)) {
				t.Errorf("child PATH must start with the upstream bd dir, got %q", v)
			}
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
	t.Setenv("PATH", t.TempDir())                 // nothing named gc on PATH
	_, _ = fakeBDEnv(t, fakeUpstreamVersion, "0") // gc refusal fires first; bd must not mask it

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
	_, _ = fakeBDEnv(t, fakeUpstreamVersion, "0")

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
	_, _ = fakeBDEnv(t, fakeUpstreamVersion, "0")

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

func TestGCSpawnRunPiKindRendersPiTemplate(t *testing.T) {
	state := testsupport.TempStateHome(t)
	bin, _ := writeSpawnFakeGC(t, fakeSessionNewOK, 0)
	t.Setenv("PARLAY_GC", bin)
	_, _ = fakeBDEnv(t, fakeUpstreamVersion, "0")

	res, err := gcSpawnRun(gctemplate.LaunchSpec{
		ID:     "spark-x",
		Kind:   "pi",
		Model:  "opencode-go/muse-spark-1.3-contributor",
		Server: "http://localhost:14242",
	})
	if err != nil {
		t.Fatalf("gcSpawnRun (pi kind): %v", err)
	}
	if !res.OK {
		t.Fatalf("result = %+v", res)
	}
	agentTOML, err := os.ReadFile(filepath.Join(state, "gascity", "city", "packs", "parlay", "agents", "spark-x", "agent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`pi --model opencode-go/muse-spark-1.3-contributor`,
		`process_names = ["pi"]`,
	} {
		if !strings.Contains(string(agentTOML), want) {
			t.Errorf("pi agent.toml missing %q:\n%s", want, agentTOML)
		}
	}
	if strings.Contains(string(agentTOML), "--dangerously-skip-permissions") {
		t.Errorf("pi template must not carry claude YOLO flags:\n%s", agentTOML)
	}
}

func TestGCSpawnRunRejectsUnknownKind(t *testing.T) {
	testsupport.TempStateHome(t)
	bin, _ := writeSpawnFakeGC(t, fakeSessionNewOK, 0)
	t.Setenv("PARLAY_GC", bin)
	_, _ = fakeBDEnv(t, fakeUpstreamVersion, "0")

	_, err := gcSpawnRun(gctemplate.LaunchSpec{ID: "probe-x", Kind: "opencode"})
	if err == nil {
		t.Fatal("expected a refusal for an unknown gc kind")
	}
	if !strings.Contains(err.Error(), `"opencode"`) {
		t.Errorf("refusal must name the kind, got: %v", err)
	}
}

func TestGCSpawnRunRefusesForkBD(t *testing.T) {
	testsupport.TempStateHome(t)
	bin, _ := writeSpawnFakeGC(t, fakeSessionNewOK, 0)
	t.Setenv("PARLAY_GC", bin)
	_, _ = fakeBDEnv(t, fakeForkVersion, "0")

	_, err := gcSpawnRun(gctemplate.LaunchSpec{ID: "probe-x"})
	if err == nil {
		t.Fatal("expected a refusal for the forked bd")
	}
	if !strings.Contains(err.Error(), "fork") {
		t.Errorf("refusal must name the fork, got: %v", err)
	}
}

func TestGCSpawnRunRefusesMissingBD(t *testing.T) {
	testsupport.TempStateHome(t)
	bin, _ := writeSpawnFakeGC(t, fakeSessionNewOK, 0)
	t.Setenv("PARLAY_GC", bin)
	t.Setenv("PARLAY_BD", "")
	// PATH carries the real world (fork first) — neutralise it so no bd
	// resolves at all.
	t.Setenv("PATH", t.TempDir())

	_, err := gcSpawnRun(gctemplate.LaunchSpec{ID: "probe-x"})
	if err == nil {
		t.Fatal("expected a refusal without bd")
	}
	if !strings.Contains(err.Error(), "bd not found") {
		t.Errorf("refusal must name the missing bd, got: %v", err)
	}
}

func TestGCSpawnResultEnvelopeShape(t *testing.T) {
	// The --json envelope is a typed contract for the spawn pipeline; field
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
