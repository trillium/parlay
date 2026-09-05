package spawn

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	start := m.agentStartOpts[0]
	if start.Kind != "claude" {
		t.Errorf("expected --kind claude, got %q", start.Kind)
	}
	// Argv entries, not a shell string: `herdr agent start` types its
	// trailing args after the kind's canonical executable, so the --settings
	// JSON is one unquoted argument here and herdr does the encoding.
	wantArgs := []string{
		"--dangerously-skip-permissions",
		"--strict-mcp-config",
		"--fallback-model", "sonnet",
		"--settings", `{"enabledPlugins":{"posthog@claude-plugins-official":false}}`,
		"--model", "sonnet",
	}
	if strings.Join(start.Cmd, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Errorf("herdr launch argv mismatch\n got: %q\nwant: %q", start.Cmd, wantArgs)
	}

	// The charter never rides in the argv — herdr refuses to encode its
	// newlines — so it must arrive through `agent prompt` instead.
	if len(m.agentPromptCalls) != 1 {
		t.Fatalf("expected exactly one AgentPrompt call, got %d", len(m.agentPromptCalls))
	}
	if !strings.Contains(m.agentPromptCalls[0], "nope-flags-z1") {
		t.Errorf("expected the startup charter to be submitted via agent prompt; got %q", m.agentPromptCalls[0])
	}

	if len(m.tabCreateCalls) != 1 {
		t.Fatalf("expected exactly one TabCreate call, got %d", len(m.tabCreateCalls))
	}
	env := strings.Join(m.tabCreateCalls[0].Env, "\n")
	for _, want := range []string{"PARLAY_SPAWN_MODEL=sonnet", "PARLAY_AGENT_MODEL=sonnet", "PARLAY_AGENT_NAME=Nope Flags", "PARLAY_AGENT_COLOR=#c084fc"} {
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

// withInstantRetrySleep stubs the 500ms agent_pane_busy retry pause so the
// retry-budget tests run instantly.
func withInstantRetrySleep(t *testing.T) {
	orig := startRetrySleep
	startRetrySleep = func() {}
	t.Cleanup(func() { startRetrySleep = orig })
}

// robots-i4pi / robots-naet: the FIRST `herdr agent start` reliably rejects
// with agent_pane_busy on a brand-new pane — a busy rejection is transient
// and must be retried until the pane settles, then the spawn succeeds.
func TestSpawnRetriesAgentPaneBusyUntilPaneSettles(t *testing.T) {
	m := &mockLauncher{busyStarts: 3}
	withMockLauncher(t, m)
	withInstantRetrySleep(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	out, rc := captureStderr(t, func() int {
		return runNamedSpawn([]string{"nope-busy-z1", "Nope Busy", "#c084fc", "brief", "--model", "sonnet"})
	})
	if rc != 0 {
		t.Fatalf("expected the retried spawn to succeed, got rc=%d; stderr:\n%s", rc, out)
	}
	if len(m.agentStartCalls) != 4 {
		t.Errorf("expected 3 busy attempts + 1 success = 4 AgentStart calls, got %d", len(m.agentStartCalls))
	}
	if !strings.Contains(out, "agent_pane_busy on pane") {
		t.Errorf("expected a busy-retry progress line on stderr; got:\n%s", out)
	}
}

// The retry budget honors PARLAY_SPAWN_START_RETRIES and, once exhausted,
// rolls back rather than looping forever.
func TestSpawnBusyRetryBudgetExhausts(t *testing.T) {
	m := &mockLauncher{busyStarts: 99}
	withMockLauncher(t, m)
	withInstantRetrySleep(t)
	t.Setenv("PARLAY_SPAWN_START_RETRIES", "3")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	out, rc := captureStderr(t, func() int {
		return runNamedSpawn([]string{"nope-busy-z2", "Nope Busy", "#c084fc", "brief", "--model", "sonnet"})
	})
	if rc == 0 {
		t.Fatal("expected the spawn to fail once the busy budget is exhausted")
	}
	if len(m.agentStartCalls) != 3 {
		t.Errorf("expected exactly 3 AgentStart attempts (PARLAY_SPAWN_START_RETRIES=3), got %d", len(m.agentStartCalls))
	}
	// The exact count matters: a loop that reports its cursor rather than the
	// starts it issued says "after 4 attempt(s)" here. Assert the number, not
	// just the prefix, or that off-by-one rides along unnoticed.
	if !strings.Contains(out, "herdr agent start failed after 3 attempt(s)") {
		t.Errorf("expected the rollback message to report exactly 3 attempts; got:\n%s", out)
	}
}

// A non-busy start failure is non-transient: exactly one attempt, no retry.
func TestSpawnNonBusyStartFailureDoesNotRetry(t *testing.T) {
	m := &mockLauncher{failStart: true}
	withMockLauncher(t, m)
	withInstantRetrySleep(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	_, rc := captureStderr(t, func() int {
		return runNamedSpawn([]string{"nope-busy-z3", "Nope Busy", "#c084fc", "brief", "--model", "sonnet"})
	})
	if rc == 0 {
		t.Fatal("expected the spawn to fail on a non-transient start error")
	}
	if len(m.agentStartCalls) != 1 {
		t.Errorf("a non-busy failure must not be retried; got %d AgentStart calls", len(m.agentStartCalls))
	}
}

// task-20czm: the herdr launcher must honor --kind. Before this, it passed a
// hardcoded `--kind claude` plus a fixed `bash -lc 'exec claude …'` script,
// so `--kind opencode` silently launched claude. The kind now reaches
// `herdr agent start --kind` (which resolves the canonical executable), and
// only claude gets the YOLO flag set — every other harness takes an explicit
// --model and relies on its own config (bin/parlay-spawn:1650-1654).
func TestSpawnOneHerdrHonorsNonClaudeKind(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	rc := runNamedSpawn([]string{"nope-kind-z1", "Nope Kind", "#c084fc", "brief", "--kind", "opencode", "--model", "opencode-go/deepseek-v4-pro"})
	if rc != 0 {
		t.Fatalf("expected success, got rc=%d", rc)
	}
	if len(m.agentStartOpts) != 1 {
		t.Fatalf("expected exactly one AgentStart call, got %d", len(m.agentStartOpts))
	}
	start := m.agentStartOpts[0]
	if start.Kind != "opencode" {
		t.Errorf("expected --kind opencode to reach herdr, got %q", start.Kind)
	}
	want := []string{"--model", "opencode-go/deepseek-v4-pro"}
	if strings.Join(start.Cmd, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("a non-claude kind takes only --model, never claude's YOLO flags\n got: %q\nwant: %q", start.Cmd, want)
	}
	if len(m.agentPromptCalls) != 1 {
		t.Errorf("expected the charter to be delivered via agent prompt, got %d calls", len(m.agentPromptCalls))
	}
}

// A charter that never lands leaves a started agent with no task, so a failed
// `agent prompt` rolls the tab back exactly like a failed start
// (bin/parlay-spawn:1685-1689).
func TestSpawnOneHerdrPromptFailureRollsBack(t *testing.T) {
	m := &mockLauncher{failPrompt: true}
	withMockLauncher(t, m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	out, rc := captureStderr(t, func() int {
		return runNamedSpawn([]string{"nope-prompt-z1", "Nope Prompt", "#c084fc", "brief", "--model", "sonnet"})
	})
	if rc == 0 {
		t.Fatal("expected the spawn to fail when the charter cannot be delivered")
	}
	if !strings.Contains(out, "herdr agent prompt failed to deliver the charter") {
		t.Errorf("expected the charter-delivery failure to be reported; got:\n%s", out)
	}
}

// CodeRabbit, PR #273: `--kind ""` survives the flag parser, which only
// checks that a value follows the flag. Unnormalized it reached the
// subprocess launcher as `exec ”` (a command that cannot run) and the gc
// launcher as a refusal reading `got ""`. spawnOne normalizes once, before
// any launcher branch reads it.
func TestSpawnOneNormalizesEmptyKindBeforeLauncherDispatch(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	rc := runNamedSpawn([]string{"nope-emptykind-z1", "Empty Kind", "#c084fc", "brief", "--model", "sonnet", "--kind", ""})
	if rc != 0 {
		t.Fatalf("expected success, got rc=%d", rc)
	}
	if len(m.agentStartOpts) != 1 {
		t.Fatalf("expected exactly one AgentStart call, got %d", len(m.agentStartOpts))
	}
	if got := m.agentStartOpts[0].Kind; got != "claude" {
		t.Errorf("an empty --kind must normalize to claude before dispatch, got %q", got)
	}
	// The claude flag set must come with it — a normalized kind that skipped
	// the YOLO flags would stall on the first permission prompt.
	if !strings.Contains(strings.Join(m.agentStartOpts[0].Cmd, " "), "--dangerously-skip-permissions") {
		t.Errorf("normalized claude kind must carry the claude flag set; got %q", m.agentStartOpts[0].Cmd)
	}
}

// CodeRabbit, PR #273: the charter is task text, and this PR is what started
// persisting it on the herdr path too. It must not be world-readable, and the
// modes must be applied even when an earlier writer (writeAgentContext runs
// first) already created the directory at 0755.
func TestWriteStartupPromptKeepsTheCharterOwnerOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", home)

	// Pre-create the agent dir wide open, the way an earlier release left it.
	agentDir := filepath.Join(home, "perm-check-z1")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(agentDir, "startup-prompt.txt")
	if err := os.WriteFile(stale, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	promptFile, err := writeStartupPrompt("perm-check-z1", "the charter")
	if err != nil {
		t.Fatalf("writeStartupPrompt: %v", err)
	}

	fi, err := os.Stat(promptFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("charter file mode = %o, want 600 (it carries the task text)", got)
	}
	// The tightening happens BEFORE the write, so this also proves moving
	// the Chmod ahead of WriteFile did not cost us the write itself.
	body, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "the charter\n" {
		t.Errorf("charter content = %q, want the new charter (not the stale one)", body)
	}
	di, err := os.Stat(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("agent dir mode = %o, want 700 even when it already existed", got)
	}
}
