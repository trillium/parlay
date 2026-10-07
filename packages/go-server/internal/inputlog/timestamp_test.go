// Whose clock stamps a hop is a correctness property for the latency half of
// the view, not a detail.
//
// The ledger's writer is a single goroutine behind a bounded queue, so a burst
// can delay it. A timestamp taken there would charge that delay to the stage
// BEFORE the hop, turning a ledger hiccup into an apparently slow relay — and
// the operator would go looking at the seam, which is exactly what this ledger
// exists to make unnecessary.
package inputlog

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRecordStampsAHopWhenItIsObservedNotWhenItIsWritten(t *testing.T) {
	release := make(chan struct{})
	wrote := make(chan []byte, 1)
	l, err := Open(t.TempDir()+"/input.jsonl", Options{
		Queue: 4,
		// Park the writer inside its sink: the event is recorded, then sits
		// in the writer's hands while the clock advances.
		Appender: func(b []byte) error { <-release; wrote <- append([]byte(nil), b...); return nil },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	recordedAt := time.Now()
	l.Record(Event{InputID: "m1", Stage: StageQueued, Class: ClassOK})

	// The writer is wedged in the sink for this long. If the timestamp came
	// from it, the hop would read as at least this old.
	time.Sleep(80 * time.Millisecond)
	releaseAt := time.Now()
	close(release)

	var line []byte
	select {
	case line = <-wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("the writer never reached its sink")
	}
	var got Event
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("persisted line is not an event: %v\n%s", err, line)
	}
	ts, ok := parseTs(got.Ts)
	if !ok {
		t.Fatalf("persisted ts %q does not parse", got.Ts)
	}
	if !ts.Before(releaseAt) {
		t.Errorf("ts %s is at/after the moment the wedged sink was released (%s): the hop was stamped by the WRITER, so a delayed drain would read as a slow seam",
			ts.Format(time.RFC3339Nano), releaseAt.Format(time.RFC3339Nano))
	}
	if ts.Before(recordedAt.Add(-time.Second)) {
		t.Errorf("ts %s predates the Record call (%s)", ts, recordedAt)
	}
}

// The fallback path stays intact: an event that never went through Record (a
// caller that went straight to the writer) still gets a timestamp.
func TestWriteStillStampsAnEventRecordNeverSaw(t *testing.T) {
	var line []byte
	l, err := Open(t.TempDir()+"/input.jsonl", Options{
		Appender: func(b []byte) error { line = append([]byte(nil), b...); return nil },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	before := time.Now()
	l.write(Event{InputID: "m1", Stage: StageQueued, Class: ClassOK})
	after := time.Now()

	var got Event
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("persisted line is not an event: %v\n%s", err, line)
	}
	ts, ok := parseTs(got.Ts)
	if !ok {
		t.Fatalf("persisted ts %q does not parse", got.Ts)
	}
	if ts.Before(before.Add(-time.Second)) || ts.After(after.Add(time.Second)) {
		t.Errorf("ts %s is not from around the write (%s..%s)", ts, before, after)
	}
}

func parseTs(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}
