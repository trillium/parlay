package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/chathistory"
	"github.com/trillium/parlay/tools/cli/internal/timeline"
)

// history writes the fixture's own chat history: the server's file, not the
// relay's, because the question this source answers ("did anything ever pick
// this message up?") is about the half of the story the relay never writes.
func (f *timelineFixture) history(t *testing.T, lines ...string) {
	t.Helper()
	f.writeTrail(t, filepath.Join(os.Getenv("PARLAY_STATE_HOME"), chathistory.FileName), lines)
}

// histLine is one persisted message. It always carries a body, so every test
// built on it also proves the body never reaches the output.
func histLine(id, channel, stamp, text string) string {
	return `{"id":"` + id + `","role":"user","ts":"` + stamp + `","text":"` + text +
		`","channel":"` + channel + `","from":"captain"}`
}

// TestTimelineRecordedMessageWithNoHandOverIsUnhanded is the gap this source
// closes: before it, a message the server accepted and nothing ever collected
// appeared NOWHERE in the timeline — silence, where the truth is "it was never
// picked up".
func TestTimelineRecordedMessageWithNoHandOverIsUnhanded(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-3*time.Hour)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`)
	f.spool(t, "CHAT_MSG|m-1|user|still waiting")
	f.history(t,
		histLine("m-1", "crew-1", f.at(-3*time.Hour), "SECRETBODY-one"),
		histLine("m-2", "crew-1", f.at(-2*time.Hour), "SECRETBODY-two"),
	)
	f.deadServer(t)

	out, _, code, exited := timelineRun(t, nil)
	if exited {
		t.Fatalf("records answered, so no non-zero exit (code %d):\n%s", code, out)
	}
	wantLine(t, out,
		"chat history (read)",
		"recorded",
		"the relay's own hand-over line for this message is in this timeline",
		"queued",
		"unhanded",
		"NOTHING picked this message up",
		"still in the agent's history, so it can be resent",
	)
	// The bodies exist in the file and must not exist in the answer.
	notWantLine(t, out, "SECRETBODY-one", "SECRETBODY-two")

	// The whole point of a distinct outcome: it can be asked for.
	out, _, _, _ = timelineRun(t, []string{"--outcome", "unhanded"})
	wantLine(t, out, "outcome unhanded", "msg m-2", "NOTHING picked")
	notWantLine(t, out, "msg m-1")
}

// TestTimelineYoungRecordIsRecordedNotUnhanded: a message the relay has not had
// time to poll is not evidence of anything, so the grace window keeps the
// verdict from firing on the newest message in the window.
func TestTimelineYoungRecordIsRecordedNotUnhanded(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-3*time.Hour)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`)
	f.history(t, histLine("m-2", "crew-1", f.at(-5*time.Second), "just sent"))
	f.deadServer(t)

	out, _, _, _ := timelineRun(t, nil)
	wantLine(t, out, "younger than the hand-over window", "not counted as unhanded")
	notWantLine(t, out, "NOTHING picked")
}

// TestTimelineOldRelayWithNoLedgerNeverSaysUnhanded is the false-accusation
// guard that matters most: the ledger is a young record, and on a fleet whose
// relay predates it, absence of a hand-over is absence of a RECORD.
func TestTimelineOldRelayWithNoLedgerNeverSaysUnhanded(t *testing.T) {
	f := newTimelineFixture(t)
	f.audit(t, `{"ts":"`+f.at(-5*time.Hour)+`","actor":"fp1","action":"register","agent":"crew-1"}`)
	f.history(t, histLine("m-1", "crew-1", f.at(-2*time.Hour), "sent hours ago"))
	f.deadServer(t)

	out, _, _, _ := timelineRun(t, nil)
	wantLine(t, out,
		"delivery ledger (absent)",
		"recorded",
		"NO delivery trail could be read",
		"whether the relay ever took it is unknown — not absent",
	)
	notWantLine(t, out, "NOTHING picked")

	out, _, _, _ = timelineRun(t, []string{"--outcome", "unhanded"})
	wantLine(t, out, "0 event(s) matched")
}

