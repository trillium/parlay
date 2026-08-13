// Tests for launch.go (ticket B9, ported from packages/cli/src/commands/
// launch.ts, which has no dedicated TS test file — these cases were derived
// directly from reading the implementation). parlayAgentsDir() hardcodes
// join(homedir(), ".parlay", "agents") (see guard.go's doc comment), so
// isolation here goes through $HOME rather than $PARLAY_AGENT_HOME.
package commands

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// agentsServer stands up a one-route httptest server answering
// GET /api/chat/agents with the given JSON body (raw, so callers can pass
// wire.AgentInfo-shaped literals without importing the wire package here).
func agentsServer(t *testing.T, rawJSON string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(rawJSON))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeIdentityFixture(t *testing.T, home, id, name, color, cwd, model string) {
	t.Helper()
	dir := filepath.Join(home, ".parlay", "agents", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: " + id + "\nname: " + name + "\ncolor: " + color + "\n"
	if cwd != "" {
		body += "cwd: " + cwd + "\n"
	}
	if model != "" {
		body += "model: " + model + "\n"
	}
	body += "---\n"
	if err := os.WriteFile(filepath.Join(dir, "identity.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKnownAgentsDiscoversValidIdentities(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "")

	known := knownAgents()
	if len(known) != 1 {
		t.Fatalf("knownAgents() = %+v, want 1 entry", known)
	}
	if known[0].id != "agent-a" || known[0].name != "Agent A" || known[0].color != "#ff0000" || known[0].cwd != "/work/a" {
		t.Errorf("knownAgents()[0] = %+v", known[0])
	}
}

func TestKnownAgentsSkipsIncompleteFrontmatter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Missing color — id/name/color are all required.
	dir := filepath.Join(home, ".parlay", "agents", "agent-b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "identity.md"), []byte("---\nid: agent-b\nname: Agent B\n---\n"), 0o644)

	if known := knownAgents(); len(known) != 0 {
		t.Errorf("knownAgents() = %+v, want none (missing color)", known)
	}
}

func TestKnownAgentsCwdDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-c", "Agent C", "#00ff00", "", "")

	known := knownAgents()
	if len(known) != 1 || known[0].cwd != home {
		t.Errorf("knownAgents() = %+v, want cwd=%q", known, home)
	}
}

func TestKnownAgentsNoAgentsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if known := knownAgents(); len(known) != 0 {
		t.Errorf("knownAgents() = %+v, want none", known)
	}
}

// fakeSpawner puts an executable named `name` on a fresh PATH containing only
// dir, recording each invocation's argv (one arg per line) to dir/<name>.argv
// and exiting with exitCode. Returns that record file's path.
func fakeSpawner(t *testing.T, dir, name string, exitCode int) string {
	t.Helper()
	record := filepath.Join(dir, name+".argv")
	script := "#!/bin/sh\n: > " + record + "\nfor a in \"$@\"; do echo \"$a\" >> " + record + "; done\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestLaunchNoKnownAgentsPrintsHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1") // unreachable, must not die
	t.Setenv("PATH", "")                            // no spawner installed — hint names the shipped one

	out := captureStdout(t, func() { Launch(nil) })
	if !strings.Contains(out, "No agent homes found in") {
		t.Errorf("Launch() output = %q, want the no-agents hint", out)
	}
	if !strings.Contains(out, "parlay-spawn <id> <name> <color> <prompt> [--cwd PATH]") {
		t.Errorf("Launch() output = %q, want the creation hint", out)
	}
}

// robots-v81b: the hint must name a binary the reader actually has. With
// parlay-bin installed it is the preferred spawner and carries a subcommand.
func TestLaunchHintNamesTheInstalledSpawner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	bin := t.TempDir()
	fakeSpawner(t, bin, "parlay-bin", 0)
	t.Setenv("PATH", bin)

	out := captureStdout(t, func() { Launch(nil) })
	if !strings.Contains(out, "parlay-bin spawn <id> <name> <color> <prompt> [--cwd PATH]") {
		t.Errorf("Launch() output = %q, want the parlay-bin creation hint", out)
	}
}

func TestLaunchListsKnownAgentsWithLiveStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-live", "Live One", "#111111", "", "")
	writeIdentityFixture(t, home, "agent-off", "Off One", "#222222", "", "")

	srv := agentsServer(t, `[{"id":"agent-live","name":"Live One","color":"#111111"}]`)
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStdout(t, func() { Launch(nil) })
	if !strings.Contains(out, "2 known agent(s):") {
		t.Errorf("Launch() output = %q, want a 2-agent header", out)
	}
	if !strings.Contains(out, "agent-live") || !strings.Contains(out, "[live]") {
		t.Errorf("Launch() output = %q, want agent-live marked live", out)
	}
	if !strings.Contains(out, "agent-off") || !strings.Contains(out, "[offline]") {
		t.Errorf("Launch() output = %q, want agent-off marked offline", out)
	}
}

