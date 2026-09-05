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

	"github.com/trillium/parlay/tools/cli/internal/config"
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
	writeIdentityFixtureWithAccount(t, home, id, name, color, cwd, model, "")
}

func writeIdentityFixtureWithAccount(t *testing.T, home, id, name, color, cwd, model, account string) {
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
	if account != "" {
		body += "account: " + account + "\n"
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

	known, unlaunchable := knownAgents()
	if len(known) != 1 || unlaunchable != 0 {
		t.Fatalf("knownAgents() = %+v (unlaunchable %d), want 1 entry", known, unlaunchable)
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

	if known, unlaunchable := knownAgents(); len(known) != 0 || unlaunchable != 1 {
		t.Errorf("knownAgents() = %+v (unlaunchable %d), want none known and 1 unlaunchable (missing color)", known, unlaunchable)
	}
}

func TestKnownAgentsCwdDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-c", "Agent C", "#00ff00", "", "")

	known, _ := knownAgents()
	if len(known) != 1 || known[0].cwd != home {
		t.Errorf("knownAgents() = %+v, want cwd=%q", known, home)
	}
}

func TestKnownAgentsNoAgentsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if known, unlaunchable := knownAgents(); len(known) != 0 || unlaunchable != 0 {
		t.Errorf("knownAgents() = %+v (unlaunchable %d), want none", known, unlaunchable)
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
	if !strings.Contains(out, "parlay spawn <id> <name> <color> <prompt> [--cwd PATH]") {
		t.Errorf("Launch() output = %q, want the creation hint", out)
	}
}

// A home whose identity.md lacks the id/name/color launch spec (a bare
// `identity '<fact>'` write, never `identity --register`) must not be
// reported as "no agent homes found" — the directory visibly exists, so
// that line reads as a lie and points away from the real repair.
func TestLaunchUnlaunchableHomesNamedInHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1") // unreachable, must not die
	t.Setenv("PATH", "")
	dir := filepath.Join(home, ".parlay", "agents", "factual")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "identity.md"), []byte("- a fact, no frontmatter\n"), 0o644)

	out := captureStdout(t, func() { Launch(nil) })
	if !strings.Contains(out, "1 agent home(s) exist but lack") {
		t.Errorf("Launch() output = %q, want the unlaunchable-homes hint", out)
	}
	if !strings.Contains(out, "identity --register") {
		t.Errorf("Launch() output = %q, want the identity --register repair", out)
	}
	if strings.Contains(out, "No agent homes found") {
		t.Errorf("Launch() output = %q, must not claim no homes exist", out)
	}
}

// withListeners swaps the process-table probe for a scripted answer. A unit
// test cannot arm a real `parlay listen`, and without this every fixture agent
// would be classified [ghost] — correctly, since no listener exists.
func withListeners(t *testing.T, ok bool, ids ...string) {
	t.Helper()
	orig := liveListeners
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	liveListeners = func() (map[string]bool, bool) { return set, ok }
	t.Cleanup(func() { liveListeners = orig })
}

func TestLaunchStatusClassification(t *testing.T) {
	cases := []struct {
		name                                 string
		registered, hasListener, listenersOK bool
		want                                 string
	}{
		{"registered with a listener is live", true, true, true, statusLive},
		// The robots-jkwc defect itself: 148 rows said [live], 11 had a reader.
		{"registered with no listener is a ghost", true, false, true, statusGhost},
		{"unregistered is offline whatever ps says", false, true, true, statusOffline},
		{"unregistered and no listener is offline", false, false, true, statusOffline},
		// An unreadable process table is not evidence of a dead listener.
		{"unknown process table keeps a registered agent live", true, false, false, statusLive},
		{"unknown process table cannot invent a registration", false, false, false, statusOffline},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := launchStatus(tc.registered, tc.hasListener, tc.listenersOK); got != tc.want {
				t.Errorf("launchStatus(%v,%v,%v) = %q, want %q", tc.registered, tc.hasListener, tc.listenersOK, got, tc.want)
			}
		})
	}
}

func TestLaunchMarksARegisteredAgentWithNoListenerAsAGhost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-ghost", "Ghost One", "#333333", "", "")

	srv := agentsServer(t, `[{"id":"agent-ghost","name":"Ghost One","color":"#333333"}]`)
	t.Setenv("PARLAY_SERVER", srv.URL)
	withListeners(t, true) // registry says yes, the process table says no

	out := captureStdout(t, func() { Launch(nil) })
	if !strings.Contains(out, "[ghost]") {
		t.Errorf("Launch() output = %q, want agent-ghost marked [ghost], not [live]", out)
	}
	if strings.Contains(out, "[live]") {
		t.Errorf("Launch() output = %q, want NO [live] agent — that is the display lie", out)
	}
}

