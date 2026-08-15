package commandreport

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// recorder is a stub registry server that remembers every report it received.
type recorder struct {
	mu   sync.Mutex
	hits []map[string]any
	code int // non-2xx to return, 0 for 200
}

func (rc *recorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if payload == nil {
			payload = map[string]any{}
		}
		payload["_path"] = r.URL.Path

		rc.mu.Lock()
		rc.hits = append(rc.hits, payload)
		code := rc.code
		rc.mu.Unlock()

		if code != 0 {
			http.Error(w, "nope", code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func (rc *recorder) paths() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	out := make([]string, len(rc.hits))
	for i, h := range rc.hits {
		out[i], _ = h["_path"].(string)
	}
	return out
}

func (rc *recorder) first(path string) map[string]any {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, h := range rc.hits {
		if h["_path"] == path {
			return h
		}
	}
	return nil
}

// withRecorder points the CLI at a stub registry and restores httpc.Exit,
// which Begin wraps.
func withRecorder(t *testing.T) *recorder {
	t.Helper()
	rc := &recorder{}
	srv := httptest.NewServer(rc.handler())
	t.Cleanup(srv.Close)
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "crew-1")

	prev := httpc.Exit
	t.Cleanup(func() { httpc.Exit = prev })
	return rc
}

func TestBeginReportsStartAndFinishReportsEnd(t *testing.T) {
	rc := withRecorder(t)

	finish := Begin("send", []string{"--agent", "crew-2", "hello there"})
	if got := rc.paths(); !reflect.DeepEqual(got, []string{"/api/chat/command-start"}) {
		t.Fatalf("after Begin, requests = %v, want just the start report", got)
	}
	start := rc.first("/api/chat/command-start")
	if start["verb"] != "send" || start["agent"] != "crew-1" {
		t.Errorf("start payload = %v, want verb send / agent crew-1", start)
	}
	if start["id"] == "" || start["pid"] == nil {
		t.Errorf("start payload = %v, want an id and a pid", start)
	}

	finish(0)
	end := rc.first("/api/chat/command-end")
	if end == nil {
		t.Fatalf("no end report; requests = %v", rc.paths())
	}
	if end["state"] != "finished" || end["outcome"] != "ok" || end["id"] != start["id"] {
		t.Errorf("end payload = %v, want finished/ok for the same id", end)
	}
}

func TestNonZeroExitIsReportedAsFailed(t *testing.T) {
	rc := withRecorder(t)
	Begin("merge-gate", []string{"7"})(3)

	end := rc.first("/api/chat/command-end")
	if end["state"] != "failed" || end["outcome"] != "error" || end["exitCode"] != float64(3) {
		t.Errorf("end payload = %v, want failed/error/exit 3", end)
	}
}

// TestDieStillReportsTheEnd is the reason Begin wraps httpc.Exit: httpc.Die
// is the CLI's universal fatal path, and a command that dies there must not
// leave a record stuck at "running" until the server's reaper notices.
func TestDieStillReportsTheEnd(t *testing.T) {
	rc := withRecorder(t)

	// Install the recording exit FIRST so Begin wraps it: that ordering is
	// exactly main.go's, where Begin wraps whatever httpc.Exit is at startup.
	httpc.Exit = testsupport.RecordingExit()
	Begin("agents", nil)

	code, exited := testsupport.Capture(func() { httpc.Die("boom", 1) })
	if !exited || code != 1 {
		t.Fatalf("die = %d exited=%v, want exit 1", code, exited)
	}

	end := rc.first("/api/chat/command-end")
	if end == nil {
		t.Fatalf("die path reported no end; requests = %v", rc.paths())
	}
	if end["state"] != "failed" || end["exitCode"] != float64(1) {
		t.Errorf("end payload = %v, want failed/exit 1", end)
	}
}

func TestFinishIsIdempotent(t *testing.T) {
	rc := withRecorder(t)
	finish := Begin("history", nil)
	finish(0)
	finish(1)
	finish(0)

	ends := 0
	for _, p := range rc.paths() {
		if p == "/api/chat/command-end" {
			ends++
		}
	}
	if ends != 1 {
		t.Errorf("end reports = %d, want exactly 1", ends)
	}
}

func TestTheObserverNeverReportsItself(t *testing.T) {
	rc := withRecorder(t)
	Begin("commands", []string{"--watch"})(0)
	if got := rc.paths(); len(got) != 0 {
		t.Errorf("`parlay commands` reported itself: %v", got)
	}
}

func TestEnvKillSwitchDisablesReporting(t *testing.T) {
	rc := withRecorder(t)
	t.Setenv(EnvDisable, "0")
	Begin("send", []string{"--agent", "crew-2"})(0)
	if got := rc.paths(); len(got) != 0 {
		t.Errorf("PARLAY_COMMAND_REPORT=0 still reported: %v", got)
	}
}

// TestUnreachableServerIsSilentAndCostsOneAttempt is constraint 2 of the
// brief at its sharpest: reporting must never become a new failure mode for
// the command it observes. Nothing exits, nothing prints, and the failed
// start disables the reporter so no later request pays another timeout.
func TestUnreachableServerIsSilentAndCostsOneAttempt(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1") // nothing listens here

	prevExit := httpc.Exit
	httpc.Exit = func(int) { t.Fatal("reporting must never exit the process") }
	t.Cleanup(func() { httpc.Exit = prevExit })

	start := time.Now()
	finish := Begin("doctor", nil)
	finish(0) // must not panic, block, or exit

	// One failed start disables the reporter, so finish costs nothing more.
	// The bound is generous; the point is that it is not one timeout per
	// report for the rest of the process.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("reporting an unreachable server took %v; it must give up after the first failure", elapsed)
	}
}

