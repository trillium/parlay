package commands

import (
	"encoding/json"
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

func TestWatchLineMarksStartsAndEnds(t *testing.T) {
	start := watchLine(wire.CommandInvocation{
		ID: "c-1", Verb: "listen", Agent: "crew-1", State: "running", DurationMs: 1200,
		Flags: []string{"--agent"},
	})
	if !strings.HasPrefix(start, "+ ") || !strings.Contains(start, "listen") || !strings.Contains(start, "--agent") {
		t.Errorf("start line = %q", start)
	}
	end := watchLine(wire.CommandInvocation{
		ID: "c-1", Verb: "listen", Agent: "crew-1", State: "finished", DurationMs: 4000, Outcome: "ok",
	})
	if !strings.HasPrefix(end, "- ") || !strings.Contains(end, "ok") {
		t.Errorf("end line = %q", end)
	}
}
