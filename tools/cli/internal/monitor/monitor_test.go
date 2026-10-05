package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func trapExit(t *testing.T) {
	t.Helper()
	orig := httpc.Exit
	httpc.Exit = testsupport.RecordingExit()
	t.Cleanup(func() { httpc.Exit = orig })
}

func TestCmdMonitorRequiresAgentUnlessLegacyPoll(t *testing.T) {
	trapExit(t)
	code, ok := testsupport.Capture(func() {
		CmdMonitor(nil)
	})
	if !ok {
		t.Fatal("expected Die when neither --agent nor --legacy-poll is given")
	}
	if code != config.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, config.ExitUsage)
	}
}

func TestCmdMonitorHelpDoesNotDie(t *testing.T) {
	trapExit(t)
	_, ok := testsupport.Capture(func() {
		CmdMonitor([]string{"--help"})
	})
	if ok {
		t.Fatal("--help should print usage and return, not die")
	}
}

func TestScriptPathPrefersPATH(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "parlay-monitor.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := scriptPath()
	if err != nil {
		t.Fatalf("scriptPath() error = %v", err)
	}
	resolvedGot, _ := filepath.EvalSymlinks(got)
	resolvedStub, _ := filepath.EvalSymlinks(stub)
	if resolvedGot != resolvedStub {
		t.Errorf("scriptPath() = %q, want %q (the PATH stub)", got, stub)
	}
}

func TestScriptPathFallsBackToRepoRelativeLocation(t *testing.T) {
	// No stub on PATH here — this should resolve tools/monitor/parlay-monitor.sh
	// relative to this source file's location in the actual checkout.
	got, err := scriptPath()
	if err != nil {
		t.Fatalf("scriptPath() error = %v", err)
	}
	if filepath.Base(got) != "parlay-monitor.sh" {
		t.Errorf("scriptPath() = %q, want a path ending in parlay-monitor.sh", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("resolved script path does not exist: %v", err)
	}
	_, thisFile, _, _ := runtime.Caller(0)
	wantSuffix := filepath.Join("tools", "monitor", "parlay-monitor.sh")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("scriptPath() = %q, want suffix %q (this test file: %s)", got, wantSuffix, thisFile)
	}
}

func TestNotifyBudgetFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{"unset falls back to 400", "", 400},
		{"non-numeric falls back to 400", "not-a-number", 400},
		{"zero falls back to 400 (JS 0 || 400 semantics)", "0", 400},
		{"valid value is used", "250", 250},
		{"negative value is used (only 0/NaN fall back)", "-5", -5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == "" {
				t.Setenv("PARLAY_NOTIFY_BUDGET", "")
				os.Unsetenv("PARLAY_NOTIFY_BUDGET")
			} else {
				t.Setenv("PARLAY_NOTIFY_BUDGET", tc.env)
			}
			if got := notifyBudgetFromEnv(); got != tc.want {
				t.Errorf("notifyBudgetFromEnv() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPollOnceNetworkErrorSleeps3s(t *testing.T) {
	lastID := ""
	var out strings.Builder
	got := pollOnce("http://127.0.0.1:1", "", &lastID, false, 400, &out)
	if got.sleep != 3*time.Second {
		t.Errorf("sleep = %v, want 3s", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output on network error, got %q", out.String())
	}
}

func TestPollOnceNon2xxSleeps2s(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 2*time.Second {
		t.Errorf("sleep = %v, want 2s", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output on non-2xx, got %q", out.String())
	}
}

func TestPollOnceInvalidJSONSleeps3s(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 3*time.Second {
		t.Errorf("sleep = %v, want 3s", got.sleep)
	}
}

func TestPollOnceTimeoutMessageIsQuietAndImmediate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"timeout": true})
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 0 {
		t.Errorf("sleep = %v, want 0 (no delay on bare timeout)", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output on timeout, got %q", out.String())
	}
}

func TestPollOnceEmitsChatMsgAndAdvancesLastID(t *testing.T) {
	var gotAfter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAfter = r.URL.Query().Get("after")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "msg-1", "role": "user", "text": "hello", "from": "captain",
		})
	}))
	defer srv.Close()

	lastID := "msg-0"
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 0 {
		t.Errorf("sleep = %v, want 0", got.sleep)
	}
	if gotAfter != "msg-0" {
		t.Errorf("server saw after=%q, want msg-0", gotAfter)
	}
	if lastID != "msg-1" {
		t.Errorf("lastID = %q, want msg-1 (advanced)", lastID)
	}
	want := "CHAT_MSG|msg-1|user|hello|from:captain\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestPollOnceSkipsIncompleteMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1"}) // no role/text
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 0 {
		t.Errorf("sleep = %v, want 0", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for an incomplete message, got %q", out.String())
	}
	if lastID != "" {
		t.Errorf("lastID should not advance on an incomplete message, got %q", lastID)
	}
}

