package monitor

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// ── unit: reading a finished run ─────────────────────────────────────────────

func TestClassifyRunNamesTheSignalExitCodeCannot(t *testing.T) {
	// The whole point of classifyRun: ExitCode() answers -1 for a signalled
	// child and throws the signal away, which is why the robots-gv6t death
	// could not report what killed it.
	cmd := exec.Command("bash", "-c", "kill -TERM $$")
	runErr := cmd.Run()
	if runErr == nil {
		t.Fatal("expected the child to die by signal")
	}

	code, signal := classifyRun(runErr)
	if signal != syscall.SIGTERM {
		t.Errorf("signal = %v, want SIGTERM", signal)
	}
	if code != -1 {
		t.Errorf("code = %d, want -1 (what ExitError reports for a signalled child)", code)
	}
	if got, want := describeRun(code, signal), "killed by terminated, reported as exit 143"; got != want {
		t.Errorf("describeRun = %q, want %q", got, want)
	}
}

func TestClassifyRunOrdinaryExits(t *testing.T) {
	if code, signal := classifyRun(nil); code != 0 || signal != 0 {
		t.Errorf("classifyRun(nil) = (%d, %v), want (0, 0)", code, signal)
	}

	runErr := exec.Command("bash", "-c", "exit 7").Run()
	code, signal := classifyRun(runErr)
	if code != 7 || signal != 0 {
		t.Errorf("classifyRun(exit 7) = (%d, %v), want (7, 0)", code, signal)
	}
	if got, want := describeRun(code, signal), "exit 7"; got != want {
		t.Errorf("describeRun = %q, want %q", got, want)
	}
}

func TestShouldRestartMonitorOnlyRespectsDeliberateRefusals(t *testing.T) {
	cases := []struct {
		code int
		want bool
		why  string
	}{
		{config.ExitUsage, false, "a bad invocation refuses identically forever"},
		{config.ExitRuntime, false, "the script already reported why and gave up"},
		{0, true, "the stream is not supposed to end on its own"},
		{-1, true, "a signalled death is exactly what supervision is for"},
		{143, true, "an unexplained status is unexplained"},
	}
	for _, tc := range cases {
		if got := shouldRestartMonitor(tc.code); got != tc.want {
			t.Errorf("shouldRestartMonitor(%d) = %v, want %v — %s", tc.code, got, tc.want, tc.why)
		}
	}
}

// ── the supervision loop ─────────────────────────────────────────────────────

// stubMonitorScript puts a fake parlay-monitor.sh first on PATH. It records one
// line per invocation in the returned runlog, dies by SIGTERM (an unexplained
// death, the robots-gv6t shape) until it has run maxRuns times, and exits
// EXIT_USAGE on that last run so a test can terminate a loop that is otherwise
// deliberately infinite.
func stubMonitorScript(t *testing.T, maxRuns int) (runlog string) {
	t.Helper()
	dir := t.TempDir()
	runlog = filepath.Join(dir, "runs")
	script := "#!/bin/bash\n" +
		"echo run >>\"$MONITOR_STUB_RUNLOG\"\n" +
		"if [ \"$(wc -l <\"$MONITOR_STUB_RUNLOG\" | tr -d ' ')\" -ge \"$MONITOR_STUB_MAX\" ]; then exit 2; fi\n" +
		"kill -TERM $$\n" +
		"sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "parlay-monitor.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MONITOR_STUB_RUNLOG", runlog)
	t.Setenv("MONITOR_STUB_MAX", "999999")
	if maxRuns > 0 {
		t.Setenv("MONITOR_STUB_MAX", strconv.Itoa(maxRuns))
	}
	return runlog
}

func countRuns(t *testing.T, runlog string) int {
	t.Helper()
	data, err := os.ReadFile(runlog)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// fakeClock makes elapsed time deterministic: every now() call advances by
// step, so a run's measured uptime is exactly step.
func fakeClock(t *testing.T, step time.Duration) {
	t.Helper()
	origNow, origSleep := now, sleep
	base := time.Unix(0, 0)
	var mu sync.Mutex
	now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		base = base.Add(step)
		return base
	}
	sleep = func(time.Duration) {} // never wait out the restart delay in a test
	t.Cleanup(func() { now, sleep = origNow, origSleep })
}