func TestLaunchOfflineHintOnStderr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-off", "Off One", "#222222", "", "")

	srv := agentsServer(t, `[]`)
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStderr(t, func() { Launch(nil) })
	if !strings.Contains(out, "To launch an offline agent:") || !strings.Contains(out, "parlay launch agent-off") {
		t.Errorf("Launch() stderr = %q, want the offline launch hint", out)
	}
}

func TestLaunchUnreachableServerTreatsAllOffline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "", "")
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")

	out := captureStdout(t, func() { Launch(nil) })
	if !strings.Contains(out, "[offline]") {
		t.Errorf("Launch() output = %q, want offline when server unreachable", out)
	}
}

func TestLaunchUnknownTargetDies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "", "")

	code, exited := withExitTrap(t, func() { Launch([]string{"ghost"}) })
	if !exited || code != 2 {
		t.Errorf("Launch([ghost]) exited=%v code=%d, want exit 2", exited, code)
	}
}

// robots-v81b: no spawner on PATH used to be a silent no-op — the announcement
// printed, the ENOENT from exec was discarded, and launch exited 0 having
// launched nothing. It must now die loudly.
func TestLaunchDiesWhenNoSpawnerOnPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "")
	t.Setenv("PATH", "")

	var code int
	var exited bool
	out := captureStderr(t, func() { code, exited = withExitTrap(t, func() { Launch([]string{"agent-a"}) }) })
	if !exited || code != 1 {
		t.Errorf("Launch([agent-a]) exited=%v code=%d, want exit 1", exited, code)
	}
	if !strings.Contains(out, "no spawner on PATH") || !strings.Contains(out, "parlay-spawn") {
		t.Errorf("Launch([agent-a]) stderr = %q, want a loud unresolvable-spawner error", out)
	}
}

// robots-v81b: with only the bash spawner installed, launch must use it — and
// pass the positional contract with NO `spawn` subcommand word.
func TestLaunchExecsParlaySpawnWithoutSubcommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "opus")
	bin := t.TempDir()
	record := fakeSpawner(t, bin, "parlay-spawn", 0)
	t.Setenv("PATH", bin)

	out := captureStderr(t, func() { Launch([]string{"agent-a"}) })
	if !strings.Contains(out, "spawning agent-a via parlay-spawn") {
		t.Errorf("Launch([agent-a]) stderr = %q, want the spawning announcement", out)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("spawner was never executed: %v", err)
	}
	argv := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if argv[0] != "agent-a" || argv[1] != "Agent A" || argv[2] != "#ff0000" {
		t.Errorf("spawner argv = %q, want id/name/color first", argv)
	}
	joined := strings.Join(argv, "\x00")
	if !strings.Contains(joined, "--cwd\x00/work/a") || !strings.Contains(joined, "--model\x00opus") {
		t.Errorf("spawner argv = %q, want --cwd and --model passed through", argv)
	}
}

// robots-v81b: parlay-bin wins when present, and gets the `spawn` subcommand.
func TestLaunchPrefersParlayBinWithSubcommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "")
	bin := t.TempDir()
	record := fakeSpawner(t, bin, "parlay-bin", 0)
	fakeSpawner(t, bin, "parlay-spawn", 0)
	t.Setenv("PATH", bin)

	captureStderr(t, func() { Launch([]string{"agent-a"}) })
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("parlay-bin was never executed: %v", err)
	}
	argv := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if argv[0] != "spawn" || argv[1] != "agent-a" {
		t.Errorf("parlay-bin argv = %q, want the spawn subcommand first", argv)
	}
}

// robots-v81b: a spawner that runs but fails is also a failed launch.
func TestLaunchDiesWhenSpawnerExitsNonZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "")
	bin := t.TempDir()
	fakeSpawner(t, bin, "parlay-spawn", 7)
	t.Setenv("PATH", bin)

	var code int
	var exited bool
	out := captureStderr(t, func() { code, exited = withExitTrap(t, func() { Launch([]string{"agent-a"}) }) })
	if !exited || code != 1 {
		t.Errorf("Launch([agent-a]) exited=%v code=%d, want exit 1", exited, code)
	}
	if !strings.Contains(out, "failed to spawn agent-a") {
		t.Errorf("Launch([agent-a]) stderr = %q, want the spawn-failure error", out)
	}
}
