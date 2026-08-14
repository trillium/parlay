package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// goldenPath is the shared wire-shape fixture, owned by the server
// (packages/go-server/testdata). This CLI decoding it — and rendering it
// without inventing or losing a field — is the CLI half of the "both
// surfaces read the same state" claim; the panel test reads the same file.
const goldenPath = "../../../../packages/go-server/testdata/live-commands.golden.json"

func loadGolden(t *testing.T) wire.CommandsResponse {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(goldenPath))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	var resp wire.CommandsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("golden does not fit wire.CommandsResponse: %v", err)
	}
	return resp
}

func TestGoldenFixtureDecodesIntoTheCLIWireTypes(t *testing.T) {
	resp := loadGolden(t)
	if !resp.OK || resp.Running != 2 || len(resp.Commands) != 5 || resp.StaleAfterMs != 90000 {
		t.Fatalf("decoded envelope = %+v, want ok/2 running/5 records/90000ms", resp)
	}

	// Every optional field must survive the round trip, or this CLI is
	// quietly dropping something the panel can see.
	rec := resp.Commands[0]
	if rec.ID != "cmd-5" || rec.Verb != "listen" || rec.Agent != "crew-1" || rec.Channel != "c1" ||
		rec.PID != 4242 || rec.State != "running" || rec.DurationMs != 18000 ||
		strings.Join(rec.Flags, " ") != "--agent --caps" {
		t.Errorf("commands[0] = %+v, want the fixture's running listen record intact", rec)
	}
	done := resp.Commands[3]
	if done.ExitCode == nil || *done.ExitCode != 0 || done.Outcome != "ok" || done.EndedAt == "" {
		t.Errorf("commands[3] = %+v, want exitCode 0 / outcome ok / an endedAt", done)
	}
}

func TestDefaultViewShowsOnlyRunningCommands(t *testing.T) {
	resp := loadGolden(t)
	shown := filterCommands(resp.Commands, commandFilter{})
	if len(shown) != 2 {
		t.Fatalf("shown = %d records, want the 2 running ones", len(shown))
	}
	out := renderCommandTable(resp, shown, commandFilter{})
	for _, want := range []string{"2 running (5 tracked)", "STATE", "listen", "crew-1", "4242", "--agent --caps"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no-heartbeat") {
		t.Errorf("default view leaked a terminal record; got:\n%s", out)
	}
}

func TestAllViewShowsTerminalRecordsAndHowTheyEnded(t *testing.T) {
	resp := loadGolden(t)
	f := commandFilter{all: true}
	out := renderCommandTable(resp, filterCommands(resp.Commands, f), f)
	for _, want := range []string{"finished", "exit 0 ok", "failed", "exit 3 error", "expired", "no-heartbeat"} {
		if !strings.Contains(out, want) {
			t.Errorf("--all table missing %q; got:\n%s", want, out)
		}
	}
}

func TestFiltersNarrowByAgentAndVerb(t *testing.T) {
	resp := loadGolden(t)
	byAgent := filterCommands(resp.Commands, commandFilter{all: true, agent: "crew-1"})
	if len(byAgent) != 2 {
		t.Errorf("--agent crew-1 = %d records, want 2", len(byAgent))
	}
	byVerb := filterCommands(resp.Commands, commandFilter{all: true, verb: "merge-gate"})
	if len(byVerb) != 1 || byVerb[0].ID != "cmd-3" {
		t.Errorf("--verb merge-gate = %+v, want just cmd-3", byVerb)
	}
	both := filterCommands(resp.Commands, commandFilter{agent: "crew-2"})
	if len(both) != 0 {
		t.Errorf("crew-2 has no RUNNING commands, got %+v", both)
	}
}

// TestEmptyViewSaysWhatItCannotSee is the "honest about its coverage"
// requirement: an empty list must never read as "nothing is running on this
// machine", because this registry only sees invocations that report
// themselves.
func TestEmptyViewSaysWhatItCannotSee(t *testing.T) {
	out := renderCommandTable(wire.CommandsResponse{OK: true}, nil, commandFilter{})
	if !strings.Contains(out, "No commands are running.") {
		t.Errorf("missing the empty-state line; got:\n%s", out)
	}
	if !strings.Contains(out, "docs/live-commands.md") || !strings.Contains(out, "only the Go CLI reports itself") {
		t.Errorf("empty state must name its coverage limit; got:\n%s", out)
	}
}