// captureStdout swaps os.Stdout for a pipe for the duration of fn. The
// supervised child inherits it too, which is the point: the MONITOR| lines
// have to reach the same stream a harness Monitor tool reads.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	// The drained text comes back over a channel rather than out of a shared
	// buffer, matching the six other capture helpers in this module
	// (internal/commands, internal/httpc, internal/identity,
	// internal/robotswatch, tools/parlay-bin). This one was the outlier: it
	// had the copying goroutine append to a strings.Builder that the test
	// goroutine then read.
	//
	// That is a data race, and -race says so — but the reason it is worth
	// fixing rather than silencing is that it is also WRONG. Go evaluates a
	// return expression BEFORE running deferred functions, so `return
	// out.String()` read the builder while the drain was still deferred and
	// therefore had not happened yet. The value returned was whatever had
	// landed by that instant. Every assertion in this file about respawn
	// notices and MONITOR| lines was reading a possibly-truncated capture, and
	// a truncated capture fails open: `strings.Count(out, "…respawned|") != 2`
	// is a flake, but `strings.Contains(out, "…")` on absent text just quietly
	// reports the line was missing.
	//
	// Receiving from `done` is the synchronization point AND the sequencing
	// point: it cannot complete until io.Copy has returned, which cannot
	// happen until w is closed and the pipe is drained to EOF.
	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	// Restoring os.Stdout stays deferred so a t.Fatal inside fn cannot leave
	// the global pointing at a closed pipe for the rest of the package's
	// tests. The drain deliberately does NOT: it has to precede the read.
	defer func() { os.Stdout = orig }()

	fn()

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// recordingRelay stands in for Pulse and collects the bodies posted to
// /api/chat/reply — where the stream-down retraction lands.
func recordingRelay(t *testing.T) *[]map[string]string {
	t.Helper()
	var mu sync.Mutex
	posts := []map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		posts = append(posts, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("PARLAY_SERVER", srv.URL)
	return &posts
}

func TestRunRelayMonitorRestartsAnUnexplainedDeath(t *testing.T) {
	// robots-gv6t: the stream used to be exactly as durable as one bash
	// process. A signalled death must now be respawned, not propagated.
	trapExit(t)
	recordingRelay(t)
	runlog := stubMonitorScript(t, 3)
	fakeClock(t, 0) // every run is instant: thrash, but under the give-up bound

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = testsupport.Capture(func() { runRelayMonitor("mc-test", false) })
	})

	if !exited {
		t.Fatal("supervision should end via Exit once the script refuses on purpose")
	}
	if code != config.ExitUsage {
		t.Errorf("exit code = %d, want %d (the script's own deliberate refusal, passed through)", code, config.ExitUsage)
	}
	if got := countRuns(t, runlog); got != 3 {
		t.Errorf("script ran %d times, want 3 (two respawns after the signalled deaths)", got)
	}
	if got := strings.Count(out, "MONITOR|respawned|"); got != 2 {
		t.Errorf("stdout carried %d respawn notices, want 2 — stdout is the only stream the harness turns into an agent-visible event\n%s", got, out)
	}
	if !strings.Contains(out, "killed by terminated") {
		t.Errorf("the notice must name what killed the stream, got:\n%s", out)
	}
}

