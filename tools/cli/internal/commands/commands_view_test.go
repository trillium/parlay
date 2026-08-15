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
	if rec.ID != "cmd-5" || rec.Verb != "listen" || rec.Agent != "crew-1" ||
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
	// The summary describes the rows printed under it — 2 of the fixture's 5
	// records — and names the fleet-wide totals separately, because the two
	// numbers answer different questions and the reader cannot check the second
	// one against anything on screen. (This assertion previously read
	// "2 running (5 tracked)": one line mixing a filtered count with an
	// unfiltered one, which is the reading this test now forbids.)
	for _, want := range []string{"2 running of 2 shown", "5 tracked fleet-wide", "STATE", "listen", "crew-1", "4242", "--agent --caps"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no-heartbeat") {
		t.Errorf("default view leaked a terminal record; got:\n%s", out)
	}
}

// The unfiltered case says it once: with nothing narrowed there is no second
// number to disambiguate, and repeating it would be noise on every run.
func TestUnnarrowedTableKeepsTheSingleCountLine(t *testing.T) {
	resp := loadGolden(t)
	f := commandFilter{all: true}
	out := renderCommandTable(resp, filterCommands(resp.Commands, f), f)
	if !strings.Contains(out, "2 running (5 tracked)") {
		t.Errorf("unfiltered table lost its one-line summary; got:\n%s", out)
	}
	if strings.Contains(out, "fleet-wide") {
		t.Errorf("nothing was narrowed, so nothing should be labelled fleet-wide; got:\n%s", out)
	}
}

// An empty result under a filter is a fact about the FILTER. Saying "nothing is
// running" when 2 commands are is the reading this forbids.
func TestEmptyFilteredViewBlamesTheFilterNotTheFleet(t *testing.T) {
	resp := loadGolden(t)
	f := commandFilter{agent: "nobody"}
	out := renderCommandTable(resp, filterCommands(resp.Commands, f), f)
	if !strings.Contains(out, "No commands match this filter") {
		t.Errorf("empty filtered view must blame the filter; got:\n%s", out)
	}
	if !strings.Contains(out, "2 running") || !strings.Contains(out, "5 tracked fleet-wide") {
		t.Errorf("empty filtered view must still carry the fleet-wide totals; got:\n%s", out)
	}
}

// Both renderers of one server state must agree on what they are counting:
// `running` describes the records shown, and the fleet-wide totals are carried
// alongside rather than substituted for it.
func TestJSONAndTableAgreeOnTheFilteredCount(t *testing.T) {
	golden, err := os.ReadFile(filepath.Clean(goldenPath))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	withServer(t, commandsAndEvents(string(golden), ""))

	out := captureStdout(t, func() { Commands([]string{"--json", "--agent", "crew-1"}) })
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, out)
	}

	resp := loadGolden(t)
	f := commandFilter{agent: "crew-1"}
	shown := filterCommands(resp.Commands, f)
	table := renderCommandTable(resp, shown, f)

	if envelope["running"] != float64(len(shown)) || envelope["shown"] != float64(len(shown)) {
		t.Errorf("--json running/shown = %v/%v, want the filtered count %d",
			envelope["running"], envelope["shown"], len(shown))
	}
	if envelope["totalRunning"] != float64(2) || envelope["totalTracked"] != float64(5) {
		t.Errorf("--json totals = %v/%v, want the unfiltered 2/5",
			envelope["totalRunning"], envelope["totalTracked"])
	}
	if !strings.Contains(table, "1 running of 1 shown") || !strings.Contains(table, "5 tracked fleet-wide") {
		t.Errorf("table disagrees with --json about what it is counting; got:\n%s", table)
	}
}

// The totals ship on every --json run, filtered or not: a machine consumer
// should not have to discover that a key exists only under some arguments.
func TestJSONTotalsArePresentEvenUnfiltered(t *testing.T) {
	golden, err := os.ReadFile(filepath.Clean(goldenPath))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	withServer(t, commandsAndEvents(string(golden), ""))

	out := captureStdout(t, func() { Commands([]string{"--json", "--all"}) })
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, out)
	}
	for _, key := range []string{"shown", "totalRunning", "totalTracked"} {
		if _, ok := envelope[key]; !ok {
			t.Errorf("unfiltered --json omitted %q: %s", key, out)
		}
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
	events, streamErr := sseEvents(strings.NewReader(stream))
	for ev := range events {
		names = append(names, ev.name)
		if !json.Valid(ev.data) {
			t.Errorf("event %s carried invalid JSON: %s", ev.name, ev.data)
		}
	}
	if strings.Join(names, ",") != "commands,command_update" {
		t.Errorf("parsed events = %v, want the two named frames only", names)
	}
	if err := streamErr(); err != nil {
		t.Errorf("a cleanly-consumed stream reported %v, want no error", err)
	}
}

