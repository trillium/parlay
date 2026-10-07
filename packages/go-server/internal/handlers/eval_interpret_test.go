// The eval door's other two input outcomes, recorded from the engine's own
// verdict rather than recomputed here:
//
//   - `fired`: what the buffer became. A phrase that matched a command and a
//     phrase that matched nothing leave the same visible trace (the box keeps
//     its text) and used to leave the same silence in every record.
//   - `pickerHint` / `senderPickerHint`: a spoken destination the engine
//     matched against the offered channels and found nothing. The hint flashes
//     in the panel and nothing durable said it had happened.
//
// These tests pin the recorded state, the precedence between the verdicts,
// that neither one copies what the operator said, and that a wedged ledger
// still cannot slow or fail the relay that carries the text to the engine.
package handlers

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/inputlog"
)

// firedSubmitEngine answers the way the real engine answers a buffer whose
// phrase matched the `submit` command (evalengine/engine.go runPass returns
// the command id in `fired`).
func firedSubmitEngine(t *testing.T) {
	t.Helper()
	fakeEngineAnswer(t, `{"v":1,"streamId":"eval-dev-1-main","seq":2,"baseVersion":2,"engineEvalNs":4,`+
		`"fired":"submit","actions":[{"verb":"armSubmit","args":{"delayMs":1000}}]}`)
}

func TestFiredCommandIsRecordedAsWhatTheInputBecame(t *testing.T) {
	st := newTestStore(t)
	firedSubmitEngine(t)

	rec := postEvalWithStore(t, newHub(newBroker()), st,
		`{"device":"dev-1","streamId":"eval-dev-1-main","version":2,"text":"SHIP IT NOW"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// The engine's envelope still reaches the caller untouched: recording
	// observes the relay, it does not rewrite what the panel receives.
	if !strings.Contains(rec.Body.String(), `"armSubmit"`) {
		t.Errorf("the relay rewrote the engine's actions: %s", rec.Body.String())
	}

	ev := waitForEvents(t, st, 1)
	if len(ev) != 1 {
		t.Fatalf("ledger has %d events for one fired command, want 1: %+v", len(ev), ev)
	}
	got := ev[0]
	if got.Stage != inputlog.StageInterpreted || got.Class != inputlog.ClassOK {
		t.Errorf("hop = %s/%s, want interpreted/ok — the buffer WAS interpreted", got.Stage, got.Class)
	}
	if got.Source != inputSourceEval {
		t.Errorf("source = %q, want %q", got.Source, inputSourceEval)
	}
	if got.InputID == "" {
		t.Error("a fired command must carry a ledger-local id so it can be replayed")
	}
	for _, want := range []string{"command=submit", "stream=eval-dev-1-main", "v=2"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail = %q, want it to carry %q", got.Detail, want)
		}
	}
	if strings.Contains(got.Detail, "SHIP IT NOW") {
		t.Errorf("the recorded hop carries the operator's text: %q", got.Detail)
	}
}

func TestPickerNoMatchIsRecordedForEachPicker(t *testing.T) {
	cases := []struct {
		name, mode, body, wantReason string
		tabs                         int
		wantCandidates               string
	}{
		{
			name: "channel-select",
			mode: "channel-select",
			// `fired` is the MODE name here, exactly as the real engine
			// answers a picker: a channel-select request bypasses command
			// matching (evalengine.Engine.Eval) and reports the resolution
			// path that ran. Omitting it from this stub is what let a real
			// picker miss be recorded as a fired command named after the
			// picker.
			body: `{"v":1,"streamId":"eval-dev-1-picker","seq":1,"baseVersion":1,"fired":"channel-select",` +
				`"actions":[{"verb":"pickerHint","args":{"text":"No channel matched \"zzz\" — try again"}}]}`,
			wantReason:     reasonChannelNotMatched,
			tabs:           3,
			wantCandidates: "candidates=3",
		},
		{
			name: "sender-select",
			mode: "sender-select",
			body: `{"v":1,"streamId":"eval-dev-1-sender","seq":1,"baseVersion":1,"fired":"sender-select",` +
				`"actions":[{"verb":"senderPickerHint","args":{"text":"No contact matched \"zzz\" — try again"}}]}`,
			wantReason: reasonSenderNotMatched,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			fakeEngineAnswer(t, tc.body)

			body := `{"device":"dev-1","mode":"` + tc.mode + `","version":1,"text":"zzz"`
			if tc.tabs > 0 {
				body += `,"tabs":[{"id":"a"},{"id":"b"},{"id":"c"}]`
			}
			body += `}`
			rec := postEvalWithStore(t, newHub(newBroker()), st, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}

			ev := waitForEvents(t, st, 1)
			if len(ev) != 1 {
				t.Fatalf("ledger has %d events for one picker miss, want 1: %+v", len(ev), ev)
			}
			got := ev[0]
			if got.Stage != inputlog.StageRouted || got.Class != inputlog.ClassNoMatch {
				t.Errorf("hop = %s/%s, want routed/no_match", got.Stage, got.Class)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q — the two pickers are different failures", got.Reason, tc.wantReason)
			}
			if !strings.Contains(got.Detail, "mode="+tc.mode) {
				t.Errorf("detail = %q, want it to name the picker that missed", got.Detail)
			}
			if tc.wantCandidates != "" && !strings.Contains(got.Detail, tc.wantCandidates) {
				t.Errorf("detail = %q, want %q — the candidate count is the context the panel cannot see",
					got.Detail, tc.wantCandidates)
			}
			if tc.wantCandidates == "" && strings.Contains(got.Detail, "candidates=") {
				t.Errorf("detail = %q claims a candidate count for a list the request never carried", got.Detail)
			}
			// The hint's own text carries the spoken words; the ledger never does.
			for _, leak := range []string{"zzz", "No channel matched", "No contact matched"} {
				if strings.Contains(got.Detail+got.Reason+got.InputID, leak) {
					t.Errorf("the recorded hop carries the operator's words (%q): %+v", leak, got)
				}
			}
		})
	}
}

// The delivery constraint, on the new producers: a ledger whose sink never
// returns must not cost the composer its answer or the panel its frame.
func TestCommandVerdictStillDeliversWithAWedgedLedger(t *testing.T) {
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
	firedSubmitEngine(t)

	start := time.Now()
	rec := postEvalWithStore(t, hub, st, `{"device":"dev-1","streamId":"eval-dev-1-main","version":2,"text":"ship it"}`)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "armSubmit") {
		t.Fatalf("relay with a wedged ledger = %d %s, want 200 carrying the engine's verdict", rec.Code, rec.Body.String())
	}
	if elapsed > time.Second {
		t.Fatalf("relay took %v with a wedged ledger — observability is in the relay path", elapsed)
	}
	if ev := awaitEvent(t, sub, "input_action", time.Second); ev.data == nil {
		t.Fatal("no input_action frame reached the device while the ledger was wedged")
	}
}