func TestLaunchGhostHintNamesTheRemedy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-ghost", "Ghost One", "#333333", "", "")

	srv := agentsServer(t, `[{"id":"agent-ghost","name":"Ghost One","color":"#333333"}]`)
	t.Setenv("PARLAY_SERVER", srv.URL)
	withListeners(t, true)

	out := captureStderr(t, func() { Launch(nil) })
	if !strings.Contains(out, "parlay agent-down agent-ghost") {
		t.Errorf("Launch() stderr = %q, want the agent-down remedy for the ghost", out)
	}
	if strings.Contains(out, "parlay launch agent-ghost") {
		t.Errorf("Launch() stderr = %q, a ghost is registered — it must not be listed as offline", out)
	}
}

func TestLaunchNeverCallsAnAgentAGhostWhenPsIsUnreadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-live", "Live One", "#111111", "", "")

	srv := agentsServer(t, `[{"id":"agent-live","name":"Live One","color":"#111111"}]`)
	t.Setenv("PARLAY_SERVER", srv.URL)
	withListeners(t, false) // ps failed: nothing is known either way

	out := captureStdout(t, func() { Launch(nil) })
	if !strings.Contains(out, "[live]") || strings.Contains(out, "[ghost]") {
		t.Errorf("Launch() output = %q, want [live] — a failed probe must not libel a working agent", out)
	}
}

func TestLaunchListsKnownAgentsWithLiveStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-live", "Live One", "#111111", "", "")
	writeIdentityFixture(t, home, "agent-off", "Off One", "#222222", "", "")

	srv := agentsServer(t, `[{"id":"agent-live","name":"Live One","color":"#111111"}]`)
	t.Setenv("PARLAY_SERVER", srv.URL)
	withListeners(t, true, "agent-live")

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

// The PARLAY_SPAWN_IMPL=bash escape hatch demands bin/parlay-spawn on PATH;
// with none there it must die loudly, never silently no-op (robots-v81b's
// lesson: a swallowed exec failure is indistinguishable from a launch).
func TestLaunchBashHatchDiesWhenNoSpawnerOnPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "")
	t.Setenv(config.SpawnImplEnv, "bash")
	t.Setenv("PATH", "")

	var code int
	var exited bool
	out := captureStderr(t, func() { code, exited = withExitTrap(t, func() { Launch([]string{"agent-a"}) }) })
	if !exited || code != 1 {
		t.Errorf("Launch([agent-a]) exited=%v code=%d, want exit 1", exited, code)
	}
	if !strings.Contains(out, "demands parlay-spawn, but it is not on PATH") {
		t.Errorf("Launch([agent-a]) stderr = %q, want a loud unresolvable-spawner error", out)
	}
}

// The bash escape hatch execs bin/parlay-spawn with the positional contract
// and NO `spawn` subcommand word.
func TestLaunchBashHatchExecsParlaySpawnWithoutSubcommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "opus")
	t.Setenv(config.SpawnImplEnv, "bash")
	bin := t.TempDir()
	record := fakeSpawner(t, bin, "parlay-spawn", 0)
	t.Setenv("PATH", bin)

	out := captureStderr(t, func() { Launch([]string{"agent-a"}) })
	if !strings.Contains(out, "spawning agent-a via parlay spawn") {
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

// task-42qot: with no override, launch runs the spawn pipeline IN-PROCESS —
// no spawner binary on PATH at all, and the request still reaches the
// pipeline's model gate (the fixture pins no model, so the gate refuses
// with exit 2). This is the proof the dispatch never shells out.
func TestLaunchDefaultRunsSpawnPipelineInProcess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PARLAY_STATE_HOME", filepath.Join(home, ".parlay"))
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "")
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	t.Setenv("PATH", "")

	var code int
	var exited bool
	out := captureStderr(t, func() { code, exited = withExitTrap(t, func() { Launch([]string{"agent-a"}) }) })
	if !exited || code != 2 {
		t.Errorf("Launch([agent-a]) exited=%v code=%d, want the in-process model gate's exit 2; stderr:\n%s", exited, code, out)
	}
	if !strings.Contains(out, "no model was chosen") {
		t.Errorf("Launch([agent-a]) stderr = %q, want the in-process model-gate refusal", out)
	}
}