func TestCommandAgeFormatting(t *testing.T) {
	cases := map[int64]string{
		0:        "0.0s",
		1500:     "1.5s",
		59900:    "59.9s",
		60000:    "1m00s",
		132000:   "2m12s",
		3600000:  "1h00m",
		5400000:  "1h30m",
		-1000000: "0.0s", // a clock skew must not render a negative age
	}
	for ms, want := range cases {
		if got := commandAge(ms); got != want {
			t.Errorf("commandAge(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestCommandDetailNeverInventsAValue(t *testing.T) {
	running := wire.CommandInvocation{State: "running", Flags: []string{"--json"}}
	if got := commandDetail(running); got != "--json" {
		t.Errorf("running detail = %q, want the flag name", got)
	}
	if got := commandDetail(wire.CommandInvocation{State: "running"}); got != "-" {
		t.Errorf("flagless running detail = %q, want %q", got, "-")
	}
	if got := commandDetail(wire.CommandInvocation{State: "expired", Outcome: "no-heartbeat"}); got != "no-heartbeat" {
		t.Errorf("expired detail = %q", got)
	}
}

// --- transport behavior ----------------------------------------------------

// withServer points PARLAY_SERVER at a test server for one test.
func withServer(t *testing.T, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("PARLAY_SERVER", srv.URL)
	testsupport.TempStateHome(t)
}

// TestOlderServerDegradesQuietly pins constraint 2 of the brief: a server
// without this registry must not turn `parlay commands` into an error. It is
// reported as unsupported and the process exits 0.
func TestOlderServerDegradesQuietly(t *testing.T) {
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	resp, supported := fetchCommands()
	if supported {
		t.Fatalf("404 must report unsupported, got supported with %+v", resp)
	}
}

func TestUnreachableServerIsFatalLikeEveryOtherReadVerb(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1") // nothing listens here

	prev := httpc.Exit
	httpc.Exit = testsupport.RecordingExit()
	t.Cleanup(func() { httpc.Exit = prev })

	code, exited := testsupport.Capture(func() { fetchCommands() })
	if !exited || code != 1 {
		t.Errorf("exit = %d exited=%v, want a runtime-error exit 1", code, exited)
	}
}

func TestFetchDecodesALiveServerResponse(t *testing.T) {
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/commands" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Clean(goldenPath))
	}))
	resp, supported := fetchCommands()
	if !supported || len(resp.Commands) != 5 {
		t.Fatalf("fetch = %+v supported=%v, want the fixture's 5 records", resp, supported)
	}
}

// --- --watch ---------------------------------------------------------------

func TestSSEParserReadsNamedFramesAndSkipsKeepAlives(t *testing.T) {
	stream := ": keep-alive\n\n" +
		"event: commands\ndata: [{\"id\":\"c-1\",\"verb\":\"listen\",\"state\":\"running\"}]\n\n" +
		": keep-alive\n\n" +
		"event: command_update\ndata: {\"id\":\"c-1\",\"verb\":\"listen\",\"state\":\"finished\"}\n\n"

	var names []string
	for ev := range sseEvents(strings.NewReader(stream)) {
		names = append(names, ev.name)
		if !json.Valid(ev.data) {
			t.Errorf("event %s carried invalid JSON: %s", ev.name, ev.data)
		}
	}
	if strings.Join(names, ",") != "commands,command_update" {
		t.Errorf("parsed events = %v, want the two named frames only", names)
	}
}

// eventStream serves one canned SSE body and then closes the connection,
// which is what a server restart or a dropped link looks like from the CLI.
func eventStream(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	})
}

// watchOnce runs follow mode against a canned stream and returns the printed
// lines plus the exit the stream's end produced. The follow log is a stdout
// contract: a notice only an operator's stderr sees is invisible to the
// harnesses that read these streams (robots-gv6t).
func watchOnce(t *testing.T, stream string, f commandFilter) (lines []string, code int, exited bool) {
	t.Helper()
	withServer(t, eventStream(stream))

	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { watchCommands(f, false) })
	})
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, code, exited
}

