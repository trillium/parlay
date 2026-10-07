package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/chathistory"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
	"github.com/trillium/parlay/tools/cli/internal/timeline"
)

// timelineRun captures stdout, stderr and the exit code in one call — the same
// shape explainRun uses, and for the same reason: httpc.Die panics through a
// test double, and a panic unwinding through captureStdout would skip its
// final read and leak a goroutine (CI runs the race detector).
func timelineRun(t *testing.T, argv []string) (stdout, stderr string, code int, exited bool) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	origExit := httpc.Exit
	httpc.Exit = testsupport.RecordingExit()

	outCh := make(chan string, 1)
	errCh := make(chan string, 1)
	go func() { var b bytes.Buffer; io.Copy(&b, outR); outCh <- b.String() }()
	go func() { var b bytes.Buffer; io.Copy(&b, errR); errCh <- b.String() }()

	code, exited = testsupport.Capture(func() { Timeline(argv) })

	os.Stdout, os.Stderr = origOut, origErr
	httpc.Exit = origExit
	outW.Close()
	errW.Close()
	return <-outCh, <-errCh, code, exited
}

// timelineFixture reuses explain's fixture (a private runtime dir, a private
// agent home) and adds the two durable trails, because the whole point of the
// timeline is what it can read without a relay running.
type timelineFixture struct {
	*explainFixture
	now time.Time
}

func newTimelineFixture(t *testing.T) *timelineFixture {
	t.Helper()
	return &timelineFixture{explainFixture: newExplainFixture(t, "crew-1"), now: time.Now().UTC()}
}

// at renders a stamp relative to the moment the fixture was built, so windows
// and ages in the assertions do not depend on the machine's clock.
func (f *timelineFixture) at(offset time.Duration) string {
	return f.now.Add(offset).Truncate(time.Second).Format(time.RFC3339)
}

func (f *timelineFixture) ledger(t *testing.T, lines ...string) {
	t.Helper()
	f.writeTrail(t, filepath.Join(f.runtime, "delivery.log"), lines)
}

func (f *timelineFixture) rotatedLedger(t *testing.T, lines ...string) {
	t.Helper()
	f.writeTrail(t, filepath.Join(f.runtime, "delivery.log.1"), lines)
}

func (f *timelineFixture) audit(t *testing.T, lines ...string) {
	t.Helper()
	f.writeTrail(t, filepath.Join(f.runtime, "audit.log"), lines)
}

