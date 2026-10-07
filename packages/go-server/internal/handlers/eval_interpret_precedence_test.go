// Which of an eval's verdicts becomes the recorded input outcome, and the
// one verdict the real engine produces that a stub got wrong: in a picker mode
// `fired` carries the MODE name, because the picker bypasses command matching.
// Reading it as a command turned a picker miss into `interpreted/ok
// command=channel-select` and — because that branch ran first — dropped the
// real `no_match` hop entirely.
package handlers

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/inputlog"
)

// Precedence is deliberate: a snapshot the engine never interpreted is not
// "what the input became". The picker case is the one the real engine actually
// produces and the one that was wrong: in a picker mode `fired` carries the
// module name, never a command.
func TestEvalOutcomePrecedence(t *testing.T) {
	cases := []struct {
		name, mode, body, wantStage, wantClass string
	}{
		{
			name: "superseded beats fired",
			body: `{"v":1,"streamId":"s","fired":"submit","actions":[` +
				`{"verb":"noop","args":{"reason":"stale-request-version"}}]}`,
			wantStage: inputlog.StageSuperseded,
			wantClass: inputlog.ClassSuperseded,
		},
		{
			name:      "a command fired in a normal eval is what the input became",
			body:      `{"v":1,"streamId":"s","fired":"clear","actions":[{"verb":"clear"}]}`,
			wantStage: inputlog.StageInterpreted,
			wantClass: inputlog.ClassOK,
		},
		{
			name: "a picker's fired is the mode, so a miss stays a miss",
			mode: modeChannelSelect,
			body: `{"v":1,"streamId":"s","fired":"channel-select","actions":[` +
				`{"verb":"pickerHint","args":{"text":"nope"}}]}`,
			wantStage: inputlog.StageRouted,
			wantClass: inputlog.ClassNoMatch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			fakeEngineAnswer(t, tc.body)
			body := `{"device":"dev-1",`
			if tc.mode != "" {
				body += `"mode":"` + tc.mode + `",`
			}
			body += `"version":1,"text":"x"}`
			postEvalWithStore(t, newHub(newBroker()), st, body)
			ev := waitForEvents(t, st, 1)
			if len(ev) != 1 {
				t.Fatalf("ledger has %d events, want exactly one outcome per eval: %+v", len(ev), ev)
			}
			if ev[0].Stage != tc.wantStage || ev[0].Class != tc.wantClass {
				t.Errorf("hop = %s/%s, want %s/%s", ev[0].Stage, ev[0].Class, tc.wantStage, tc.wantClass)
			}
		})
	}
}

// A picker that DID resolve records nothing, exactly as before: the panel
// switches tabs visibly, so it is not a silent failure — and the engine still
// reports `fired` as the mode, which must not be mistaken for a command.
func TestResolvedPickerRecordsNothing(t *testing.T) {
	st := newTestStore(t)
	fakeEngineAnswer(t, `{"v":1,"streamId":"s","fired":"channel-select","actions":[`+
		`{"verb":"switchTab","args":{"id":"a"}},{"verb":"closeChannelPicker"}]}`)
	rec := postEvalWithStore(t, newHub(newBroker()), st,
		`{"device":"dev-1","mode":"channel-select","version":1,"text":"a"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if ev := st.Input.Events(); len(ev) != 0 {
		t.Errorf("a resolved picker recorded %d hop(s), want none: %+v", len(ev), ev)
	}
}

// The delivery constraint on the branch this file exists for: naming a picker
// miss must not cost the panel its pickerHint frame, nor the composer its
// answer, however wedged the ledger is.
func TestPickerMissStillDeliversWithAWedgedLedger(t *testing.T) {
	st := newTestStore(t)
	block := make(chan struct{})
	wedged, err := inputlog.Open(filepath.Join(t.TempDir(), "input.jsonl"), inputlog.Options{
		Queue:    1,
		Appender: func([]byte) error { <-block; return nil },
	})
	if err != nil {
		t.Fatalf("inputlog.Open: %v", err)
	}
	t.Cleanup(func() { close(block) })
	st.Input.Close()
	st.Input = wedged

	hub := newHub(newBroker())
	t.Cleanup(hub.Stop)
	sub, cancel := hub.subscribe("dev-1")
	defer cancel()

	fakeEngineAnswer(t, `{"v":1,"streamId":"eval-dev-1-picker","seq":1,"baseVersion":1,"fired":"channel-select",`+
		`"actions":[{"verb":"pickerHint","args":{"text":"No channel matched"}}]}`)

	start := time.Now()
	rec := postEvalWithStore(t, hub, st,
		`{"device":"dev-1","mode":"channel-select","version":1,"text":"zzz","tabs":[{"id":"a"}]}`)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pickerHint") {
		t.Fatalf("relay with a wedged ledger = %d %s, want 200 carrying the picker hint", rec.Code, rec.Body.String())
	}
	if elapsed > time.Second {
		t.Fatalf("relay took %v with a wedged ledger — observability is in the relay path", elapsed)
	}
	if ev := awaitEvent(t, sub, "input_action", time.Second); ev.data == nil {
		t.Fatal("no input_action frame reached the device while the ledger was wedged")
	}
}