// A stream that ended because a frame could not be read is a different fact
// from one the server closed, and discarding the reader's error made the two
// indistinguishable.
func TestSSEParserReportsWhyItStopped(t *testing.T) {
	defer withSSELineCap(t, 64)()

	stream := "event: commands\ndata: [" + strings.Repeat("0", 500) + "]\n\n"
	events, streamErr := sseEvents(strings.NewReader(stream))
	for range events { //nolint:revive // draining is the point
	}
	if streamErr() == nil {
		t.Error("an over-long frame ended the stream silently")
	}
}

// withSSELineCap shrinks the frame cap so the over-long path is reachable
// without allocating megabytes. Production keeps the real 4 MiB.
func withSSELineCap(t *testing.T, n int) func() {
	t.Helper()
	prev := sseMaxLineBytes
	sseMaxLineBytes = n
	return func() { sseMaxLineBytes = prev }
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
	return splitLines(out), code, exited
}

func splitLines(out string) (lines []string) {
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
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

// TestWatchOpensTheStreamAtTheNewestMessage pins the cursor: without an
// `after`, the connect burst replays the server's whole retained history in one
// frame, which is exactly the frame the line cap then has to refuse. The
// sentinel has to be a REAL id — the server sends everything for an unknown one
// too, so a made-up value would look right and change nothing.
func TestWatchOpensTheStreamAtTheNewestMessage(t *testing.T) {
	var gotAfter string
	var sawEvents bool
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat/history":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"id":"m-42","role":"user","ts":"2026-01-01T00:00:00Z","text":"hi"}]`)
		case "/api/chat/events":
			sawEvents = true
			gotAfter = r.URL.Query().Get("after")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: commands\ndata: []\n\n")
		default:
			http.NotFound(w, r)
		}
	}))

	captureStderr(t, func() {
		captureStdout(t, func() {
			withExitTrap(t, func() { watchCommands(commandFilter{}, false) })
		})
	})

	if !sawEvents {
		t.Fatal("the watch never opened the event stream")
	}
	if gotAfter != "m-42" {
		t.Errorf("events request carried after=%q, want the newest message id m-42", gotAfter)
	}
}

// The cursor probe is an optimization, and an optimization must never be able
// to fail the thing it optimizes (robots-dcag). Every broken answer below has
// to leave the watch working, just without a cursor.
func TestWatchConnectsAnywayWhenTheCursorProbeFails(t *testing.T) {
	probes := map[string]func(w http.ResponseWriter){
		"non-200":     func(w http.ResponseWriter) { http.Error(w, "nope", http.StatusInternalServerError) },
		"garbage":     func(w http.ResponseWriter) { _, _ = io.WriteString(w, "not json at all") },
		"empty array": func(w http.ResponseWriter) { _, _ = io.WriteString(w, `[]`) },
		"no id":       func(w http.ResponseWriter) { _, _ = io.WriteString(w, `[{"role":"user"}]`) },
	}
	for name, probe := range probes {
		t.Run(name, func(t *testing.T) {
			var gotAfter, rawQuery string
			var sawEvents bool
			withServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/chat/history":
					probe(w)
				case "/api/chat/events":
					sawEvents = true
					gotAfter, rawQuery = r.URL.Query().Get("after"), r.URL.RawQuery
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: commands\ndata: []\n\n")
				default:
					http.NotFound(w, r)
				}
			}))

			var code int
			var exited bool
			captureStderr(t, func() {
				captureStdout(t, func() {
					code, exited = withExitTrap(t, func() { watchCommands(commandFilter{}, false) })
				})
			})

			if !sawEvents {
				t.Fatalf("a %s cursor probe stopped the watch from connecting", name)
			}
			if gotAfter != "" {
				t.Errorf("a %s probe still produced after=%q (query %q)", name, gotAfter, rawQuery)
			}
			// The only exit here is the stream's own end, not the probe's.
			if !exited || code == 0 {
				t.Errorf("exit = %d exited=%v, want the ordinary stream-ended give-up", code, exited)
			}
		})
	}
}

// A frame too large to read ends the stream, and stdout's contract does not
// change for it: the operator still gets the one documented line, a script
// still gets the one documented object, and WHY goes to stderr where it cannot
// break either.
func TestOverLongFrameKeepsStdoutsContractAndNamesTheCauseOnStderr(t *testing.T) {
	huge := "event: commands\ndata: [" + strings.Repeat("0", 4096) + "]\n\n"

	for _, asJSON := range []bool{false, true} {
		defer withSSELineCap(t, 128)()
		withServer(t, eventStream(huge))

		var code int
		var exited bool
		var stdout string
		stderr := captureStderr(t, func() {
			stdout = captureStdout(t, func() {
				code, exited = withExitTrap(t, func() { watchCommands(commandFilter{}, asJSON) })
			})
		})

		lines := splitLines(stdout)
		if len(lines) != 1 {
			t.Fatalf("asJSON=%v: stdout = %d line(s), want exactly the stream-end line:\n%s", asJSON, len(lines), stdout)
		}
		if asJSON {
			var decoded map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
				t.Fatalf("asJSON: stdout line does not parse (%v): %q", err, lines[0])
			}
			if decoded["ok"] != false || decoded["error"] != "stream-ended" || len(decoded) != 2 {
				t.Errorf("asJSON: payload = %v, want exactly {ok:false, error:\"stream-ended\"}", decoded)
			}
		} else if lines[0] != streamEndedNotice {
			t.Errorf("stdout line = %q, want the fixed notice %q", lines[0], streamEndedNotice)
		}
		if !strings.Contains(stderr, "event stream read failed") {
			t.Errorf("asJSON=%v: stderr never named the underlying read error:\n%s", asJSON, stderr)
		}
		if !exited || code == 0 {
			t.Errorf("asJSON=%v: exit = %d exited=%v, want a non-zero give-up", asJSON, code, exited)
		}
	}
}

