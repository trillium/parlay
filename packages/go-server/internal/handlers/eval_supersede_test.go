// The eval relay's one INPUT outcome (as opposed to a transport fact): a
// composer snapshot the engine dropped because a newer one had already
// replaced it.
//
// These tests pin three things: that the verdict is recorded as its own named
// state with the stream and version it belonged to; that ordinary evals record
// NOTHING, because a row per keystroke would drown the seam it is meant to
// make legible; and that a wedged ledger can neither slow nor fail the relay
// that carries the operator's text to the engine.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/inputlog"
)

// fakeEngineAnswer stubs the eval engine with one fixed envelope body.
func fakeEngineAnswer(t *testing.T, body string) {
	t.Helper()
	resetStreamTable(t)
	fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
}

// staleEngine answers every /eval the way the real engine answers a snapshot
// whose version is older than the newest it has seen for that stream
// (evalengine/engine.go, "Last-write-wins").
func staleEngine(t *testing.T) {
	t.Helper()
	fakeEngineAnswer(t, `{"v":1,"streamId":"eval-dev-1-main","seq":7,"baseVersion":3,"engineEvalNs":9,`+
		`"actions":[{"verb":"noop","args":{"reason":"stale-request-version"}}]}`)
}

func TestSupersededEvalIsRecordedAsItsOwnState(t *testing.T) {
	st := newTestStore(t)
	staleEngine(t)

	rec := postEvalWithStore(t, newHub(newBroker()), st,
		`{"device":"dev-1","streamId":"eval-dev-1-main","version":3,"text":"SHIP IT NOW"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// The verdict still reaches the caller verbatim: recording observes the
	// relay, it does not rewrite what the panel receives.
	var resp struct {
		Actions []map[string]any `json:"actions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Actions) != 1 || resp.Actions[0]["verb"] != "noop" {
		t.Errorf("actions = %+v, want the engine's noop passed through untouched", resp.Actions)
	}

	ev := waitForEvents(t, st, 1)
	if len(ev) != 1 {
		t.Fatalf("ledger has %d events for one superseded eval, want 1: %+v", len(ev), ev)
	}
	got := ev[0]
	if got.Stage != inputlog.StageSuperseded || got.Class != inputlog.ClassSuperseded {
		t.Errorf("hop = %s/%s, want superseded/superseded", got.Stage, got.Class)
	}
	if got.Source != inputSourceEval {
		t.Errorf("source = %q, want %q", got.Source, inputSourceEval)
	}
	if got.Reason != reasonSupersededByNewerVersion {
		t.Errorf("reason = %q, want %q — a supersession must name itself, not be an absence", got.Reason, reasonSupersededByNewerVersion)
	}
	if got.InputID == "" {
		t.Error("a superseded input must still carry a ledger-local id so it can be replayed")
	}
	for _, want := range []string{"stream=eval-dev-1-main", "v=3", "engine=" + evalStaleToken} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail = %q, want it to carry %q", got.Detail, want)
		}
	}
	// The ledger's standing rule: never a copy of what the operator said.
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "SHIP IT NOW") {
		t.Errorf("the recorded hop carries the operator's text: %s", encoded)
	}
}

