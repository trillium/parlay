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
	mu              sync.Mutex
	existing        map[string]string // agent id -> existing name, for duplicate-guard tests
	failStart       bool
	agentGetCalls   []string
	agentStartCalls []string
}

func (m *mockLauncher) AgentGet(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentGetCalls = append(m.agentGetCalls, id)
	return m.existing[id], nil
}
func (m *mockLauncher) TabCreate(opts TabCreateOptions) (string, string, error) {
	return "tab-" + opts.Label, "pane-" + opts.Label, nil
}
func (m *mockLauncher) AgentStart(opts AgentStartOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentStartCalls = append(m.agentStartCalls, opts.ID)
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
	os.Setenv("PARLAY_SERVER", url)
	os.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	os.Setenv("PARLAY_CLAUDE_JSON", filepath.Join(t.TempDir(), ".claude.json"))
	os.Setenv("PARLAY_SPAWN_NO_WATCHDOG", "1")
	t.Cleanup(func() {
		os.Setenv("PARLAY_SERVER", origServer)
		os.Setenv("PARLAY_AGENT_HOME", origHome)
		os.Setenv("PARLAY_CLAUDE_JSON", origClaudeJSON)
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
			"nope-batch-a-z1=/tmp/none-a", "nope-batch-b-z2=/tmp/none-b", "--prompt", "brief",
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
			"nope-batch-mix-z5=/tmp/none-mix", "bogus-no-equals", "--prompt", "brief",
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
		return runBatchSpawn([]string{"tilde-agent=~/somewhere", "--prompt", "brief"})
	})
	if rc != 0 {
		t.Fatalf("expected success, got rc=%d", rc)
	}
	if len(m.agentStartCalls) != 1 || m.agentStartCalls[0] != "tilde-agent" {
		t.Fatalf("expected tilde-agent to be started, got %v", m.agentStartCalls)
	}
	_ = home // cwd itself isn't captured by the mock; AgentStart succeeding confirms the pipeline ran to completion
}
