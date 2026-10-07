// Handler tests for the dictation intake's ledger hops: the seam where the
// operator's phone voice input actually lands, and where a held submission, a
// transcript that never arrived, and a successful injection used to leave the
// same silence.
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/remoteinput"
	"parlay/go-server/internal/store"
)

// newTestStoreWithThreshold opens a store whose ledger holds input below min.
func newTestStoreWithThreshold(t *testing.T, min float64) *store.Store {
	t.Helper()
	st, err := store.Open(store.Config{Dir: t.TempDir(), MinInputConfidence: &min})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// remoteInputHarness wires the submit handler the way registerRemoteInput
// does — accept hook and settle callback both recording into the ledger —
// without the live Talon REPL or an SSE hub.
func remoteInputHarness(t *testing.T, st *store.Store, talon *remoteinput.FakeTalon) (http.HandlerFunc, http.HandlerFunc, *remoteinput.Service) {
	t.Helper()
	seam := remoteInputSeam{st: st}
	svc := remoteinput.NewService(talon, 0, func(o remoteinput.Outcome) {
		if o.Terminal() {
			seam.settled(o)
		}
	})
	svc.SetOnAccepted(seam.intake)
	t.Cleanup(svc.Stop)
	return handleRemoteInputSubmit(svc, st), handleRemoteInputStatus(svc), svc
}

// postRemoteInput posts a submit body and returns the 202/4xx recorder.
func postRemoteInput(t *testing.T, submit http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/remote-input/submit", strings.NewReader(body))
	rec := httptest.NewRecorder()
	submit(rec, req)
	return rec
}

// submitID reads the 202 body's submission id.
func submitID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", rec.Body.String(), err)
	}
	if resp.ID == "" {
		t.Fatalf("submit response has no id: %s", rec.Body.String())
	}
	return resp.ID
}

// hopsFor returns every retained hop of one input once at least n events
// exist overall.
func hopsFor(t *testing.T, st *store.Store, id string, n int) []inputlog.Event {
	t.Helper()
	waitForEvents(t, st, n)
	var out []inputlog.Event
	for _, e := range st.Input.EventsFor(id) {
		out = append(out, e)
	}
	return out
}

func findHop(hops []inputlog.Event, stage string) (inputlog.Event, bool) {
	for _, e := range hops {
		if e.Stage == stage {
			return e, true
		}
	}
	return inputlog.Event{}, false
}

