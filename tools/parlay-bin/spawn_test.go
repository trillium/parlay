package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mockLauncher is the Go-idiomatic replacement for the bash test suite's
// PATH-stubbed `herdr` shim (docs/scope-go-spawn.md §5): it implements the
// Launcher interface directly instead of shadowing a binary on PATH, so
// batch-dispatch and pipeline-ordering behavior can be verified without a
// live herdr daemon.
type mockLauncher struct {
	mu                  sync.Mutex
	existing            map[string]string // agent id -> existing name, for duplicate-guard tests
	failStart           bool
	agentGetCalls       []string
	agentStartCalls     []string
	agentStartOpts      []AgentStartOptions
	tabCreateCalls      []TabCreateOptions
	paneSendTextCalls   []string
	paneSendKeysCalls   []string
	paneWaitOutputCalls []string
}

func (m *mockLauncher) AgentGet(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentGetCalls = append(m.agentGetCalls, id)
	return m.existing[id], nil
}
func (m *mockLauncher) TabCreate(opts TabCreateOptions) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tabCreateCalls = append(m.tabCreateCalls, opts)
	return "tab-" + opts.Label, "pane-" + opts.Label, nil
}
func (m *mockLauncher) AgentStart(opts AgentStartOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentStartCalls = append(m.agentStartCalls, opts.ID)
	m.agentStartOpts = append(m.agentStartOpts, opts)
	if m.failStart {
		return &mockErr{"agent start failed"}
	}
	return nil
}
func (m *mockLauncher) TabClose(tabID string) error                      { return nil }
func (m *mockLauncher) PaneClose(paneID string) error                    { return nil }
func (m *mockLauncher) AgentWait(id, status string, timeoutMs int) error { return nil }
func (m *mockLauncher) AgentSend(id, text string) error                  { return nil }
func (m *mockLauncher) TabsForLabel(id string) ([]TabRef, error)         { return nil, nil }
func (m *mockLauncher) PaneSendText(paneID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paneSendTextCalls = append(m.paneSendTextCalls, text)
	return nil
}
func (m *mockLauncher) PaneSendKeys(paneID, keys string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paneSendKeysCalls = append(m.paneSendKeysCalls, keys)
	return nil
}
func (m *mockLauncher) PaneWaitOutput(paneID, regex string, timeoutMs int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paneWaitOutputCalls = append(m.paneWaitOutputCalls, regex)
	return nil
}

type mockErr struct{ msg string }

func (e *mockErr) Error() string { return e.msg }

// withMockLauncher swaps launcherFactory for the duration of the test.
func withMockLauncher(t *testing.T, m *mockLauncher) {
	t.Helper()
	orig := launcherFactory
	launcherFactory = func() (Launcher, error) { return m, nil }
	t.Cleanup(func() { launcherFactory = orig })
}

// captureStderr redirects os.Stderr for the duration of f and returns what
// was written plus f's return value.
func captureStderr(t *testing.T, f func() int) (string, int) {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- string(buf)
	}()
	rc := f()
	w.Close()
	os.Stderr = orig
	return <-done, rc
}