func TestPollOnceEmptyTextStillEmits(t *testing.T) {
	// TS: `msg.text != null` — empty string is NOT null, so it still emits.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "user", "text": ""})
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	pollOnce(srv.URL, "", &lastID, false, 400, &out)
	want := "CHAT_MSG|msg-1|user|\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestPollOnceNotifySafeTruncatesLongLines(t *testing.T) {
	longText := strings.Repeat("x", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "user", "text": longText})
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	pollOnce(srv.URL, "", &lastID, true, 100, &out)
	got := out.String()
	if strings.Contains(got, longText) {
		t.Errorf("expected truncation, but full text present: %q", got)
	}
	if !strings.Contains(got, "chars truncated for notification") {
		t.Errorf("expected truncation marker, got %q", got)
	}
	if !strings.HasPrefix(got, "CHAT_MSG|msg-1|user|"+strings.Repeat("x", 100-len("CHAT_MSG|msg-1|user|"))) {
		t.Errorf("truncated line does not start with the expected budget-length prefix: %q", got)
	}
}

func TestPollOnceNotifySafeLeavesShortLinesAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "user", "text": "short"})
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	pollOnce(srv.URL, "", &lastID, true, 400, &out)
	want := "CHAT_MSG|msg-1|user|short\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// stubScript puts a recording parlay-monitor.sh on PATH and returns the path of
// the file it writes its args to.
func stubScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	argLog := filepath.Join(dir, "args.txt")
	stub := filepath.Join(dir, "parlay-monitor.sh")
	body := "#!/bin/sh\necho \"$@\" > " + argLog + "\nexit 0\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argLog
}

// stubRefusingScript installs a parlay-monitor.sh stub that exits
// config.ExitUsage. runRelayMonitor reads that as a deliberate refusal (not a
// crash worth respawning), so a test using it returns on the first iteration
// instead of looping through the whole thrash budget.
func stubRefusingScript(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "parlay-monitor.sh")
	body := fmt.Sprintf("#!/bin/sh\nexit %d\n", config.ExitUsage)
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCmdMonitorReapForwardsToScriptWithoutAnAgent(t *testing.T) {
	argLog := stubScript(t)
	trapExit(t)

	code, ok := testsupport.Capture(func() {
		CmdMonitor([]string{"--reap"})
	})
	if !ok {
		t.Fatal("--reap should run the script and exit, not return")
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	got, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("script was never run: %v", err)
	}
	if strings.TrimSpace(string(got)) != "--reap" {
		t.Errorf("script args = %q, want %q", strings.TrimSpace(string(got)), "--reap")
	}
}

func TestCmdMonitorReapApplyIsForwarded(t *testing.T) {
	argLog := stubScript(t)
	trapExit(t)

	testsupport.Capture(func() {
		CmdMonitor([]string{"--reap", "--apply"})
	})
	got, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("script was never run: %v", err)
	}
	if strings.TrimSpace(string(got)) != "--reap --apply" {
		t.Errorf("script args = %q, want %q", strings.TrimSpace(string(got)), "--reap --apply")
	}
}

func TestPollOnceStopsOn410Gone(t *testing.T) {
	// robots-ycfa: a tombstoned channel answers 410, and retrying re-creates
	// it and polls forever. The Go port used to fold 410 into the generic
	// non-2xx 2s retry, which kept the leak alive on the path bin/parlay
	// actually execs (robots-jkwc).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(`{"gone":true,"error":"channel was unregistered; stop polling"}`))
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "&channel=ghost-z1", &lastID, false, 400, &out)
	if !got.stop {
		t.Error("410 Gone must be terminal for the poll loop")
	}
	if got.sleep != 0 {
		t.Errorf("a terminal answer must not also ask for a retry sleep, got %v", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no CHAT_MSG output on 410, got %q", out.String())
	}
}

func TestPollOnceKeepsRetryingOnAServerError(t *testing.T) {
	// 410 is the ONLY terminal status — a 500 is a transient server problem
	// and must never retire a live agent's monitor.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.stop {
		t.Error("a 500 must not stop the poll loop")
	}
	if got.sleep != 2*time.Second {
		t.Errorf("sleep = %v, want 2s", got.sleep)
	}
}

// task-1t0m: GET /api/chat/poll no longer auto-registers an unrecognized
// channel (that write moved to POST /api/chat/register-agent, called
// explicitly by every real consumer). CmdMonitor's ensureRegistered is the
// explicit-registration step for `parlay monitor --agent <id>` run directly
// (not via `parlay listen`, which already registers on its own). This proves
// it posts the right shape and tolerates a failing/absent server, matching
// the best-effort auto-register it replaces.
func TestEnsureRegisteredPostsRegisterAgent(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	t.Setenv("PARLAY_SERVER", srv.URL)

	ensureRegistered("test-agent")

	if gotPath != "/api/chat/register-agent" {
		t.Errorf("path = %q, want /api/chat/register-agent", gotPath)
	}
	if gotBody["id"] != "test-agent" || gotBody["name"] != "test-agent" {
		t.Errorf("body = %+v, want id/name = test-agent", gotBody)
	}
	if gotBody["color"] == "" || gotBody["color"] == nil {
		t.Errorf("body = %+v, want a non-empty color", gotBody)
	}
}