// A spawner that runs but fails is also a failed launch — and since
// task-42qot the spawner's real exit code propagates verbatim instead of
// being flattened to 1.
func TestLaunchPropagatesSpawnerExitCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeIdentityFixture(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "")
	t.Setenv(config.SpawnImplEnv, "bash")
	bin := t.TempDir()
	fakeSpawner(t, bin, "parlay-spawn", 7)
	t.Setenv("PATH", bin)

	var code int
	var exited bool
	captureStderr(t, func() { code, exited = withExitTrap(t, func() { Launch([]string{"agent-a"}) }) })
	if !exited || code != 7 {
		t.Errorf("Launch([agent-a]) exited=%v code=%d, want the spawner's exit 7 propagated", exited, code)
	}
}

// spawnerArgv reads back the argv a fakeSpawner recorded, NUL-joined so a
// flag and its value can be asserted as an adjacent pair (a "--account" and
// an "acc2" that are both present but not adjacent is a different command).
func spawnerArgv(t *testing.T, record string) string {
	t.Helper()
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("spawner was never executed: %v", err)
	}
	return strings.Join(strings.Split(strings.TrimRight(string(got), "\n"), "\n"), "\x00")
}

// launchAccountFixture isolates HOME and the state home the config.toml is
// read from, so no real ~/.parlay/config.toml can decide the outcome. The
// ambient PARLAY_SPAWN_DEFAULT_ACCOUNT is already cleared package-wide by
// TestMain.
func launchAccountFixture(t *testing.T, identityAccount, configTOML string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PARLAY_STATE_HOME", filepath.Join(home, ".parlay"))
	// These tests observe the spawner argv from outside the process, so they
	// route through the bash escape hatch's exec seam — argv construction is
	// identical for the in-process default.
	t.Setenv(config.SpawnImplEnv, "bash")
	writeIdentityFixtureWithAccount(t, home, "agent-a", "Agent A", "#ff0000", "/work/a", "", identityAccount)
	if configTOML != "" {
		if err := os.WriteFile(filepath.Join(home, ".parlay", "config.toml"), []byte(configTOML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	record := fakeSpawner(t, bin, "parlay-spawn", 0)
	t.Setenv("PATH", bin)
	return record
}

// An identity that pins an `account:` must respawn under that ccjuggler
// account — otherwise the agent comes back on whatever token the launching
// shell happened to hold.
func TestLaunchPassesIdentityAccountToSpawner(t *testing.T) {
	record := launchAccountFixture(t, "acc2", "")

	captureStderr(t, func() { Launch([]string{"agent-a"}) })
	if argv := spawnerArgv(t, record); !strings.Contains(argv, "--account\x00acc2") {
		t.Errorf("spawner argv = %q, want --account acc2 passed through", argv)
	}
}

// An identity with no `account:` of its own must relaunch with NO --account,
// even when a config-level default exists. Launch no longer synthesizes that
// default into the argv (SpawnAccountArgs): the spawner resolves it itself,
// so the agent still lands on it, while the default stays live rather than
// being pinned. Synthesizing it would make the spawn pipeline read it as an
// explicit --account and persist it into identity.md (task-0d6mi's writer),
// outranking every later `parlay defaults set account` rotation.
func TestLaunchOmitsConfiguredDefaultForUnpinnedIdentity(t *testing.T) {
	record := launchAccountFixture(t, "", "spawnAccount = \"acc2\"\n")

	captureStderr(t, func() { Launch([]string{"agent-a"}) })
	argv := spawnerArgv(t, record)
	if strings.Contains(argv, "--account") {
		t.Errorf("spawner argv = %q, want no --account — the config default must stay live, not be pinned", argv)
	}
	if strings.Contains(argv, "acc2") {
		t.Errorf("spawner argv = %q, want the config default absent from the relaunch argv", argv)
	}
}

func TestLaunchIdentityAccountBeatsConfiguredDefault(t *testing.T) {
	record := launchAccountFixture(t, "identity-acc", "spawnAccount = \"config-acc\"\n")

	captureStderr(t, func() { Launch([]string{"agent-a"}) })
	argv := spawnerArgv(t, record)
	if !strings.Contains(argv, "--account\x00identity-acc") {
		t.Errorf("spawner argv = %q, want the identity's account to win", argv)
	}
	if strings.Contains(argv, "config-acc") {
		t.Errorf("spawner argv = %q, want the config default not to appear at all", argv)
	}
}

// With nothing configured, --account must be ABSENT rather than empty: the
// spawner rejects `--account` with no value (exit 2), so passing an empty
// string would turn "no account configured" into a hard launch failure.
func TestLaunchOmitsAccountWhenNoneConfigured(t *testing.T) {
	record := launchAccountFixture(t, "", "")

	captureStderr(t, func() { Launch([]string{"agent-a"}) })
	if argv := spawnerArgv(t, record); strings.Contains(argv, "--account") {
		t.Errorf("spawner argv = %q, want no --account flag at all", argv)
	}
}
