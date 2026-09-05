package spawn

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withRecordedWatchdog swaps the arming path for a recorder. Production
// arming launches a detached child process, which a test must never do.
func withRecordedWatchdog(t *testing.T) *[]watchdogSpec {
	t.Helper()
	var seen []watchdogSpec
	orig := armWatchdog
	armWatchdog = func(spec watchdogSpec) { seen = append(seen, spec) }
	t.Cleanup(func() { armWatchdog = orig })
	return &seen
}

// task-br4r6: every launcher gets a watchdog now. Before this, armWatchdog
// was called only on the herdr branch, so a --subprocess or gc spawn was
// watched by nothing at all (docs/scope-go-spawn.md's disclosed leftover).
func TestSpawnOneArmsWatchdogForSubprocessLauncher(t *testing.T) {
	refuseHerdr(t)
	seen := withRecordedWatchdog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	agentID := "nope-wd-sub-z1"
	rc := runNamedSpawn([]string{agentID, "Nope WD", "#c084fc", "brief", "--model", "sonnet", "--kind", "true", "--subprocess"})
	stateDir := defaultSubprocessStateDir(agentID)
	t.Cleanup(func() { _ = subprocessStop(stateDir) })
	if rc != 0 {
		t.Fatalf("expected success, got rc=%d", rc)
	}
	if len(*seen) != 1 {
		t.Fatalf("expected exactly one watchdog to be armed, got %d", len(*seen))
	}
	if (*seen)[0].Launcher != "subprocess" {
		t.Errorf("expected the subprocess watchdog arm, got %q", (*seen)[0].Launcher)
	}
	if (*seen)[0].Server != srv.URL {
		t.Errorf("watchdog must be told which server to observe; got %q", (*seen)[0].Server)
	}
}

// The herdr arm needs the charter on disk: a detached watchdog process
// cannot inherit the composed prompt from this process's memory.
func TestSpawnOneArmsHerdrWatchdogWithCharterOnDisk(t *testing.T) {
	m := &mockLauncher{}
	withMockLauncher(t, m)
	seen := withRecordedWatchdog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withPARLAYServer(t, srv.URL)

	rc := runNamedSpawn([]string{"nope-wd-herdr-z1", "Nope WD", "#c084fc", "brief", "--model", "sonnet"})
	if rc != 0 {
		t.Fatalf("expected success, got rc=%d", rc)
	}
	if len(*seen) != 1 || (*seen)[0].Launcher != "herdr" {
		t.Fatalf("expected one herdr watchdog, got %+v", *seen)
	}
	if !strings.HasSuffix((*seen)[0].PromptFile, "startup-prompt.txt") {
		t.Errorf("herdr watchdog needs a charter file to re-send; got %q", (*seen)[0].PromptFile)
	}
}

func TestWatchdogSpecArgsCarryEveryFlagItWasGiven(t *testing.T) {
	spec := watchdogSpec{
		Launcher: "gc", AgentID: "aid", Server: "http://s",
		AgentDir: "/a/dir", Session: "sess-1", CityDir: "/city",
	}
	got := strings.Join(spec.args(1234), " ")
	for _, want := range []string{
		"spawn-watchdog aid", "--launcher gc", "--server http://s",
		"--timeout-ms 1234", "--agent-dir /a/dir", "--session sess-1", "--city /city",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("watchdog argv missing %q; got: %s", want, got)
		}
	}
	// PromptFile is empty here, so its flag must not appear at all — an
	// empty --prompt-file would make the herdr arm read "".
	if strings.Contains(got, "--prompt-file") {
		t.Errorf("an unset field must not emit its flag; got: %s", got)
	}
}

// The subprocess arm confirms liveness from the agent's OWN emitted effect —
// its poll channel — not from the registration row the spawn pipeline itself
// created moments earlier.
func TestSpawnWatchdogSubprocessArmConfirmsOnPollChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"poll":{"count":1,"channels":[{"channel":"live-agent"}]}}`))
	}))
	t.Cleanup(srv.Close)

	out, rc := captureStderr(t, func() int {
		return RunSpawnWatchdog([]string{"live-agent", "--launcher", "subprocess", "--server", srv.URL, "--timeout-ms", "5000"})
	})
	if rc != 0 {
		t.Fatalf("expected the watchdog to confirm liveness (rc=0), got rc=%d; log:\n%s", rc, out)
	}
	if !strings.Contains(out, "observed in /api/chat/subscribers") {
		t.Errorf("expected the confirmation line in the watchdog log; got:\n%s", out)
	}
}

func TestSpawnWatchdogSubprocessArmReportsWhenNeverObserved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A registration row alone must NOT count as liveness.
		_, _ = w.Write([]byte(`{"presence":[{"channel":"quiet-agent","listening":false}]}`))
	}))
	t.Cleanup(srv.Close)

	out, rc := captureStderr(t, func() int {
		return RunSpawnWatchdog([]string{"quiet-agent", "--launcher", "subprocess", "--server", srv.URL, "--timeout-ms", "1", "--agent-dir", "/tmp/agent-dir"})
	})
	if rc != 1 {
		t.Fatalf("expected the watchdog to report a never-observed agent (rc=1), got rc=%d; log:\n%s", rc, out)
	}
	if !strings.Contains(out, "did not appear in /api/chat/subscribers") {
		t.Errorf("expected the timeout report in the watchdog log; got:\n%s", out)
	}
	if !strings.Contains(out, "parlay subprocess-ping quiet-agent") {
		t.Errorf("the timeout report should name the command that inspects the session; got:\n%s", out)
	}
}

func TestSpawnWatchdogRejectsBadUsage(t *testing.T) {
	for name, argv := range map[string][]string{
		"no agent id":      {"--launcher", "herdr", "--server", "http://s"},
		"unknown launcher": {"aid", "--launcher", "tmux", "--server", "http://s"},
		"unknown flag":     {"aid", "--nope"},
	} {
		_, rc := captureStderr(t, func() int { return RunSpawnWatchdog(argv) })
		if rc != 2 {
			t.Errorf("%s: expected a usage error (rc=2), got rc=%d", name, rc)
		}
	}
}