// followStream exercises every case the default follow mode has to tell apart:
// a record already running when the watch began, one that starts and ends
// during it, one that was already over, one belonging to another agent, and a
// retention drop.
const followStream = "event: commands\n" +
	"data: [{\"id\":\"pre\",\"verb\":\"listen\",\"agent\":\"crew-1\",\"state\":\"running\",\"durationMs\":1000}]\n\n" +
	"event: command_update\n" +
	"data: {\"id\":\"new\",\"verb\":\"send\",\"agent\":\"crew-1\",\"state\":\"running\",\"durationMs\":10,\"flags\":[\"--agent\"]}\n\n" +
	"event: command_update\n" +
	"data: {\"id\":\"new\",\"verb\":\"send\",\"agent\":\"crew-1\",\"state\":\"finished\",\"exitCode\":0,\"outcome\":\"ok\",\"durationMs\":50}\n\n" +
	"event: command_update\n" +
	"data: {\"id\":\"pre\",\"verb\":\"listen\",\"agent\":\"crew-1\",\"state\":\"expired\",\"outcome\":\"no-heartbeat\",\"durationMs\":90000}\n\n" +
	"event: command_update\n" +
	"data: {\"id\":\"ghost\",\"verb\":\"stats\",\"agent\":\"crew-1\",\"state\":\"finished\",\"exitCode\":0,\"outcome\":\"ok\",\"durationMs\":20}\n\n" +
	"event: command_update\n" +
	"data: {\"id\":\"other\",\"verb\":\"agents\",\"agent\":\"crew-9\",\"state\":\"running\",\"durationMs\":5}\n\n" +
	"event: command_update\n" +
	"data: {\"id\":\"new\",\"verb\":\"send\",\"agent\":\"crew-1\",\"state\":\"dropped\",\"durationMs\":60000}\n\n"

// TestWatchLineMarksStartsAndEnds drives the DEFAULT follow mode (no --all),
// the path an operator actually takes. An end event is how a record leaves the
// running set, so suppressing it would leave the log asserting that a finished
// command is still running.
func TestWatchLineMarksStartsAndEnds(t *testing.T) {
	lines, _, _ := watchOnce(t, followStream, commandFilter{agent: "crew-1"})

	var starts, ends []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+ "):
			starts = append(starts, line)
		case strings.HasPrefix(line, "- "):
			ends = append(ends, line)
		}
	}

	if len(starts) != 1 || !strings.Contains(starts[0], "send") || !strings.Contains(starts[0], "--agent") {
		t.Errorf("start lines = %v, want one `+` line for the send that started here", starts)
	}
	if len(ends) != 2 {
		t.Fatalf("end lines = %v, want two: the send that finished and the listen that expired", ends)
	}
	if !strings.Contains(ends[0], "finished") || !strings.Contains(ends[0], "exit 0 ok") {
		t.Errorf("first end line = %q, want the finished send with how it ended", ends[0])
	}
	if !strings.Contains(ends[1], "expired") || !strings.Contains(ends[1], "no-heartbeat") {
		t.Errorf("second end line = %q, want the expired listen (it was running when the watch began)", ends[1])
	}

	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "stats") {
		t.Errorf("printed an end for a command that started AND ended before the watch began:\n%s", joined)
	}
	if strings.Contains(joined, "crew-9") || strings.Contains(joined, "agents") {
		t.Errorf("printed a record that never matched --agent crew-1:\n%s", joined)
	}
	if strings.Contains(joined, "dropped") {
		t.Errorf("a retention drop is internal bookkeeping, not an operator-visible end line:\n%s", joined)
	}
}

// --all keeps its meaning exactly: every record matching --agent/--verb,
// whatever its state and whether or not this session saw it running.
func TestWatchAllShowsEveryRecordIncludingOnesItNeverSawRunning(t *testing.T) {
	lines, _, _ := watchOnce(t, followStream, commandFilter{all: true, agent: "crew-1"})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "stats") {
		t.Errorf("--all dropped a terminal record it never saw start:\n%s", joined)
	}
	if strings.Contains(joined, "crew-9") {
		t.Errorf("--all ignored the --agent filter:\n%s", joined)
	}
	if strings.Contains(joined, "dropped") {
		t.Errorf("--all printed a retention drop as an end line:\n%s", joined)
	}
}

// TestWatchSaysSoWhenTheStreamEnds: follow mode that simply stops is
// indistinguishable from an idle fleet, to an operator and to a script reading
// the exit code alike (robots-dcag / robots-gv6t).
func TestWatchSaysSoWhenTheStreamEnds(t *testing.T) {
	lines, code, exited := watchOnce(t, "event: commands\ndata: []\n\n", commandFilter{})

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "event stream ended") {
		t.Errorf("a closed stream printed no notice on stdout; got:\n%s", joined)
	}
	if !exited || code == 0 {
		t.Errorf("exit = %d exited=%v, want a non-zero give-up", code, exited)
	}
}