func TestRunRelayMonitorGivesUpLoudlyAfterRepeatedFastDeaths(t *testing.T) {
	trapExit(t)
	posts := recordingRelay(t)
	runlog := stubMonitorScript(t, 0) // never refuses; only supervision can stop it
	fakeClock(t, 0)

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = testsupport.Capture(func() { runRelayMonitor("mc-test", false) })
	})

	if !exited || code != config.ExitRuntime {
		t.Fatalf("give-up exit = (%d, %v), want (%d, true)", code, exited, config.ExitRuntime)
	}
	if got, want := countRuns(t, runlog), monitorMaxRestarts+1; got != want {
		t.Errorf("script ran %d times, want %d (restarts are bounded, not infinite)", got, want)
	}
	if !strings.Contains(out, "MONITOR|down|") || !strings.Contains(out, "DEAF") {
		t.Errorf("giving up must say so on stdout, in the words an agent needs:\n%s", out)
	}

	// And the "listening — monitor armed" announce has to be retracted on the
	// channel, because the registry has no listening flag to clear.
	if len(*posts) != 1 {
		t.Fatalf("posted %d stream-down notices, want 1", len(*posts))
	}
	got := (*posts)[0]
	if got["agent"] != "mc-test" || !strings.Contains(got["text"], "monitor DOWN") {
		t.Errorf("stream-down notice = %#v, want a 'monitor DOWN' reply for mc-test", got)
	}
}

func TestRunRelayMonitorDoesNotRestartADeliberateRefusal(t *testing.T) {
	// EXIT_USAGE/EXIT_RUNTIME are the script's self-explained refusals: a bad
	// invocation or an unreachable relay reproduces identically, so retrying
	// only spams.
	trapExit(t)
	posts := recordingRelay(t)
	runlog := stubMonitorScript(t, 1)
	fakeClock(t, 0)

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = testsupport.Capture(func() { runRelayMonitor("mc-test", false) })
	})

	if !exited || code != config.ExitUsage {
		t.Fatalf("exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
	if got := countRuns(t, runlog); got != 1 {
		t.Errorf("script ran %d times, want 1", got)
	}
	if strings.Contains(out, "MONITOR|respawned|") {
		t.Errorf("a deliberate refusal must not be respawned:\n%s", out)
	}
	if len(*posts) != 1 {
		t.Errorf("posted %d stream-down notices, want 1 — an exit after the announce still leaves the agent deaf", len(*posts))
	}
}

func TestRunRelayMonitorHealthyRunResetsTheThrashCounter(t *testing.T) {
	// A stream that lived a long time and then died is not thrash. Without the
	// reset, an agent whose channel drops once an hour would exhaust the
	// restart budget over a long session and go silently deaf anyway.
	trapExit(t)
	recordingRelay(t)
	longEnough := monitorMinUptime + time.Second
	runs := monitorMaxRestarts + 3 // more deaths than the give-up bound allows
	runlog := stubMonitorScript(t, runs)
	fakeClock(t, longEnough)

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = testsupport.Capture(func() { runRelayMonitor("mc-test", false) })
	})

	if !exited || code != config.ExitUsage {
		t.Fatalf("exit = (%d, %v), want (%d, true) — supervision gave up instead of restarting", code, exited, config.ExitUsage)
	}
	if got := countRuns(t, runlog); got != runs {
		t.Errorf("script ran %d times, want %d", got, runs)
	}
	if strings.Contains(out, "MONITOR|down|") {
		t.Errorf("long-lived runs must never accumulate into a give-up:\n%s", out)
	}
}

func TestRunRelayMonitorForwardsNotifySafe(t *testing.T) {
	trapExit(t)
	recordingRelay(t)
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/bash\nprintf '%s\\n' \"$@\" >\"$MONITOR_STUB_ARGS\"\nexit 2\n"
	if err := os.WriteFile(filepath.Join(dir, "parlay-monitor.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MONITOR_STUB_ARGS", argsFile)
	fakeClock(t, 0)

	captureStdout(t, func() {
		_, _ = testsupport.Capture(func() { runRelayMonitor("mc-test", true) })
	})

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(data))
	want := []string{"--agent", "mc-test", "--notify-safe"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("script args = %v, want %v", got, want)
	}
}

func TestAnnounceStreamDownSurvivesAnUnreachableServer(t *testing.T) {
	// The stream is already dead; a server that cannot be reached must not
	// turn that into a second failure.
	trapExit(t)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	if _, exited := testsupport.Capture(func() { announceStreamDown("mc-test", "exit 9") }); exited {
		t.Fatal("announceStreamDown must be best-effort, not fatal")
	}
}