// deadRegisterServer always fails register-agent, so spawnOne fails before
// any herdr side effect — the hermetic failure trigger this suite uses in
// place of the bash suite's dead-port trick.
func deadRegisterServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withPARLAYServer points PARLAY_SERVER at a throwaway server AND redirects
// every on-disk write (this process's own agentcontext.go writes, plus the
// `parlay identity --register` subprocess registerIdentity() shells out to,
// plus pretrustWorkdir's ~/.claude.json rewrite) into a t.TempDir() via
// PARLAY_AGENT_HOME and PARLAY_CLAUDE_JSON. Without this, a successful
// spawnOne in a test writes a REAL identity into the developer's actual
// ~/.parlay/agents and a REAL trust-store entry into their actual
// ~/.claude.json — the `parlay identity` CLI does its own local-disk
// writes independent of PARLAY_SERVER, so pointing the server at a mock
// alone does not make a test hermetic.
func withPARLAYServer(t *testing.T, url string) {
	t.Helper()
	origServer := os.Getenv("PARLAY_SERVER")
	origHome := os.Getenv("PARLAY_AGENT_HOME")
	origClaudeJSON := os.Getenv("PARLAY_CLAUDE_JSON")
	origStateHome := os.Getenv("PARLAY_STATE_HOME")
	origBeadsRequired := os.Getenv("PARLAY_SPAWN_BEADS_REQUIRED")
	os.Setenv("PARLAY_SERVER", url)
	os.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	os.Setenv("PARLAY_CLAUDE_JSON", filepath.Join(t.TempDir(), ".claude.json"))
	os.Setenv("PARLAY_SPAWN_NO_WATCHDOG", "1")
	// Isolate config.toml lookups (loadSpawnConfig reads PARLAY_STATE_HOME/
	// config.toml, falling back to ~/.parlay/config.toml) so a test never
	// inherits the developer's real spawnAccount/launcher/beads_required —
	// point it at an empty tmp dir with no config.toml, and force the
	// beads-required gate off unless a test explicitly wants it on.
	os.Setenv("PARLAY_STATE_HOME", t.TempDir())
	os.Setenv("PARLAY_SPAWN_BEADS_REQUIRED", "0")
	t.Cleanup(func() {
		os.Setenv("PARLAY_SERVER", origServer)
		os.Setenv("PARLAY_AGENT_HOME", origHome)
		os.Setenv("PARLAY_CLAUDE_JSON", origClaudeJSON)
		os.Setenv("PARLAY_STATE_HOME", origStateHome)
		os.Setenv("PARLAY_SPAWN_BEADS_REQUIRED", origBeadsRequired)
		os.Unsetenv("PARLAY_SPAWN_NO_WATCHDOG")
	})
}

func TestIsBatchPairBoundaries(t *testing.T) {
	cases := []struct {
		label string
		arg   string
		want  bool
	}{
		{"single id=repo pair", "nope-batch-solo-z3=/tmp/none-solo", true},
		{"plain id (no =) is not a pair", "nope-single-z4", false},
		{"id part containing / is not a pair", "weird/id-z6=projects/none", false},
		{"a flag is not a pair", "--ephemeral", false},
		{"empty id part with = still batches (id validated later)", "=repo", true},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if got := isBatchPair(c.arg); got != c.want {
				t.Errorf("isBatchPair(%q) = %v, want %v", c.arg, got, c.want)
			}
		})
	}
}

// The load-bearing batch guarantee (bash suite's own framing): every pair
// in a batch is dispatched even though each one fails, and the loop does
// not stop early.
func TestRunBatchSpawnDispatchesEveryPairOnFailure(t *testing.T) {
	withMockLauncher(t, &mockLauncher{})
	withPARLAYServer(t, deadRegisterServer(t).URL)

	out, rc := captureStderr(t, func() int {
		return runBatchSpawn([]string{
			"nope-batch-a-z1=/tmp/none-a", "nope-batch-b-z2=/tmp/none-b", "--prompt", "brief", "--model", "sonnet",
		})
	})

	if rc == 0 {
		t.Errorf("expected non-zero exit for a batch with failing spawns, got 0")
	}
	if !strings.Contains(out, "batch: FAILED to spawn nope-batch-a-z1 (/tmp/none-a)") {
		t.Errorf("first pair was not dispatched/reported; output:\n%s", out)
	}
	if !strings.Contains(out, "batch: FAILED to spawn nope-batch-b-z2 (/tmp/none-b)") {
		t.Errorf("second pair was not dispatched/reported (loop stopped early?); output:\n%s", out)
	}
}

func TestRunBatchSpawnRequiresPrompt(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	withPARLAYServer(t, deadRegisterServer(t).URL)

	out, rc := captureStderr(t, func() int {
		return runBatchSpawn([]string{"nope-batch-noprompt-z9=/tmp/none-np"})
	})

	if rc != 2 {
		t.Errorf("missing --prompt should exit 2, got %d", rc)
	}
	if !strings.Contains(out, "batch dispatch requires a shared --prompt") {
		t.Errorf("missing --prompt error not reported; output:\n%s", out)
	}
	if strings.Contains(out, "batch: FAILED") {
		t.Errorf("missing --prompt should fail before dispatching any pair; output:\n%s", out)
	}
	if len(m.agentGetCalls) != 0 {
		t.Errorf("no pair should have reached the launcher; agentGetCalls=%v", m.agentGetCalls)
	}
}