func TestEnsureRegisteredIsBestEffortOnFailure(t *testing.T) {
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	// Must not panic or die — a register-agent failure degrades gracefully,
	// exactly like the auto-register-on-poll side effect it replaces.
	ensureRegistered("test-agent")
}

// A direct `parlay monitor --agent X` must preflight the relay BEFORE it
// registers, or it is the last remaining way to reach the registered-but-deaf
// state: register-agent posts the tab, the relay script then discovers there is
// no relay and exits, and the agent stays enrolled looking live while receiving
// nothing. `parlay listen` and `parlay claim` both preflight already.
func TestCmdMonitorRelayPathPreflightsBeforeRegistering(t *testing.T) {
	var registered bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat/register-agent" {
			registered = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	t.Setenv("PARLAY_SERVER", srv.URL)
	stubScript(t) // never reached, but keeps a stray script run harmless
	trapExit(t)
	stubPreflight(t, 1)

	code, ok := testsupport.Capture(func() {
		CmdMonitor([]string{"--agent", "deaf-test"})
	})
	if !ok {
		t.Fatal("a failed relay preflight must die, not fall through to the stream")
	}
	if code != config.ExitRuntime {
		t.Errorf("exit code = %d, want %d (ExitRuntime)", code, config.ExitRuntime)
	}
	if registered {
		t.Error("register-agent was POSTed despite a failed relay preflight — the agent is left enrolled and deaf")
	}
}

// A relay that IS reachable must still register and stream: the preflight is a
// gate on the broken case, not a new reason to refuse a working setup.
func TestCmdMonitorRelayPathRegistersWhenPreflightPasses(t *testing.T) {
	var registered bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat/register-agent" {
			registered = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	t.Setenv("PARLAY_SERVER", srv.URL)
	// A stub that exits with ExitUsage: runRelayMonitor treats that as a
	// deliberate refusal and does not respawn, so the test ends on the first
	// iteration instead of burning the whole thrash budget.
	stubRefusingScript(t)
	trapExit(t)
	stubPreflight(t, 0)

	testsupport.Capture(func() {
		CmdMonitor([]string{"--agent", "ok-test"})
	})
	if !registered {
		t.Error("register-agent was never POSTed on a healthy relay path")
	}
}

// `--legacy-poll` is the no-relay escape hatch, so it must not preflight —
// otherwise the documented fresh-clone path fails for the very reason it exists
// to avoid needing a relay. The server answers 410 so the poll loop's one
// terminal status ends it immediately: a bare `for {}` here would leak a
// never-stopped goroutine past the end of the test.
func TestCmdMonitorLegacyPollNeverPreflights(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()
	t.Setenv("PARLAY_SERVER", srv.URL)
	trapExit(t)

	preflightRan := false
	orig := preflightRelay
	preflightRelay = func(agent string) int {
		preflightRan = true
		return 1
	}
	t.Cleanup(func() { preflightRelay = orig })

	testsupport.Capture(func() {
		CmdMonitor([]string{"--agent", "legacy-test", "--legacy-poll"})
	})
	if preflightRan {
		t.Error("--legacy-poll ran the relay preflight; it must work with no relay installed")
	}
}

// The listen→monitor handoff must not re-register: `parlay listen`
// registers, then hands off in-process to CmdMonitor, whose own
// ensureRegistered would otherwise POST register-agent a second time for the
// same agent seconds later. The claim is single-use so a later DIRECT
// `parlay monitor` for the same id still registers.
func TestClaimHandoffRegistrationConsumesOnce(t *testing.T) {
	orig := handoffRegisteredAgent
	t.Cleanup(func() { handoffRegisteredAgent = orig })

	handoffRegisteredAgent = "brain-dev"
	if !claimHandoffRegistration("brain-dev") {
		t.Error("claimHandoffRegistration(brain-dev) = false, want true on first claim")
	}
	if claimHandoffRegistration("brain-dev") {
		t.Error("claimHandoffRegistration(brain-dev) = true twice, want single-use")
	}
}

func TestClaimHandoffRegistrationRejectsOtherAgentsAndEmpty(t *testing.T) {
	orig := handoffRegisteredAgent
	t.Cleanup(func() { handoffRegisteredAgent = orig })

	handoffRegisteredAgent = "brain-dev"
	if claimHandoffRegistration("mayor") {
		t.Error("claimHandoffRegistration(mayor) = true, want false for a different agent")
	}
	if claimHandoffRegistration("") {
		t.Error("claimHandoffRegistration(\"\") = true, want false for an empty agent")
	}
	// A rejected claim must not consume the pending one.
	if !claimHandoffRegistration("brain-dev") {
		t.Error("a rejected claim consumed the pending handoff for brain-dev")
	}
}