func TestRemoteInputRecordsReceivedThenDelivered(t *testing.T) {
	st := newTestStore(t)
	talon := &remoteinput.FakeTalon{}
	submit, status, _ := remoteInputHarness(t, st, talon)

	rec := postRemoteInput(t, submit, `{"device":"phone-1","text":"ship it","app":"Terminal"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit = %d %s, want 202", rec.Code, rec.Body.String())
	}
	id := submitID(t, rec)
	if o := waitRemoteOutcome(t, status, id); o.Status != remoteinput.StatusInjected {
		t.Fatalf("outcome = %s, want injected", o.Status)
	}

	hops := hopsFor(t, st, id, 2)
	rcv, ok := findHop(hops, inputlog.StageReceived)
	if !ok {
		t.Fatalf("no received hop: %+v", hops)
	}
	if rcv.Source != inputSourceRemoteInput || rcv.Class != inputlog.ClassOK {
		t.Errorf("received hop = %s/%s source=%q, want ok with source %q",
			rcv.Stage, rcv.Class, rcv.Source, inputSourceRemoteInput)
	}
	dlv, ok := findHop(hops, inputlog.StageDelivered)
	if !ok {
		t.Fatalf("no delivered hop: %+v", hops)
	}
	if dlv.Class != inputlog.ClassOK {
		t.Errorf("delivered class = %q, want ok", dlv.Class)
	}
	if len(hops) != 2 {
		t.Errorf("hops = %d, want 2 (received, delivered): %+v", len(hops), hops)
	}
}

func TestRemoteInputEmptyTranscriptIsARecogniserError(t *testing.T) {
	st := newTestStore(t)
	submit, _, _ := remoteInputHarness(t, st, &remoteinput.FakeTalon{})

	rec := postRemoteInput(t, submit, `{"device":"phone-1","text":"","app":"Terminal"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("submit = %d, want 400", rec.Code)
	}
	ev := waitForEvents(t, st, 1)[0]
	if ev.Class != inputlog.ClassRecogniserError {
		t.Errorf("class = %q, want %q — an empty transcript is the recogniser failing, not a missing message",
			ev.Class, inputlog.ClassRecogniserError)
	}
	if ev.Reason != reasonEmptyTranscript {
		t.Errorf("reason = %q, want %q", ev.Reason, reasonEmptyTranscript)
	}
	if ev.InputID == "" {
		t.Error("a recogniser error must carry a ledger-local id so it can be replayed")
	}
}

func TestRemoteInputMissingDeviceIsRecordedAsRefused(t *testing.T) {
	st := newTestStore(t)
	submit, _, _ := remoteInputHarness(t, st, &remoteinput.FakeTalon{})

	if rec := postRemoteInput(t, submit, `{"text":"ship it"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("submit = %d, want 400", rec.Code)
	}
	ev := waitForEvents(t, st, 1)[0]
	if ev.Class != inputlog.ClassRefused || ev.Reason != reasonMissingDevice {
		t.Errorf("hop = %s/%q, want refused with reason %q", ev.Class, ev.Reason, reasonMissingDevice)
	}
}

func TestRemoteInputFocusMismatchIsNoMatch(t *testing.T) {
	st := newTestStore(t)
	// The focus request is accepted but the OS keeps another app active.
	talon := &remoteinput.FakeTalon{ActiveAppName: "Finder", StickyActive: true}
	submit, status, _ := remoteInputHarness(t, st, talon)

	id := submitID(t, postRemoteInput(t, submit, `{"device":"phone-1","text":"ship it","app":"Terminal"}`))
	if o := waitRemoteOutcome(t, status, id); o.Status != remoteinput.StatusFocusFailed {
		t.Fatalf("outcome = %s, want focus_failed", o.Status)
	}

	hops := hopsFor(t, st, id, 2)
	ev, ok := findHop(hops, inputlog.StageRouted)
	if !ok {
		t.Fatalf("no routed hop: %+v", hops)
	}
	if ev.Class != inputlog.ClassNoMatch || ev.Reason != reasonTargetNotMatched {
		t.Errorf("hop = %s/%q, want no_match with reason %q", ev.Class, ev.Reason, reasonTargetNotMatched)
	}
	if _, delivered := findHop(hops, inputlog.StageDelivered); delivered {
		t.Error("a focus mismatch must not record a delivered hop")
	}
}

func TestRemoteInputInjectFailureIsRefused(t *testing.T) {
	st := newTestStore(t)
	talon := &remoteinput.FakeTalon{InsertErr: remoteinput.ErrInsertTransport}
	submit, status, _ := remoteInputHarness(t, st, talon)

	id := submitID(t, postRemoteInput(t, submit, `{"device":"phone-1","text":"ship it","app":"Terminal"}`))
	if o := waitRemoteOutcome(t, status, id); o.Status != remoteinput.StatusInjectFailed {
		t.Fatalf("outcome = %s, want inject_failed", o.Status)
	}
	ev, ok := findHop(hopsFor(t, st, id, 2), inputlog.StageDelivered)
	if !ok {
		t.Fatalf("no delivered hop: %+v", st.Input.EventsFor(id))
	}
	if ev.Class != inputlog.ClassRefused || ev.Reason != reasonInjectFailed {
		t.Errorf("hop = %s/%q, want refused with reason %q", ev.Class, ev.Reason, reasonInjectFailed)
	}
}