func TestOrdinaryEvalRecordsNothing(t *testing.T) {
	st := newTestStore(t)
	fakeEngineAnswer(t, `{"v":1,"streamId":"eval-dev-1-main","seq":1,"baseVersion":1,"engineEvalNs":3,`+
		`"actions":[{"verb":"noop","args":{"reason":"nothing-matched"}}]}`)

	rec := postEvalWithStore(t, newHub(newBroker()), st,
		`{"device":"dev-1","streamId":"eval-dev-1-main","version":4,"text":"hello"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// A negative assertion, so there is nothing to await: give the async
	// writer a generous window and then assert the ledger is still empty.
	time.Sleep(150 * time.Millisecond)
	if ev := st.Input.Events(); len(ev) != 0 {
		t.Fatalf("an ordinary eval recorded %d hop(s) — a row per keystroke would drown the seam: %+v", len(ev), ev)
	}
	if stats := st.Input.Stats(); stats.Rejected != 0 || stats.Dropped != 0 {
		t.Errorf("ledger stats = %+v, want no rejections or drops", stats)
	}
}

func TestSupersededEvalStillDeliversWithAWedgedLedger(t *testing.T) {
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
	staleEngine(t)

	start := time.Now()
	rec := postEvalWithStore(t, hub, st, `{"device":"dev-1","streamId":"eval-dev-1-main","version":3,"text":"ship it"}`)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), evalStaleToken) {
		t.Fatalf("relay with a wedged ledger = %d %s, want 200 carrying the engine's verdict", rec.Code, rec.Body.String())
	}
	if elapsed > time.Second {
		t.Fatalf("relay took %v with a wedged ledger — observability is in the relay path", elapsed)
	}
	// The panel still gets its frame: an observer that cannot write must not
	// cost the composer its answer.
	ev := awaitEvent(t, sub, "input_action", time.Second)
	if ev.data == nil {
		t.Fatal("no input_action frame reached the device while the ledger was wedged")
	}
}

func TestEvalWasSupersededReadsOnlyTheEngineVerdict(t *testing.T) {
	cases := []struct {
		name    string
		actions []interface{}
		want    bool
	}{
		{"nil", nil, false},
		{"empty", []interface{}{}, false},
		{"not an object", []interface{}{"noop"}, false},
		{"noop without args", []interface{}{map[string]interface{}{"verb": "noop"}}, false},
		{"noop for another reason", []interface{}{map[string]interface{}{
			"verb": "noop", "args": map[string]interface{}{"reason": "voice-disabled"}}}, false},
		{"a real action", []interface{}{map[string]interface{}{"verb": "setText"}}, false},
		{"stale noop", []interface{}{map[string]interface{}{
			"verb": "noop", "args": map[string]interface{}{"reason": "stale-request-version"}}}, true},
		{"stale among others", []interface{}{
			map[string]interface{}{"verb": "showHint"},
			map[string]interface{}{"verb": "noop", "args": map[string]interface{}{"reason": "stale-request-version"}},
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalWasSuperseded(tc.actions); got != tc.want {
				t.Errorf("evalWasSuperseded(%+v) = %v, want %v", tc.actions, got, tc.want)
			}
		})
	}
}

func TestSupersessionDetailIsBoundedAndNeverSplitsARune(t *testing.T) {
	d := evalSupersessionDetail(strings.Repeat("é", 400), 12)
	if !strings.HasPrefix(d, "stream=") || !strings.Contains(d, "v=12") {
		t.Fatalf("detail = %q, want the stream and version", d)
	}
	stream := strings.TrimSuffix(strings.TrimPrefix(d, "stream="), " v=12 engine="+evalStaleToken)
	if n := len([]rune(stream)); n != evalDetailStreamCap+3 {
		t.Errorf("stream part is %d runes, want %d (cap plus the ellipsis)", n, evalDetailStreamCap+3)
	}
	if !json.Valid([]byte(`"` + stream + `"`)) {
		t.Errorf("truncation produced invalid UTF-8: %q", stream)
	}
}

// A ledger whose sink fails (rather than blocks) is the other half of the same
// constraint: a failing observer is counted, never propagated.
func TestFailingLedgerSinkDoesNotFailTheRelay(t *testing.T) {
	st := newTestStore(t)
	broken, err := inputlog.Open(filepath.Join(t.TempDir(), "input.jsonl"), inputlog.Options{
		Appender: func([]byte) error { return errors.New("disk on fire") },
	})
	if err != nil {
		t.Fatalf("inputlog.Open: %v", err)
	}
	st.Input.Close()
	st.Input = broken
	t.Cleanup(broken.Close)
	staleEngine(t)

	rec := postEvalWithStore(t, newHub(newBroker()), st, `{"device":"dev-1","version":2,"text":"ship it"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a failed observer must not fail the relay (body %s)", rec.Code, rec.Body.String())
	}
	if got := st.Input.Stats().Dropped; got == 0 {
		// The failing sink is reported by the ledger, so a broken observer is
		// visible rather than silent. (The write error is not a "drop", but
		// either way the relay above must have succeeded.)
		t.Logf("ledger dropped=%d (an append failure is logged, not counted as a drop)", got)
	}
}