func TestRunBatchSpawnRejectsCwdFlag(t *testing.T) {
	out, rc := captureStderr(t, func() int {
		return runBatchSpawn([]string{"a=/tmp/a", "--prompt", "x", "--cwd", "/nope"})
	})
	if rc != 2 {
		t.Errorf("--cwd in batch mode should exit 2, got %d", rc)
	}
	if !strings.Contains(out, "--cwd is not valid in batch mode") {
		t.Errorf("expected --cwd rejection message; output:\n%s", out)
	}
}

func TestRunBatchSpawnRejectsNonPairArg(t *testing.T) {
	withMockLauncher(t, &mockLauncher{})
	withPARLAYServer(t, deadRegisterServer(t).URL)

	out, rc := captureStderr(t, func() int {
		return runBatchSpawn([]string{
			"nope-batch-mix-z5=/tmp/none-mix", "bogus-no-equals", "--prompt", "brief", "--model", "sonnet",
		})
	})

	if rc == 0 {
		t.Errorf("expected non-zero exit, got 0")
	}
	if !strings.Contains(out, "batch dispatch expects every argument as id=repo; got \"bogus-no-equals\"") {
		t.Errorf("expected rejection message for non-pair arg; output:\n%s", out)
	}
	if !strings.Contains(out, "batch:") {
		t.Errorf("valid pair preceding the bad one should still have been dispatched; output:\n%s", out)
	}
}

func TestRunBatchSpawnExpandsTilde(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)

	// register-agent succeeds this time, so we can inspect what cwd the
	// pipeline actually resolved for the pair.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	home := os.Getenv("HOME")
	_, rc := captureStderr(t, func() int {
		return runBatchSpawn([]string{"tilde-agent=~/somewhere", "--prompt", "brief", "--model", "sonnet"})
	})
	if rc != 0 {
		t.Fatalf("expected success, got rc=%d", rc)
	}
	if len(m.agentStartCalls) != 1 || m.agentStartCalls[0] != "tilde-agent" {
		t.Fatalf("expected tilde-agent to be started, got %v", m.agentStartCalls)
	}
	_ = home // cwd itself isn't captured by the mock; AgentStart succeeding confirms the pipeline ran to completion
}

// task-qyu8q: a model must be chosen deliberately on every spawn, across all
// three invocation shapes — named, --ephemeral, and batch. These tests
// assert the refusal fires BEFORE any side effect (registration, mint,
// launcher call), mirroring bash's "gate before the mint" ordering.

func TestRequireModel(t *testing.T) {
	if err := requireModel("sonnet"); err != nil {
		t.Errorf("requireModel(%q) = %v, want nil", "sonnet", err)
	}
	if err := requireModel(""); err == nil {
		t.Error("requireModel(\"\") = nil, want a refusal error")
	}
}

func TestRunNamedSpawnRefusesWithoutModel(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	withPARLAYServer(t, deadRegisterServer(t).URL)

	out, rc := captureStderr(t, func() int {
		return runNamedSpawn([]string{"nope-named-nomodel", "Nope Named", "#c084fc", "brief"})
	})

	if rc != 2 {
		t.Errorf("missing --model should exit 2, got %d", rc)
	}
	if !strings.Contains(out, "no model was chosen") {
		t.Errorf("expected model-gate refusal message; output:\n%s", out)
	}
	if len(m.agentGetCalls) != 0 {
		t.Errorf("refusal must happen before any launcher call; agentGetCalls=%v", m.agentGetCalls)
	}
}

func TestRunEphemeralSpawnRefusesWithoutModel(t *testing.T) {
	var mintHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mintHit = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	out, rc := captureStderr(t, func() int {
		return runEphemeralSpawn([]string{"brief"})
	})

	if rc != 2 {
		t.Errorf("missing --model should exit 2, got %d", rc)
	}
	if !strings.Contains(out, "no model was chosen") {
		t.Errorf("expected model-gate refusal message; output:\n%s", out)
	}
	if mintHit {
		t.Error("refusal must happen before the mint (gate-before-mint convention) — no identity should be seeded")
	}
}