// commandsAndEvents serves both routes the verb touches: the snapshot read it
// prints first, then the canned follow stream.
func commandsAndEvents(snapshot, stream string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat/commands":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, snapshot)
		case "/api/chat/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, stream)
		default:
			http.NotFound(w, r)
		}
	})
}

// `parlay commands --json --watch` is a STREAM, so every line of it — the
// leading snapshot envelope included — has to parse on its own or a strict
// NDJSON reader dies on line one. Driven through the verb, not watchCommands,
// because the envelope is printed before follow mode is ever entered.
func TestJSONWatchIsLineParseableEndToEnd(t *testing.T) {
	snapshot := `{"ok":true,"now":"2026-01-01T00:00:00Z","running":1,"staleAfterMs":90000,` +
		`"commands":[{"id":"pre","verb":"listen","agent":"crew-1","state":"running","durationMs":1000}]}`
	withServer(t, commandsAndEvents(snapshot, followStream))

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() {
			Commands([]string{"--json", "--watch", "--agent", "crew-1"})
		})
	})
	lines := splitLines(out)

	if len(lines) < 2 {
		t.Fatalf("--json --watch printed %d line(s), want the envelope plus follow output:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("stdout line %d does not parse on its own (%v): %q", i+1, err, line)
		}
	}

	var first map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	if _, ok := first["commands"]; !ok {
		t.Errorf("first line = %q, want the snapshot envelope", lines[0])
	}

	var last map[string]any
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &last)
	if last["ok"] != false || last["error"] != "stream-ended" {
		t.Errorf("final line = %v, want the structured give-up {ok:false, error:\"stream-ended\"}", last)
	}
	for i, line := range lines[:len(lines)-1] {
		var rec map[string]any
		_ = json.Unmarshal([]byte(line), &rec)
		if _, ok := rec["error"]; ok {
			t.Errorf("line %d carries an `error` key, destroying the discriminator: %q", i+1, line)
		}
	}
	if !exited || code == 0 {
		t.Errorf("exit = %d exited=%v, want a non-zero give-up in --json mode too", code, exited)
	}
}

// --json without --watch is a DOCUMENT: pretty-printing it is correct, and the
// compact-under-watch branch must not have flattened it.
func TestJSONSnapshotWithoutWatchStaysPretty(t *testing.T) {
	snapshot := `{"ok":true,"now":"2026-01-01T00:00:00Z","running":1,"staleAfterMs":90000,` +
		`"commands":[{"id":"pre","verb":"listen","agent":"crew-1","state":"running","durationMs":1000}]}`
	withServer(t, commandsAndEvents(snapshot, ""))

	out := captureStdout(t, func() {
		Commands([]string{"--json"})
	})
	if len(splitLines(out)) < 2 {
		t.Errorf("--json alone printed one line; the document form should stay indented:\n%s", out)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("--json output does not parse: %v", err)
	}
}

// A dropped update is the server saying "forget this id". Follow mode is built
// to run indefinitely, so its bookkeeping must shrink again — the panel prunes
// on the same signal.
func TestDroppedRecordIsForgottenByTheFollowState(t *testing.T) {
	f := commandFilter{}
	state := newWatchState()
	state.seed([]wire.CommandInvocation{
		{ID: "pre", Verb: "listen", Agent: "crew-1", State: "running"},
	}, f)

	for _, rec := range []wire.CommandInvocation{
		{ID: "new", Verb: "send", Agent: "crew-1", State: "running"},
		{ID: "new", Verb: "send", Agent: "crew-1", State: "finished", Outcome: "ok"},
		{ID: "pre", Verb: "listen", Agent: "crew-1", State: "expired", Outcome: "no-heartbeat"},
	} {
		state.update(rec, f)
	}
	if state.tracked() == 0 {
		t.Fatal("terminal records were forgotten before the server said to drop them")
	}

	for _, id := range []string{"pre", "new"} {
		if printed := state.update(wire.CommandInvocation{ID: id, State: "dropped"}, f); printed {
			t.Errorf("a retention drop for %q earned an operator-visible line", id)
		}
	}
	if state.tracked() != 0 {
		t.Errorf("state still holds %d id(s) after both were dropped (seen=%v running=%v)",
			state.tracked(), state.seen, state.running)
	}
}
