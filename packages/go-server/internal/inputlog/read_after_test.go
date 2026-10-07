// Cursor reads: the read a live tail follows. The rule under test is not just
// "returns the newer hops" but "the answer says where it starts", because that
// is the only thing that lets a reader tell "nothing new" from "I fell off the
// end of the retained window".
package inputlog

import (
	"path/filepath"
	"testing"
	"time"
)

// recordN writes n hops synchronously (Close flushes the queue) into a fresh
// ledger and returns it reopened, so Seq is stable and the ring is loaded.
func recordN(t *testing.T, n int) *Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.jsonl")
	l, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < n; i++ {
		l.Record(Event{InputID: "m" + string(rune('a'+i)), Stage: StageQueued, Class: ClassOK, Source: "send"})
	}
	l.Close()

	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(reopened.Close)
	if got := len(reopened.Events()); got != n {
		t.Fatalf("reopened ledger holds %d hops, want %d", got, n)
	}
	return reopened
}

func TestEventsAfterReadsForwardFromACursor(t *testing.T) {
	l := recordN(t, 3)
	all := l.Events()

	got := l.EventsAfter(all[0].Seq)
	if len(got) != 2 {
		t.Fatalf("EventsAfter(seq 1) returned %d hops, want 2", len(got))
	}
	if got[0].Seq != all[1].Seq || got[1].Seq != all[2].Seq {
		t.Errorf("EventsAfter returned seqs %d,%d, want %d,%d — and oldest first",
			got[0].Seq, got[1].Seq, all[1].Seq, all[2].Seq)
	}

	// At the live edge there is nothing newer, and that is an honest empty
	// answer rather than an error.
	if got := l.EventsAfter(all[2].Seq); len(got) != 0 {
		t.Errorf("EventsAfter(newest) returned %d hops, want none", len(got))
	}
	// Seq 0 is "from the beginning of what is retained".
	if got := l.EventsAfter(0); len(got) != 3 {
		t.Errorf("EventsAfter(0) returned %d hops, want all 3", len(got))
	}
}

// TestEventsAfterAnEvictedCursorReturnsWhatIsLeft is the honesty half: when
// the cursor is older than the retained window the reader must still get the
// oldest hops that ARE retained, so it can see from their Seq that a span was
// evicted. Returning nothing would make an eviction look like calm.
func TestEventsAfterAnEvictedCursorReturnsWhatIsLeft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.jsonl")
	l, err := Open(path, Options{MaxEvents: 2})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(l.Close)
	for i := 0; i < 5; i++ {
		l.Record(Event{InputID: "m", Stage: StageQueued, Class: ClassOK, Source: "send"})
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(l.Events()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	got := l.EventsAfter(1)
	if len(got) != 2 {
		t.Fatalf("EventsAfter(1) on a 2-event ring returned %d hops, want 2", len(got))
	}
	// Nothing about hop 2 exists any more. The reader can only know that
	// because the first hop it gets is seq 4, not seq 2.
	if got[0].Seq != 4 {
		t.Fatalf("oldest retained seq = %d, want 4 (hops 2–3 evicted); the reader needs this to detect the gap", got[0].Seq)
	}
}

func TestStatsReportTheNewestSeq(t *testing.T) {
	if got := (&Log{}).Stats().NewestSeq; got != 0 {
		t.Errorf("empty ledger newest seq = %d, want 0", got)
	}
	l := recordN(t, 3)
	if got := l.Stats().NewestSeq; got != 3 {
		t.Errorf("newest seq after 3 hops = %d, want 3", got)
	}
}

// TestStatsDoesNotWaitOnAWedgedSink pins why lastSeq is an atomic rather than
// a mutex-guarded field. The live tail asks where the ledger is on every poll;
// if that answer waited behind the writer's disk append, the one view that
// exists to watch a stalled seam would itself stall — and a wedged sink is
// precisely the incident it is running for.
func TestStatsDoesNotWaitOnAWedgedSink(t *testing.T) {
	inSink := make(chan struct{}, 1)
	block := make(chan struct{})
	l, err := Open(filepath.Join(t.TempDir(), "input.jsonl"), Options{
		Queue: 1,
		Appender: func([]byte) error {
			select {
			case inSink <- struct{}{}:
			default:
			}
			<-block
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { close(block) })
	t.Cleanup(l.Close)

	l.Record(Event{InputID: "m1", Stage: StageQueued, Class: ClassOK})
	select {
	case <-inSink:
	case <-time.After(2 * time.Second):
		t.Fatal("the writer never reached the sink; nothing is being tested")
	}

	done := make(chan uint64, 1)
	go func() { done <- l.Stats().NewestSeq }()
	select {
	case newest := <-done:
		if newest != 1 {
			t.Errorf("newest seq behind a wedged sink = %d, want 1 (Seq is assigned before the append)", newest)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stats blocked behind a wedged disk write")
	}
}
