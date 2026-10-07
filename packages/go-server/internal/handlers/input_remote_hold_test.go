// The hold and non-interference halves of the dictation-intake tests:
// what the confidence threshold does (and deliberately does not do), and
// the proof that recording it never sits in the delivery path. The harness
// helpers live in input_remote_test.go.
package handlers

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/remoteinput"
)

func TestRemoteInputHoldsLowConfidenceWithoutTyping(t *testing.T) {
	st := newTestStoreWithThreshold(t, 0.80)
	talon := &remoteinput.FakeTalon{}
	submit, status, _ := remoteInputHarness(t, st, talon)

	rec := postRemoteInput(t, submit, `{"device":"phone-1","text":"ship it maybe","app":"Terminal","confidence":0.30}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit = %d %s, want 202 held", rec.Code, rec.Body.String())
	}
	id := submitID(t, rec)
	var resp remoteinput.SubmitResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != remoteinput.StatusHeld {
		t.Errorf("submit status = %q, want %q — a held message says it is held", resp.Status, remoteinput.StatusHeld)
	}
	o := waitRemoteOutcome(t, status, id)
	if o.Status != remoteinput.StatusHeld || !o.PreserveText {
		t.Errorf("outcome = %s preserveText=%v, want held with the text preserved", o.Status, o.PreserveText)
	}
	if len(talon.Inserts) != 0 {
		t.Errorf("Talon inserts = %v, want none: a held submission is never typed", talon.Inserts)
	}

	hops := hopsFor(t, st, id, 3)
	low, ok := findHop(hops, inputlog.StageInterpreted)
	if !ok {
		t.Fatalf("no interpreted hop: %+v", hops)
	}
	if low.Class != inputlog.ClassLowConfidence || low.Confidence == nil || low.Threshold == nil {
		t.Fatalf("interpreted hop = %+v, want low_confidence carrying confidence and threshold", low)
	}
	if *low.Confidence != 0.30 || *low.Threshold != 0.80 {
		t.Errorf("confidence/threshold = %v/%v, want 0.30/0.80", *low.Confidence, *low.Threshold)
	}
	held, ok := findHop(hops, inputlog.StageHeld)
	if !ok {
		t.Fatalf("no held hop: %+v", hops)
	}
	if held.Class != inputlog.ClassHeld || held.Reason != inputlog.ReasonBelowConfidence {
		t.Errorf("held hop = %s/%q, want held with reason %q", held.Class, held.Reason, inputlog.ReasonBelowConfidence)
	}
	if _, delivered := findHop(hops, inputlog.StageDelivered); delivered {
		t.Error("a held input must not also record a delivered hop")
	}
}

func TestRemoteInputConfidentEnoughIsNotHeld(t *testing.T) {
	st := newTestStoreWithThreshold(t, 0.80)
	talon := &remoteinput.FakeTalon{}
	submit, status, _ := remoteInputHarness(t, st, talon)

	id := submitID(t, postRemoteInput(t, submit,
		`{"device":"phone-1","text":"ship it","app":"Terminal","confidence":0.95}`))
	if o := waitRemoteOutcome(t, status, id); o.Status != remoteinput.StatusInjected {
		t.Fatalf("outcome = %s, want injected", o.Status)
	}
	if len(talon.Inserts) != 1 {
		t.Errorf("Talon inserts = %v, want the one submission typed", talon.Inserts)
	}
	ev, ok := findHop(hopsFor(t, st, id, 3), inputlog.StageInterpreted)
	if !ok {
		t.Fatalf("a reported confidence must be recorded: %+v", st.Input.EventsFor(id))
	}
	if ev.Class != inputlog.ClassOK || ev.Confidence == nil || *ev.Confidence != 0.95 {
		t.Errorf("interpreted hop = %+v, want ok carrying confidence 0.95", ev)
	}
}

// The dictation seam's half of the hard constraint that observability never
// sits in the delivery path: the ledger's sink never returns, and the
// submission must still be accepted, still reach Talon, and still settle.
func TestRemoteInputDeliveryIsNotSlowedOrFailedByAWedgedLedger(t *testing.T) {
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

	talon := &remoteinput.FakeTalon{}
	submit, status, _ := remoteInputHarness(t, st, talon)
	start := time.Now()
	rec := postRemoteInput(t, submit, `{"device":"phone-1","text":"ship it","app":"Terminal"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit with a wedged ledger = %d %s, want 202", rec.Code, rec.Body.String())
	}
	id := submitID(t, rec)
	if o := waitRemoteOutcome(t, status, id); o.Status != remoteinput.StatusInjected {
		t.Fatalf("outcome = %s, want injected — a wedged observer must not fail the delivery", o.Status)
	}
	if len(talon.Inserts) != 1 {
		t.Errorf("Talon inserts = %v, want the one submission typed", talon.Inserts)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("submit took %v with a wedged ledger — recording must not be waited on", elapsed)
	}
}

// The asymmetry the threshold is built on: absence is never a hold. A
// deployment that set a threshold must not start refusing every surface that
// cannot report a confidence — which today is all of them.
func TestRemoteInputWithoutReportedConfidenceIsNeverHeld(t *testing.T) {
	st := newTestStoreWithThreshold(t, 0.99)
	talon := &remoteinput.FakeTalon{}
	submit, status, _ := remoteInputHarness(t, st, talon)

	id := submitID(t, postRemoteInput(t, submit, `{"device":"phone-1","text":"ship it","app":"Terminal"}`))
	if o := waitRemoteOutcome(t, status, id); o.Status != remoteinput.StatusInjected {
		t.Fatalf("outcome = %s, want injected: an unreported confidence is not a low one", o.Status)
	}
	if _, held := findHop(hopsFor(t, st, id, 2), inputlog.StageHeld); held {
		t.Error("an input with no reported confidence must never be held")
	}
}