// TestTimelineRotatedLedgerCannotAccuseTheRelay: rotation is lossy, and a
// message older than what the trail still holds must not be read as dropped.
func TestTimelineRotatedLedgerCannotAccuseTheRelay(t *testing.T) {
	f := newTimelineFixture(t)
	f.rotatedLedger(t, `{"ts":"`+f.at(-6*time.Hour)+`","event":"rotated","reason":"size-cap"}`)
	f.ledger(t, `{"ts":"`+f.at(-1*time.Hour)+`","event":"spooled","agent":"crew-2","msg":"m-9","role":"user"}`)
	f.history(t, histLine("m-1", "crew-1", f.at(-2*time.Hour), "old enough to judge"))
	f.deadServer(t)

	out, _, _, _ := timelineRun(t, nil)
	wantLine(t, out, "it has rotated", "a missing hand-over line is not evidence")
	notWantLine(t, out, "NOTHING picked")
}

// TestTimelineNamesTheHistoryFileStates: absent and unreadable are named, and
// neither is allowed to read as "no message was ever sent".
func TestTimelineNamesTheHistoryFileStates(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		f := newTimelineFixture(t)
		f.audit(t, `{"ts":"`+f.at(-5*time.Hour)+`","actor":"fp1","action":"register","agent":"crew-1"}`)
		f.deadServer(t)

		out, _, _, _ := timelineRun(t, nil)
		wantLine(t, out, "chat history (absent)", "Not the same as 'no message was ever sent'", "state-dir other than")
	})

	t.Run("unreadable", func(t *testing.T) {
		f := newTimelineFixture(t)
		f.audit(t, `{"ts":"`+f.at(-5*time.Hour)+`","actor":"fp1","action":"register","agent":"crew-1"}`)
		f.history(t, histLine("m-1", "crew-1", f.at(-2*time.Hour), "x"))
		path := filepath.Join(os.Getenv("PARLAY_STATE_HOME"), chathistory.FileName)
		if err := os.Chmod(path, 0o000); err != nil {
			t.Skipf("cannot drop read permission: %v", err)
		}
		f.deadServer(t)

		out, _, _, _ := timelineRun(t, nil)
		wantLine(t, out, "chat history (unreadable)", "what it holds is unknown")
	})
}

// TestTimelineWarnsWhenTheHistoryFileMayBelongToAnotherServer: the file is read
// off THIS host. When the CLI targets another host, those records may be a
// different server's, and a caveat is cheaper than a wrong answer.
func TestTimelineWarnsWhenTheHistoryFileMayBelongToAnotherServer(t *testing.T) {
	f := newTimelineFixture(t)
	f.history(t, histLine("m-1", "crew-1", f.at(-2*time.Hour), "x"))
	t.Setenv("PARLAY_SERVER", "http://macbook:31337")

	out, _, _, _ := timelineRun(t, nil)
	wantLine(t, out, "chat history (read)", "WARNING this is the state dir of THIS host", "may be a different server's")
}

// TestTimelineUnhandedIsInTheJSONVocabulary: a machine consumer branches on
// outcome and source, so both new members must travel verbatim.
func TestTimelineUnhandedIsInTheJSONVocabulary(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-3*time.Hour)+`","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}`)
	f.history(t, histLine("m-2", "crew-1", f.at(-2*time.Hour), "x"))
	f.deadServer(t)

	out, _, _, _ := timelineRun(t, []string{"--json", "--outcome", "unhanded"})

	var doc struct {
		Shown  int `json:"shown"`
		Events []struct {
			Outcome string `json:"outcome"`
			Source  string `json:"source"`
			Msg     string `json:"msg"`
		} `json:"events"`
		Sources []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json did not decode: %v\n%s", err, out)
	}
	if doc.Shown != 1 || len(doc.Events) != 1 {
		t.Fatalf("shown=%d events=%+v, want just the unhanded one", doc.Shown, doc.Events)
	}
	got := doc.Events[0]
	if got.Outcome != string(timeline.OutcomeUnhanded) || got.Source != string(timeline.SourceHistory) || got.Msg != "m-2" {
		t.Fatalf("event = %+v", got)
	}
	found := false
	for _, s := range doc.Sources {
		if s.Name == "chat history" && s.State == "read" {
			found = true
		}
	}
	if !found {
		t.Errorf("the history source is missing from --json: %+v", doc.Sources)
	}
}