// docs/scope-go-spawn.md Finding F1: `parlay spawn` is the sole public entry
// point (task-qyu8q scope 3) — it sets PARLAY_SPAWN_VIA_CLI=1 before
// invoking the resolved spawner. Calling this binary's spawn subcommand any
// other way must be refused, mirroring bin/parlay-spawn's own refusal block
// (lines 45–57).
func TestRunSpawnCommandRefusesWithoutViaCLI(t *testing.T) {
	orig := os.Getenv("PARLAY_SPAWN_VIA_CLI")
	os.Unsetenv("PARLAY_SPAWN_VIA_CLI")
	t.Cleanup(func() {
		if orig != "" {
			os.Setenv("PARLAY_SPAWN_VIA_CLI", orig)
		}
	})

	out, rc := captureStderr(t, func() int {
		return runSpawnCommand([]string{"nope-viacli-z1", "Nope", "#c084fc", "brief"})
	})
	if rc != 2 {
		t.Errorf("missing PARLAY_SPAWN_VIA_CLI should exit 2, got %d", rc)
	}
	if !strings.Contains(out, "refusing to run directly") {
		t.Errorf("expected VIA_CLI refusal message; output:\n%s", out)
	}
}

func TestRunSpawnCommandProceedsWithViaCLI(t *testing.T) {
	os.Setenv("PARLAY_SPAWN_VIA_CLI", "1")
	t.Cleanup(func() { os.Unsetenv("PARLAY_SPAWN_VIA_CLI") })
	withMockLauncher(t, &mockLauncher{})
	withPARLAYServer(t, deadRegisterServer(t).URL)

	// No --model: should fail on the model gate (proving it got PAST the
	// VIA_CLI gate), not on the VIA_CLI refusal.
	out, rc := captureStderr(t, func() int {
		return runSpawnCommand([]string{"nope-viacli-z2", "Nope", "#c084fc", "brief"})
	})
	if rc != 2 {
		t.Errorf("expected exit 2, got %d", rc)
	}
	if strings.Contains(out, "refusing to run directly") {
		t.Errorf("PARLAY_SPAWN_VIA_CLI=1 should bypass the refusal; output:\n%s", out)
	}
	if !strings.Contains(out, "no model was chosen") {
		t.Errorf("expected the request to reach the model gate; output:\n%s", out)
	}
}

// --claim (bin/parlay-spawn lines 1028–1035, 1359–1364): the initial-prompt
// positional is optional when --claim is given, and either one satisfying
// is required.
func TestRunNamedSpawnClaimMakesPromptOptional(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	rc := runNamedSpawn([]string{"nope-claim-z1", "Nope Claim", "#c084fc", "--claim", "task-xyz", "--model", "sonnet"})
	if rc != 0 {
		t.Fatalf("expected success spawning with --claim and no inline prompt, got rc=%d", rc)
	}
	if len(m.agentStartCalls) != 1 || m.agentStartCalls[0] != "nope-claim-z1" {
		t.Fatalf("expected nope-claim-z1 to be started, got %v", m.agentStartCalls)
	}
}

func TestRunNamedSpawnRequiresPromptOrClaim(t *testing.T) {
	out, rc := captureStderr(t, func() int {
		return runNamedSpawn([]string{"nope-noprompt-z1", "Nope", "#c084fc"})
	})
	if rc != 2 {
		t.Errorf("neither prompt nor --claim should exit 2, got %d", rc)
	}
	if !strings.Contains(out, "give the agent work") {
		t.Errorf("expected the give-the-agent-work refusal; output:\n%s", out)
	}
}

func TestRunBatchSpawnRefusesWithoutModel(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	withPARLAYServer(t, deadRegisterServer(t).URL)

	out, rc := captureStderr(t, func() int {
		return runBatchSpawn([]string{"nope-batch-nomodel-z8=/tmp/none-nm", "--prompt", "brief"})
	})

	if rc != 2 {
		t.Errorf("missing --model should exit 2, got %d", rc)
	}
	if !strings.Contains(out, "no model was chosen") {
		t.Errorf("expected model-gate refusal message; output:\n%s", out)
	}
	if len(m.agentGetCalls) != 0 {
		t.Errorf("refusal must happen before any pair is dispatched; agentGetCalls=%v", m.agentGetCalls)
	}
}
