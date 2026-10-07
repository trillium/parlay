package inputlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateRejectsUnexplainedFailure(t *testing.T) {
	for _, class := range []string{ClassRecogniserError, ClassLowConfidence, ClassNoMatch, ClassRefused, ClassUnpicked, ClassSuperseded, ClassHeld} {
		e := Event{InputID: "m1", Stage: StageRouted, Class: class}
		if err := e.Validate(); err == nil {
			t.Errorf("class %q with no reason validated; an unexplained failure must be a defect", class)
		}
	}
	ok := Event{InputID: "m1", Stage: StageRouted, Class: ClassOK}
	if err := ok.Validate(); err != nil {
		t.Errorf("ClassOK validated with error %v, want nil", err)
	}
}

func TestValidateClosedVocabulary(t *testing.T) {
	cases := []struct {
		name string
		e    Event
	}{
		{"empty input id", Event{Stage: StageReceived, Class: ClassOK}},
		{"unknown stage", Event{InputID: "m1", Stage: "arrived", Class: ClassOK}},
		{"unknown class", Event{InputID: "m1", Stage: StageReceived, Class: "probably-fine"}},
		{"confidence out of range", Event{InputID: "m1", Stage: StageInterpreted, Class: ClassOK, Confidence: f(1.4)}},
		{"negative threshold", Event{InputID: "m1", Stage: StageInterpreted, Class: ClassOK, Threshold: f(-0.1)}},
		{"low confidence without threshold", Event{InputID: "m1", Stage: StageInterpreted, Class: ClassLowConfidence, Reason: "below", Confidence: f(0.2)}},
	}
	for _, c := range cases {
		if err := c.e.Validate(); err == nil {
			t.Errorf("%s: validated, want rejection", c.name)
		}
	}
	good := Event{InputID: "m1", Stage: StageInterpreted, Class: ClassLowConfidence, Reason: "below", Confidence: f(0.2), Threshold: f(0.8)}
	if err := good.Validate(); err != nil {
		t.Errorf("well-formed low-confidence event rejected: %v", err)
	}
}

func TestRecordRejectsMalformedAndCountsIt(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "input.jsonl"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	l.Record(Event{InputID: "m1", Stage: StageRouted, Class: ClassRefused}) // no reason
	if got := l.Stats().Rejected; got != 1 {
		t.Errorf("Rejected = %d, want 1", got)
	}
	if got := len(l.Events()); got != 0 {
		t.Errorf("retained %d events, want 0 — a malformed event must not be stored", got)
	}
}

func TestLedgerRoundTripsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.jsonl")
	l, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	l.Record(Event{InputID: "m1", Stage: StageQueued, Class: ClassOK, Source: "send", Channel: "c0"})
	l.Record(Event{InputID: "m1", Stage: StageDelivered, Class: ClassOK, Source: "poll-wake", Channel: "c0"})
	l.Close()

	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer reopened.Close()

	events := reopened.Events()
	if len(events) != 2 {
		t.Fatalf("after restart retained %d events, want 2", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Errorf("seqs = %d,%d, want 1,2 (monotonic across restart)", events[0].Seq, events[1].Seq)
	}
	if events[0].InputID != "m1" || events[0].Stage != StageQueued {
		t.Errorf("event 0 = %+v, want the queued hop of m1", events[0])
	}
	// The next write continues the sequence rather than colliding with 1/2.
	reopened.Record(Event{InputID: "m2", Stage: StageQueued, Class: ClassOK})
	deadline := time.Now().Add(2 * time.Second)
	for len(reopened.Events()) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := reopened.Events()[2].Seq; got != 3 {
		t.Errorf("next seq = %d, want 3", got)
	}
}

func TestRingIsBoundedButFileKeepsEveryLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.jsonl")
	l, err := Open(path, Options{MaxEvents: 3})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 10; i++ {
		l.Record(Event{InputID: "m1", Stage: StageQueued, Class: ClassOK})
	}
	l.Close()

	reopened, err := Open(path, Options{MaxEvents: 3})
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer reopened.Close()
	if got := len(reopened.Events()); got != 3 {
		t.Errorf("retained %d, want the 3 newest", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := strings.Count(strings.TrimRight(string(raw), "\n"), "\n") + 1; got != 10 {
		t.Errorf("file holds %d lines, want 10 — the file is the durable record, the ring the working set", got)
	}
}

func TestEventsForNarrowsToOneInput(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "input.jsonl"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	l.Record(Event{InputID: "a", Stage: StageQueued, Class: ClassOK})
	l.Record(Event{InputID: "b", Stage: StageQueued, Class: ClassOK})
	l.Record(Event{InputID: "a", Stage: StageDelivered, Class: ClassOK})

	deadline := time.Now().Add(2 * time.Second)
	for len(l.Events()) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := l.EventsFor("a")
	if len(got) != 2 {
		t.Fatalf("EventsFor(a) returned %d hops, want 2", len(got))
	}
	for _, e := range got {
		if e.InputID != "a" {
			t.Errorf("EventsFor(a) returned a hop for %q", e.InputID)
		}
	}
	if len(l.EventsFor("nope")) != 0 {
		t.Error("EventsFor(unknown) returned hops; an unknown id must be an honest empty answer")
	}
}

// TestRecordNeverBlocksOnAWedgedSink is the load-bearing test for the hard
// constraint that observability must never sit in the delivery path: the
// sink here never returns, and Record must still return immediately.
func TestRecordNeverBlocksOnAWedgedSink(t *testing.T) {
	release := make(chan struct{})

	l, err := Open(filepath.Join(t.TempDir(), "input.jsonl"), Options{
		Queue:    4,
		Appender: func([]byte) error { <-release; return nil },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	defer close(release) // runs first, so the deferred Close can finish

	start := time.Now()
	for i := 0; i < 50000; i++ {
		l.Record(Event{InputID: "m1", Stage: StageQueued, Class: ClassOK})
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("50000 Record calls against a wedged sink took %v — Record is blocking the caller", elapsed)
	}
	stats := l.Stats()
	if stats.Dropped == 0 {
		t.Error("Dropped = 0 with a wedged sink; a full queue must shed rather than block")
	}
	// Every event is either dropped, queued, or in the writer's hands. One
	// write is in flight and the rest of the queue is full, so allow for
	// that handful and no more: the ledger must not lose records silently.
	accounted := int(stats.Dropped) + stats.Queue + int(stats.Written)
	if accounted < 50000-8 {
		t.Errorf("stats account for %d of 50000 events — records vanished uncounted", accounted)
	}
}

func TestFailingSinkIsCountedAndSurvivable(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "input.jsonl"), Options{
		Appender: func([]byte) error { return os.ErrPermission },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	l.Record(Event{InputID: "m1", Stage: StageQueued, Class: ClassOK})
	// A failed append must not panic, must not block, and must not corrupt
	// the caller's view: the event is simply absent, and Written stays 0.
	time.Sleep(50 * time.Millisecond)
	if got := l.Stats().Written; got != 0 {
		t.Errorf("Written = %d after every append failed, want 0", got)
	}
}

func TestNewInputIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewInputID()
		if id == "" {
			t.Fatal("NewInputID returned an empty id")
		}
		if seen[id] {
			t.Fatalf("NewInputID returned %q twice", id)
		}
		seen[id] = true
	}
}

func f(v float64) *float64 { return &v }
