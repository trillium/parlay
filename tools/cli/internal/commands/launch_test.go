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

func TestLaunchNoKnownAgentsPrintsHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1") // unreachable, must not die

	out := captureStdout(t, func() { Launch(nil) })
	if !strings.Contains(out, "No agent homes found in") {
		t.Errorf("Launch() output = %q, want the no-agents hint", out)
	}
	if !strings.Contains(out, "parlay-bin spawn <id> <name> <color> <prompt> [--cwd PATH]") {
		t.Errorf("Launch() output = %q, want the creation hint", out)
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

func TestLaunchKnownTargetSpawnsWithoutCrashing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "")
	// Force exec.Command("parlay-bin", ...) to fail to start (not on PATH);
	// Launch must not propagate that error (matches Bun.spawnSync's ignored
	// result).
	t.Setenv("PATH", "")

	out := captureStderr(t, func() { Launch([]string{"agent-a"}) })
	if !strings.Contains(out, "spawning agent-a via parlay-bin spawn") {
		t.Errorf("Launch([agent-a]) stderr = %q, want the spawning announcement", out)
	}
}