func TestServerRejectionDisablesFurtherReports(t *testing.T) {
	rc := withRecorder(t)
	rc.code = http.StatusInternalServerError

	finish := Begin("stats", nil)
	finish(0)

	for _, p := range rc.paths() {
		if p == "/api/chat/command-end" {
			t.Errorf("a rejected start must not be followed by an end report; requests = %v", rc.paths())
		}
	}
}

// TestFlagNamesNeverCarryValues is the redaction guarantee on the CLI side.
// The server sanitizes again on arrival, but a value must not leave this
// process in the first place.
//
// A leading dash is NOT what makes a token a flag — the shape is. A parlay
// command line routinely carries message bodies, and a body that happens to
// open with punctuation ("-- heads up: …") is still a body. Every case below
// that is not exactly one-or-two-dashes-then-a-letter-then-word-characters is
// a positional, and a positional is reported nowhere.
func TestFlagNamesNeverCarryValues(t *testing.T) {
	const secret = "sk-live-abcdef"
	cases := []struct {
		name string
		argv []string
		want []string
	}{
		{"separate value", []string{"--token", "s3cr3t"}, []string{"--token"}},
		{"attached value", []string{"--token=s3cr3t"}, []string{"--token"}},
		{"bare positional", []string{"a whole private message body"}, []string{}},
		{"short flag", []string{"-h"}, []string{"-h"}},
		{"lone dash", []string{"-"}, []string{}},
		{"bare terminator", []string{"--"}, []string{}},
		{"after --", []string{"--json", "--", "--not-a-flag"}, []string{"--json"}},
		{"secret after --", []string{"--json", "--", "--token", secret}, []string{"--json"}},
		{"deduped", []string{"--json", "--json"}, []string{"--json"}},
		{"path positional", []string{"send", "/home/someone/secrets.txt"}, []string{}},
		{"message body opening with a terminator", []string{"-- heads up: the key is " + secret}, []string{}},
		{"flag-shaped prefix then prose", []string{"--flag with space"}, []string{}},
		{"negative number", []string{"-5"}, []string{}},
		{"punctuation body", []string{"--!?"}, []string{}},
		{"three dashes", []string{"---json"}, []string{}},
		{"empty token", []string{""}, []string{}},
	}
	for _, c := range cases {
		got := flagNames(c.argv)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: flagNames(%v) = %v, want %v", c.name, c.argv, got, c.want)
		}
		for _, name := range got {
			if strings.ContainsAny(name, " \t\n/") {
				t.Errorf("%s: reported %q, which is not a bare flag name", c.name, name)
			}
			for _, leak := range []string{"s3cr3t", secret, "sk-live", "heads"} {
				if strings.Contains(name, leak) {
					t.Errorf("%s: reported %q, which carries %q", c.name, name, leak)
				}
			}
		}
	}
}

func TestFlagNamesAreCapped(t *testing.T) {
	argv := []string{"--a", "--b", "--c", "--d", "--e", "--f", "--g", "--h", "--i", "--j"}
	if got := flagNames(argv); len(got) != maxReportedFlags {
		t.Errorf("flagNames returned %d names, want the %d cap", len(got), maxReportedFlags)
	}
}

func TestStartPayloadCarriesNoPositionalsAtAll(t *testing.T) {
	rc := withRecorder(t)
	Begin("say", []string{
		"the quick brown fox",
		"--agent", "crew-2",
		"-- heads up: the key is sk-live-abcdef",
		"--", "--token", "sk-live-abcdef",
	})(0)

	raw, _ := json.Marshal(rc.first("/api/chat/command-start"))
	for _, leak := range []string{"quick brown fox", "crew-2", "sk-live", "abcdef", "heads up", "--token"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("start payload leaked %q: %s", leak, raw)
		}
	}
}