func (f *timelineFixture) writeTrail(t *testing.T, path string, lines []string) {
	t.Helper()
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestTimelineAnswersWhatHappened is the whole command in one read: messages,
// lifecycle, delivery outcomes and a command on a single axis.
func TestTimelineAnswersWhatHappened(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t,
		`{"ts":"`+f.at(-90*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user","from":"captain"}`,
		`{"ts":"`+f.at(-89*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user","from":"captain"}`,
		`{"ts":"`+f.at(-80*time.Minute)+`","event":"spool-failed","agent":"crew-1","msg":"m-2"}`,
		`{"ts":"`+f.at(-10*time.Minute)+`","event":"delivery-ended","agent":"crew-1","reason":"channel-gone","spoolLines":1}`,
	)
	f.audit(t,
		`{"ts":"`+f.at(-95*time.Minute)+`","actor":"fp1","action":"register","agent":"crew-1"}`,
	)
	f.spool(t, "CHAT_MSG|m-1|user|the live one")
	exit := 1
	f.serverWith(t, map[string]any{
		"/api/chat/commands": map[string]any{"ok": true, "commands": []map[string]any{
			{"id": "c-1", "verb": "send", "agent": "crew-1", "state": "failed",
				"startedAt": f.at(-5 * time.Minute), "exitCode": exit, "outcome": "error", "durationMs": 1200},
		}},
	})

	out, _, _, exited := timelineRun(t, nil)
	if exited {
		t.Fatalf("a readable fleet must not exit non-zero:\n%s", out)
	}
	wantLine(t, out,
		"oldest first",
		"enrolled",
		"superseded",
		"msg m-1 (user) from captain",
		"dropped",
		"msg m-2 — the append to the agent's spool FAILED",
		"queued",
		"still in the agent's spool",
		"ended",
		"reason=channel-gone",
		"send → failed",
		"exit=1",
		"outcome=error",
		"sources",
		"delivery ledger (read)",
		"audit log (read)",
		"command registry (read)",
	)
	// The one claim this whole surface must never make.
	notWantLine(t, out, "delivered at", "was delivered", "DELIVERED")
}

// TestTimelineReadsTheTrailWhileTheRelayIsDown is the degradation that matters
// most: the operator is asking because the relay is dead, so the answer has to
// come from the files the relay already wrote.
func TestTimelineReadsTheTrailWhileTheRelayIsDown(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-30*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`)
	f.spool(t, "CHAT_MSG|m-1|user|still waiting")
	f.deadServer(t)

	out, _, code, exited := timelineRun(t, nil)
	if exited {
		t.Fatalf("the trail answered, so the command must not exit non-zero (code %d):\n%s", code, out)
	}
	wantLine(t, out,
		"queued",
		"msg m-1 (user)",
		"relay control socket (unreachable)",
		"no answer at",
		"the relay is not running",
		"The trails above are files and were still read",
		"command registry (unreachable)",
		"the server did not answer",
	)
}

// TestTimelineNamesAMissingLedgerInsteadOfAnEmptyOne: "this relay has never
// recorded a delivery event" must not read as "nothing was delivered".
func TestTimelineNamesAMissingLedgerInsteadOfAnEmptyOne(t *testing.T) {
	f := newTimelineFixture(t)
	f.audit(t, `{"ts":"`+f.at(-30*time.Minute)+`","actor":"fp1","action":"register","agent":"crew-1"}`)
	f.deadServer(t)

	out, _, _, exited := timelineRun(t, nil)
	if exited {
		t.Fatalf("the audit log answered:\n%s", out)
	}
	wantLine(t, out,
		"delivery ledger (absent)",
		"never recorded a delivery event",
		"NOT the same as 'nothing was delivered'",
	)
}

// TestTimelineUnreadableSpoolIsUnknownNotGone: a spool that is not there
// removes the ability to answer, so the answer is unknown with a reason rather
// than the friendlier "left-spool".
func TestTimelineUnreadableSpoolIsUnknownNotGone(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-30*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`)
	f.deadServer(t)

	out, _, _, exited := timelineRun(t, nil)
	if exited {
		t.Fatalf("the ledger answered:\n%s", out)
	}
	wantLine(t, out,
		"unknown",
		"the spool could not be read",
		"no spool at",
	)
	notWantLine(t, out, "left-spool")
}

// TestTimelineReadsTheRotatedGeneration: rotation is lossy, but only for what
// the marker says is gone — the generation itself is still evidence.
func TestTimelineReadsTheRotatedGeneration(t *testing.T) {
	f := newTimelineFixture(t)
	f.rotatedLedger(t,
		`{"ts":"`+f.at(-4*time.Hour)+`","event":"spooled","agent":"crew-1","msg":"ancient","role":"user"}`,
		`{"ts":"`+f.at(-3*time.Hour)+`","event":"rotated","reason":"size-cap"}`,
	)
	f.ledger(t, `{"ts":"`+f.at(-20*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`)
	f.spool(t, "CHAT_MSG|m-1|user|x")
	f.deadServer(t)

	out, _, _, exited := timelineRun(t, nil)
	if exited {
		t.Fatalf("both generations are readable:\n%s", out)
	}
	wantLine(t, out,
		"msg ancient (user)",
		"rotated",
		"every event older than this line is gone",
		"rotated generation (read)",
		"2 event(s) from the generation before the last rotation",
	)
}

// TestTimelineTellsTheRestartStory: the relay's own start and the channels it
// brought back are on the same axis as the deliveries they interrupted. That is
// what makes a gap in deliveries explainable instead of indistinguishable from a
// quiet fleet — and what makes a crash loop visible as a burst of starts.
func TestTimelineTellsTheRestartStory(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t,
		`{"ts":"`+f.at(-90*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`,
		`{"ts":"`+f.at(-60*time.Minute)+`","event":"delivery-ended","agent":"crew-1","reason":"shutdown","spoolLines":1}`,
		`{"ts":"`+f.at(-60*time.Minute)+`","event":"delivery-ended","agent":"crew-2","reason":"shutdown","spoolLines":0}`,
		`{"ts":"`+f.at(-59*time.Minute)+`","event":"started"}`,
		`{"ts":"`+f.at(-58*time.Minute)+`","event":"resumed","agent":"crew-1"}`,
		`{"ts":"`+f.at(-57*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-2","role":"user"}`,
	)
	f.spool(t, "CHAT_MSG|m-1|user|x", "CHAT_MSG|m-2|user|y")
	f.deadServer(t)

	out, _, _, exited := timelineRun(t, nil)
	if exited {
		t.Fatalf("the ledger answered, so the command must not exit non-zero:\n%s", out)
	}
	wantLine(t, out,
		"started",
		"the relay process started here",
		"a gap between two of these lines is a RESTART, not a quiet fleet",
		"resumed",
		"the relay resumed polling this channel at its start",
		"not proof the agent was listening",
		"reason=shutdown",
		"msg m-2 (user)",
	)
	// The other channel stopped at the same shutdown and has no resume row. It
	// is left as its last known fact: nothing in this trail can prove it stayed
	// down, and inventing that verdict from an absence is the one thing this
	// surface must not do.
	notWantLine(t, out, "never came back", "did not come back", "was not resumed", "is deaf")

	// The query the restart story exists for.
	only, _, _, _ := timelineRun(t, []string{"--outcome", "started,resumed"})
	wantLine(t, only, "2 matching event(s)", "started", "resumed")
	notWantLine(t, only, "reason=shutdown", "msg m-1", "msg m-2")

	js, _, _, _ := timelineRun(t, []string{"--outcome", "started,resumed", "--json"})
	var doc struct {
		Shown  int `json:"shown"`
		Events []struct {
			Outcome string `json:"outcome"`
			Source  string `json:"source"`
			Agent   string `json:"agent"`
			Detail  string `json:"detail"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json did not decode: %v\n%s", err, js)
	}
	if doc.Shown != 2 || doc.Events[0].Outcome != "started" || doc.Events[1].Outcome != "resumed" {
		t.Fatalf("--json events wrong: %+v", doc)
	}
	if doc.Events[0].Agent != "" || doc.Events[1].Agent != "crew-1" {
		t.Errorf("--json must keep the fleet-wide start agentless and the resume attributed: %+v", doc.Events)
	}
	if doc.Events[0].Source != "delivery" {
		t.Errorf("the relay's own lifecycle comes from the delivery ledger, got source %q", doc.Events[0].Source)
	}
}

// TestTimelineRecordingSwitchedOffIsNamed: only the live socket can say that
// recording is off right now; the file cannot.
func TestTimelineRecordingSwitchedOffIsNamed(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-30*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`)
	f.spool(t, "CHAT_MSG|m-1|user|x")
	f.deadServer(t)
	f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1"},
		&relayctl.Delivery{OK: true, Enabled: false, Exists: true, Ledger: filepath.Join(f.runtime, "delivery.log")})

	out, _, _, exited := timelineRun(t, nil)
	if exited {
		t.Fatalf("the ledger answered:\n%s", out)
	}
	wantLine(t, out, "delivery recording (off)", "PARLAY_RELAY_DELIVERY_LOG=0")
}

// TestTimelineWarnsWhenTheRelayPollsAnotherServer: a trail written by a relay
// bound elsewhere does not describe the traffic this CLI sends, and saying
// "relay up" without that would be the registered-but-deaf trap again.
func TestTimelineWarnsWhenTheRelayPollsAnotherServer(t *testing.T) {
	f := newTimelineFixture(t)
	f.serverWith(t, map[string]any{})
	f.relay(t, relayctl.Health{OK: true, Server: "http://somewhere-else:4242", Runtime: f.runtime}, nil, nil)

	out, _, _, _ := timelineRun(t, nil)
	wantLine(t, out, "WARNING this relay polls http://somewhere-else:4242", f.server)
}

// TestTimelineHonestExitWhenNothingIsObservable: "no events" printed for a
// fleet nobody could look at would be the exact lie this surface removes.
func TestTimelineHonestExitWhenNothingIsObservable(t *testing.T) {
	f := newTimelineFixture(t)
	f.deadServer(t)

	out, errOut, code, exited := timelineRun(t, nil)
	if !exited || code != ExitTimelineNothing {
		t.Fatalf("code = %d exited=%v, want %d true\n%s", code, exited, ExitTimelineNothing, out)
	}
	wantLine(t, errOut, "nothing was observable", "no delivery ledger, no audit log")
	wantLine(t, out, "delivery ledger (absent)", "audit log (absent)", "chat history (absent)", "command registry (unreachable)")
}

// TestTimelineDistinguishesAnOldServerFromADeadOne: a 404 means "this server
// has no registry"; a refused connection means "unknown". Only one of those is
// worth an operator's attention.
func TestTimelineDistinguishesAnOldServerFromADeadOne(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-30*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`)
	f.spool(t, "CHAT_MSG|m-1|user|x")
	f.serverWith(t, map[string]any{}) // no /api/chat/commands route

	out, _, _, _ := timelineRun(t, nil)
	wantLine(t, out, "command registry (unsupported)", "older than the live-command registry")
}

// TestTimelineFiltersNarrowTheOneTimeline: agent, outcome and window, all on
// the same axis.
func TestTimelineFiltersNarrowTheOneTimeline(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t,
		`{"ts":"`+f.at(-50*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"mine","role":"user"}`,
		`{"ts":"`+f.at(-45*time.Minute)+`","event":"spooled","agent":"crew-2","msg":"theirs","role":"user"}`,
		`{"ts":"`+f.at(-5*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"recent","role":"user"}`,
		`{"ts":"`+f.at(-40*time.Minute)+`","event":"spool-failed","agent":"crew-1","msg":"lost"}`,
	)
	f.spool(t, "CHAT_MSG|mine|user|x", "CHAT_MSG|recent|user|x")
	if err := os.WriteFile(filepath.Join(f.runtime, "crew-2.chan"), []byte("CHAT_MSG|theirs|user|x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.deadServer(t)

	out, _, _, exited := timelineRun(t, []string{"--agent", "crew-1", "--since", "30m"})
	if exited {
		t.Fatalf("the ledger answered:\n%s", out)
	}
	wantLine(t, out, "agent crew-1", "msg recent (user)", "1 matching event(s)")
	notWantLine(t, out, "msg mine", "msg theirs", "msg lost")

	out, _, _, _ = timelineRun(t, []string{"--outcome", "dropped"})
	wantLine(t, out, "msg lost", "dropped", "1 matching event(s)")

	// --channel is the same axis under the name the poll API uses.
	out, _, _, _ = timelineRun(t, []string{"--channel", "crew-2"})
	wantLine(t, out, "msg theirs", "agent crew-2")
}

// TestTimelineLimitKeepsTheNewestAndSaysSo: a truncated list that reads like a
// complete one is the failure the header line exists to prevent.
func TestTimelineLimitKeepsTheNewestAndSaysSo(t *testing.T) {
	f := newTimelineFixture(t)
	lines := []string{}
	for i := 0; i < 5; i++ {
		lines = append(lines, `{"ts":"`+f.at(time.Duration(-50+i)*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-`+string(rune('0'+i))+`","role":"user"}`)
	}
	f.ledger(t, lines...)
	f.spool(t, "CHAT_MSG|m-4|user|x")
	f.deadServer(t)

	out, _, _, _ := timelineRun(t, []string{"--limit", "2"})
	wantLine(t, out, "newest 2 of 5 matching event(s) (--limit 0 shows all)", "msg m-3", "msg m-4")
	notWantLine(t, out, "msg m-0", "msg m-1")

	out, _, _, _ = timelineRun(t, []string{"--limit", "0"})
	wantLine(t, out, "5 matching event(s)", "msg m-0", "msg m-4")
}

// TestTimelineJSONIsCompleteAndTruncationAware: a script must be able to tell
// a complete answer from a truncated one, and an undated record from a
// midnight one.
func TestTimelineJSONIsCompleteAndTruncationAware(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t,
		`{"ts":"`+f.at(-30*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`,
		`{"ts":"whenever","event":"rotated","reason":"size-cap"}`,
	)
	f.spool(t, "CHAT_MSG|m-1|user|x")
	f.deadServer(t)

	out, _, _, _ := timelineRun(t, []string{"--json"})

	var doc struct {
		OK      bool `json:"ok"`
		Shown   int  `json:"shown"`
		Matched int  `json:"matched"`
		Limit   int  `json:"limit"`
		Events  []struct {
			At      string `json:"at"`
			AtKnown bool   `json:"atKnown"`
			AtRaw   string `json:"atRaw"`
			Outcome string `json:"outcome"`
			Source  string `json:"source"`
			Msg     string `json:"msg"`
			Detail  string `json:"detail"`
		} `json:"events"`
		Sources []struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Path  string `json:"path"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json did not decode: %v\n%s", err, out)
	}
	if !doc.OK || doc.Shown != 2 || doc.Matched != 2 || doc.Limit != timelineDefaultLimit {
		t.Fatalf("envelope counts wrong: %+v", doc)
	}
	if doc.Events[0].AtKnown != true || doc.Events[0].Outcome != string(timeline.OutcomeQueued) || doc.Events[0].Source != "delivery" {
		t.Errorf("first event wrong: %+v", doc.Events[0])
	}
	last := doc.Events[1]
	if last.AtKnown || last.At != "" || last.AtRaw != "whenever" || last.Outcome != "rotated" {
		t.Errorf("an undated record must decode as unknown-time, not midnight: %+v", last)
	}
	found := false
	for _, s := range doc.Sources {
		if s.Name == "delivery ledger" && s.State == "read" && strings.HasSuffix(s.Path, "delivery.log") {
			found = true
		}
	}
	if !found {
		t.Errorf("sources block incomplete: %+v", doc.Sources)
	}
}

// TestTimelineIsReadOnly: an observability surface that can change the thing it
// observes is not one. Every control-socket request must be a GET, and the
// trails must be byte-identical afterwards.
func TestTimelineIsReadOnly(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-30*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`)
	f.audit(t, `{"ts":"`+f.at(-31*time.Minute)+`","actor":"fp1","action":"register","agent":"crew-1"}`)
	f.spool(t, "CHAT_MSG|m-1|user|x")
	f.history(t, histLine("m-1", "crew-1", f.at(-30*time.Minute), "x"))
	f.deadServer(t)
	f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1"},
		&relayctl.Delivery{OK: true, Enabled: true, Exists: true, Ledger: filepath.Join(f.runtime, "delivery.log")})

	before := map[string][]byte{}
	for _, path := range []string{
		filepath.Join(f.runtime, "delivery.log"),
		filepath.Join(f.runtime, "audit.log"),
		filepath.Join(f.runtime, "crew-1.chan"),
		filepath.Join(os.Getenv("PARLAY_STATE_HOME"), chathistory.FileName),
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		before[path] = body
	}

	_, _, _, exited := timelineRun(t, nil)
	if exited {
		t.Fatal("unexpected exit")
	}
	if !f.relaySawOnlyGets() {
		t.Fatal("timeline sent a non-GET to the relay control socket — the relay also serves POST /register and POST /unregister, and a diagnostic must not be able to reach them")
	}
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s changed (err=%v)", path, err)
		}
	}
}

// TestTimelineUsageErrors: every rejection is a usage error (exit 2) with a
// message that says what to type instead.
func TestTimelineUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"bad since", []string{"--since", "yesterday"}, "--since not an RFC3339 timestamp"},
		{"bad until", []string{"--until", "soon"}, "--until not an RFC3339 timestamp"},
		{"unknown outcome", []string{"--outcome", "delivered"}, "unknown outcome \"delivered\""},
		{"negative limit", []string{"--limit", "-3"}, "--limit cannot be negative"},
		{"junk limit", []string{"--limit", "lots"}, "--limit wants a non-negative count"},
		{"two positionals", []string{"a", "b"}, "at most one agent id"},
		{"agent named twice", []string{"a", "--agent", "b"}, "agent named twice and differently"},
		{"channel and agent disagree", []string{"--agent", "a", "--channel", "b"}, "--agent and --channel name the same thing"},
		{"window inverted", []string{"--since", "1h", "--until", "2h"}, "--until is before --since"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTimelineFixture(t)
			f.deadServer(t)
			_, errOut, code, exited := timelineRun(t, tc.argv)
			if !exited || code != 2 {
				t.Fatalf("code=%d exited=%v, want 2 true", code, exited)
			}
			wantLine(t, errOut, tc.want)
		})
	}
}

// TestTimelineDaysAreATimeUnit: an incident window is measured in days and
// time.ParseDuration has no day unit.
func TestTimelineDaysAreATimeUnit(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t,
		`{"ts":"`+f.at(-50*time.Hour)+`","event":"spooled","agent":"crew-1","msg":"old","role":"user"}`,
		`{"ts":"`+f.at(-2*time.Hour)+`","event":"spooled","agent":"crew-1","msg":"new","role":"user"}`,
	)
	f.spool(t, "CHAT_MSG|new|user|x")
	f.deadServer(t)

	out, _, _, _ := timelineRun(t, []string{"--since", "3d"})
	wantLine(t, out, "since 3d", "msg old", "msg new")
	out, _, _, _ = timelineRun(t, []string{"--since", "1d"})
	wantLine(t, out, "msg new")
	notWantLine(t, out, "msg old")
}
