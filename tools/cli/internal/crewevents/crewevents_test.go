// The invariants that make this log a safe §7.1 mitigation: sequence numbers
// are monotonic and race-free (the blocking flock), every failure comes back
// to the caller, and a torn write can neither corrupt a later append nor
// silently vanish from a read.
package crewevents

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func tempLog(t *testing.T) string {
	t.Helper()
	return File(filepath.Join(t.TempDir(), "agent-1"))
}

func TestAppendAssignsMonotonicSeqFromOne(t *testing.T) {
	file := tempLog(t)
	for i, verb := range []string{"working", "blocked", "done"} {
		ev, err := Append(file, Event{At: "2026-08-31T00:00:00Z", Name: EventCrewStatus, Agent: "agent-1", Verb: verb})
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if ev.Seq != uint64(i+1) {
			t.Errorf("Append %d assigned seq %d, want %d", i, ev.Seq, i+1)
		}
	}

	evs, skipped, err := ReadAfter(file, 0)
	if err != nil || skipped != 0 {
		t.Fatalf("ReadAfter: evs err %v, skipped %d", err, skipped)
	}
	if len(evs) != 3 || evs[0].Verb != "working" || evs[2].Verb != "done" {
		t.Errorf("ReadAfter(0) = %+v, want the 3 events in write order", evs)
	}
	if evs[1].Name != EventCrewStatus || evs[1].Agent != "agent-1" {
		t.Errorf("event fields did not round-trip: %+v", evs[1])
	}
}

func TestReadAfterIsAnExclusiveCursor(t *testing.T) {
	file := tempLog(t)
	for _, verb := range []string{"working", "working", "needs-decision"} {
		if _, err := Append(file, Event{Name: EventCrewStatus, Agent: "a", Verb: verb}); err != nil {
			t.Fatal(err)
		}
	}
	evs, _, err := ReadAfter(file, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Seq != 3 || evs[0].Verb != "needs-decision" {
		t.Errorf("ReadAfter(2) = %+v, want only seq 3", evs)
	}
}

func TestMissingLogReadsAsEmptyNotError(t *testing.T) {
	file := File(filepath.Join(t.TempDir(), "never-wrote"))
	if evs, skipped, err := ReadAfter(file, 0); err != nil || evs != nil || skipped != 0 {
		t.Errorf("ReadAfter on missing file = (%v, %d, %v), want empty and nil", evs, skipped, err)
	}
	if seq, err := LatestSeq(file); err != nil || seq != 0 {
		t.Errorf("LatestSeq on missing file = (%d, %v), want (0, nil)", seq, err)
	}
}

func TestLatestSeqTracksHead(t *testing.T) {
	file := tempLog(t)
	for i := 0; i < 4; i++ {
		if _, err := Append(file, Event{Name: EventCrewStatus, Agent: "a", Verb: "working"}); err != nil {
			t.Fatal(err)
		}
	}
	if seq, err := LatestSeq(file); err != nil || seq != 4 {
		t.Errorf("LatestSeq = (%d, %v), want (4, nil)", seq, err)
	}
}

// The §7.1 point: unlike FileRecorder.Record, a write that cannot land is an
// ERROR the caller sees, never a silent drop.
func TestAppendReturnsItsFailures(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(filepath.Join(blocker, "agent", "events.jsonl"), Event{Verb: "working"}); err == nil {
		t.Fatal("Append under an unwritable path returned nil — that is the silent drop this package exists to prevent")
	}
}

// A crash can leave a newline-less fragment at EOF. The next Append must
// terminate it so the new line never concatenates onto it, and readers must
// treat the (now complete, unparseable) fragment as counted garbage — never
// as a reason to lose the events around it.
func TestTornTrailingWriteIsFencedOffAndCounted(t *testing.T) {
	file := tempLog(t)
	if _, err := Append(file, Event{Name: EventCrewStatus, Agent: "a", Verb: "working"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":2,"verb":"blo`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// The fragment is invisible to reads while torn…
	evs, skipped, err := ReadAfter(file, 0)
	if err != nil || len(evs) != 1 || skipped != 0 {
		t.Fatalf("ReadAfter with torn tail = (%d evs, %d skipped, %v), want (1, 0, nil)", len(evs), skipped, err)
	}

	// …and the next Append fences it with a newline instead of gluing on.
	ev, err := Append(file, Event{Name: EventCrewStatus, Agent: "a", Verb: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Seq != 2 {
		t.Errorf("post-torn Append seq = %d, want 2 (fragment never claimed a seq)", ev.Seq)
	}
	evs, skipped, err = ReadAfter(file, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[1].Verb != "done" || evs[1].Seq != 2 {
		t.Errorf("post-fence ReadAfter = %+v, want the 2 real events", evs)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 (the fenced fragment is counted, not silently dropped)", skipped)
	}
	data, _ := os.ReadFile(file)
	if strings.Contains(string(data), `"blo{`) || strings.Contains(string(data), `"blo"`) {
		t.Errorf("fragment appears to have merged with a later line:\n%s", data)
	}
}

// The blocking flock is what makes concurrent writers safe on one agent's
// file: every append gets a distinct, gapless seq.
func TestConcurrentAppendsGetDistinctSeqs(t *testing.T) {
	file := tempLog(t)
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Append(file, Event{Name: EventCrewStatus, Agent: "a", Verb: "working"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Append: %v", err)
		}
	}

	evs, skipped, err := ReadAfter(file, 0)
	if err != nil || skipped != 0 {
		t.Fatalf("ReadAfter: %v, skipped %d", err, skipped)
	}
	seen := map[uint64]bool{}
	for _, ev := range evs {
		seen[ev.Seq] = true
	}
	if len(evs) != n || len(seen) != n {
		t.Fatalf("got %d events, %d distinct seqs, want %d of each", len(evs), len(seen), n)
	}
	for i := uint64(1); i <= n; i++ {
		if !seen[i] {
			t.Errorf("seq %d missing — sequence must be gapless 1..%d", i, n)
		}
	}
}
