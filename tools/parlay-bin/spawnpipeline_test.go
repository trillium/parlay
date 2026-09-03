package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Three-way launcher selection (docs/scope-go-spawn.md gap matrix item 7):
// the subprocess and gc launchers must never touch herdr at all — proven
// here by making launcherFactory itself fail, so a spawn that reaches herdr
// would blow up immediately.
func refuseHerdr(t *testing.T) {
	t.Helper()
	orig := launcherFactory
	launcherFactory = func() (Launcher, error) { return nil, fmt.Errorf("herdr must not be invoked for this launcher") }
	t.Cleanup(func() { launcherFactory = orig })
}

func TestSpawnOneSubprocessLauncherNeverTouchesHerdr(t *testing.T) {
	refuseHerdr(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	agentID := "nope-subproc-z1"
	// "true" is a harmless no-op binary — using it as --kind proves the
	// pipeline wiring (state dir, pid file, env overrides) without ever
	// invoking a real `claude` process.
	out, rc := captureStderr(t, func() int {
		return runNamedSpawn([]string{agentID, "Nope Sub", "#c084fc", "brief", "--model", "sonnet", "--kind", "true", "--subprocess"})
	})
	stateDir := defaultSubprocessStateDir(agentID)
	t.Cleanup(func() { _ = subprocessStop(stateDir) })

	if rc != 0 {
		t.Fatalf("expected success, got rc=%d; output:\n%s", rc, out)
	}
	if !strings.Contains(out, "subprocess session") {
		t.Errorf("expected subprocess-launch confirmation; output:\n%s", out)
	}
}

func TestSpawnOneGCLauncherRejectsNonClaudeKind(t *testing.T) {
	refuseHerdr(t)
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "gc")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	out, rc := captureStderr(t, func() int {
		return runNamedSpawn([]string{"nope-gc-z2", "Nope GC", "#c084fc", "brief", "--model", "sonnet", "--kind", "opencode"})
	})
	if rc != 1 {
		t.Errorf("gc launcher + non-claude kind should refuse (rc=1), got rc=%d; output:\n%s", rc, out)
	}
	if !strings.Contains(out, "gc launcher only supports --kind claude") {
		t.Errorf("expected the gc kind-refusal message; output:\n%s", out)
	}
}

// --pane (in-place mode): skip herdr tab create entirely and reuse the
// caller's pane, exporting env via PaneSendText/PaneSendKeys/PaneWaitOutput
// instead (bin/parlay-spawn lines 1495-1498, 1557-1573).
func TestSpawnOneHerdrPaneInPlaceSkipsTabCreate(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	rc := runNamedSpawn([]string{"nope-pane-z1", "Nope Pane", "#c084fc", "brief", "--model", "sonnet", "--pane", "pane-caller-42"})
	if rc != 0 {
		t.Fatalf("expected success, got rc=%d", rc)
	}
	if len(m.tabCreateCalls) != 0 {
		t.Errorf("in-place mode must not create a new tab; tabCreateCalls=%v", m.tabCreateCalls)
	}
	if len(m.agentStartCalls) != 1 || m.agentStartCalls[0] != "nope-pane-z1" {
		t.Fatalf("expected nope-pane-z1 to be started, got %v", m.agentStartCalls)
	}
	if len(m.paneSendTextCalls) != 1 {
		t.Fatalf("expected exactly one env-prep pane send-text call, got %d", len(m.paneSendTextCalls))
	}
	if !strings.Contains(m.paneSendTextCalls[0], "PARLAY_SPAWN_PROMPT") {
		t.Errorf("pane-prep text should export the spawn env; got %q", m.paneSendTextCalls[0])
	}
	if len(m.paneSendKeysCalls) != 1 || m.paneSendKeysCalls[0] != "enter" {
		t.Errorf("expected a single 'enter' send-keys call, got %v", m.paneSendKeysCalls)
	}
	if len(m.paneWaitOutputCalls) != 1 {
		t.Errorf("expected a single wait-output call for the READY marker, got %v", m.paneWaitOutputCalls)
	}
}

// Parity regression (task-ub2l7, docs/scope-go-spawn.md gap matrix): the
// herdr launch command and env must carry the same load-bearing flags/vars
// bash's herdr path sends (bin/parlay-spawn:1628, :1557), even though this
// Go port never asserted on AgentStart's actual argv before this test
// existed — mockLauncher only recorded opts.ID. Catches the exact regression
// this task found and fixed: --strict-mcp-config/--settings silently missing
// from launchScript, and PARLAY_AGENT_MODEL never set alongside
// PARLAY_SPAWN_MODEL.
func TestSpawnOneHerdrLaunchCommandMatchesBashFlagsAndEnv(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	rc := runNamedSpawn([]string{"nope-flags-z1", "Nope Flags", "#c084fc", "brief", "--model", "sonnet"})
	if rc != 0 {
		t.Fatalf("expected success, got rc=%d", rc)
	}

	if len(m.agentStartOpts) != 1 {
		t.Fatalf("expected exactly one AgentStart call, got %d", len(m.agentStartOpts))
	}
	cmd := strings.Join(m.agentStartOpts[0].Cmd, " ")
	for _, flag := range []string{"--dangerously-skip-permissions", "--strict-mcp-config", "--fallback-model sonnet", `--settings '{"enabledPlugins":{"posthog@claude-plugins-official":false}}'`} {
		if !strings.Contains(cmd, flag) {
			t.Errorf("herdr launch command missing bash-parity flag %q; got: %s", flag, cmd)
		}
	}

	if len(m.tabCreateCalls) != 1 {
		t.Fatalf("expected exactly one TabCreate call, got %d", len(m.tabCreateCalls))
	}
	env := strings.Join(m.tabCreateCalls[0].Env, "\n")
	for _, want := range []string{"PARLAY_SPAWN_MODEL=sonnet", "PARLAY_AGENT_MODEL=sonnet"} {
		if !strings.Contains(env, want) {
			t.Errorf("herdr tab env missing bash-parity var %q; got: %s", want, env)
		}
	}
}

// --workspace, ID form: passed straight through to TabCreate without
// shelling out to herdr for resolution (workspace.go's resolveWorkspace
// short-circuits on the `^w[A-Za-z0-9]+$` id pattern).
func TestSpawnOneHerdrWorkspaceIDPassthrough(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	rc := runNamedSpawn([]string{"nope-ws-z1", "Nope WS", "#c084fc", "brief", "--model", "sonnet", "--workspace", "wAbc123"})
	if rc != 0 {
		t.Fatalf("expected success, got rc=%d", rc)
	}
	if len(m.tabCreateCalls) != 1 {
		t.Fatalf("expected exactly one TabCreate call, got %d", len(m.tabCreateCalls))
	}
	if m.tabCreateCalls[0].WorkspaceID != "wAbc123" {
		t.Errorf("expected the workspace id to pass through unchanged, got %q", m.tabCreateCalls[0].WorkspaceID)
	}
}
