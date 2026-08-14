package store

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is the injectable clock every lifecycle test here drives by hand —
// reaping is defined in terms of elapsed time, and a test that slept for it
// would be both slow and flaky.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestRegistry(clock *fakeClock) *CommandRegistry {
	return NewCommandRegistry(CommandRegistryConfig{
		Now:        clock.Now,
		StaleAfter: 90 * time.Second,
		RetainDone: 60 * time.Second,
	})
}

func findCommand(list []CommandInvocation, id string) (CommandInvocation, bool) {
	for _, rec := range list {
		if rec.ID == id {
			return rec, true
		}
	}
	return CommandInvocation{}, false
}

// TestStartedCommandAppears is the first required lifecycle case: a command
// that starts is visible as running.
func TestStartedCommandAppears(t *testing.T) {
	clock := newFakeClock()
	cr := newTestRegistry(clock)

	rec, created := cr.Start(CommandStart{ID: "inv-1", Verb: "listen", Agent: "scout", PID: 4242})
	if !created {
		t.Fatal("Start reported no new record")
	}
	if rec.State != CommandRunning {
		t.Fatalf("state = %q, want %q", rec.State, CommandRunning)
	}

	list := cr.List()
	got, ok := findCommand(list, "inv-1")
	if !ok {
		t.Fatalf("started command missing from List(): %+v", list)
	}
	if got.Verb != "listen" || got.Agent != "scout" || got.PID != 4242 {
		t.Fatalf("record fields not preserved: %+v", got)
	}
	if cr.Running() != 1 {
		t.Fatalf("Running() = %d, want 1", cr.Running())
	}
}

// TestFinishedCommandLeaves is the second required lifecycle case: a command
// that reports its end goes terminal immediately and drops out of the
// registry entirely once the retention window passes.
func TestFinishedCommandLeaves(t *testing.T) {
	clock := newFakeClock()
	cr := newTestRegistry(clock)

	cr.Start(CommandStart{ID: "inv-1", Verb: "send"})
	clock.Advance(2 * time.Second)

	code := 0
	rec, ok := cr.End(CommandEnd{ID: "inv-1", State: CommandFinished, ExitCode: &code, Outcome: "ok"})
	if !ok {
		t.Fatal("End reported no transition")
	}
	if rec.State != CommandFinished {
		t.Fatalf("state = %q, want %q", rec.State, CommandFinished)
	}
	if rec.DurationMs != 2000 {
		t.Fatalf("durationMs = %d, want 2000", rec.DurationMs)
	}
	if cr.Running() != 0 {
		t.Fatalf("Running() = %d, want 0 after end", cr.Running())
	}

	// Still briefly visible, so "it just finished" is readable.
	if _, ok := findCommand(cr.List(), "inv-1"); !ok {
		t.Fatal("finished command should linger for the retention window")
	}

	clock.Advance(61 * time.Second)
	_, dropped := cr.Sweep()
	if len(dropped) != 1 || dropped[0] != "inv-1" {
		t.Fatalf("dropped = %v, want [inv-1]", dropped)
	}
	if _, ok := findCommand(cr.List(), "inv-1"); ok {
		t.Fatal("finished command still present after retention window")
	}
}

// TestAbandonedCommandIsReaped is the third required lifecycle case: a
// command that dies without reporting (SIGKILL, crash, sleeping laptop) must
// not leak a permanently "running" entry.
func TestAbandonedCommandIsReaped(t *testing.T) {
	clock := newFakeClock()
	cr := newTestRegistry(clock)

	cr.Start(CommandStart{ID: "zombie", Verb: "monitor"})

	// Just under the staleness bound: still trusted.
	clock.Advance(89 * time.Second)
	if expired, _ := cr.Sweep(); len(expired) != 0 {
		t.Fatalf("reaped too early: %+v", expired)
	}

	clock.Advance(2 * time.Second)
	expired, _ := cr.Sweep()
	if len(expired) != 1 || expired[0].ID != "zombie" {
		t.Fatalf("expired = %+v, want the zombie record", expired)
	}
	if expired[0].State != CommandExpired {
		t.Fatalf("state = %q, want %q", expired[0].State, CommandExpired)
	}
	if expired[0].Outcome != "no-heartbeat" {
		t.Fatalf("outcome = %q, want no-heartbeat", expired[0].Outcome)
	}
	if cr.Running() != 0 {
		t.Fatalf("Running() = %d, want 0 after reaping", cr.Running())
	}

	// Reaping is not repeated once a record is terminal.
	if expired, _ := cr.Sweep(); len(expired) != 0 {
		t.Fatalf("second sweep re-expired a terminal record: %+v", expired)
	}
}

func TestHeartbeatKeepsALongRunningCommandAlive(t *testing.T) {
	clock := newFakeClock()
	cr := newTestRegistry(clock)

	cr.Start(CommandStart{ID: "listener", Verb: "listen"})
	for i := 0; i < 20; i++ {
		clock.Advance(20 * time.Second)
		if _, ok := cr.Heartbeat("listener"); !ok {
			t.Fatalf("heartbeat %d rejected", i)
		}
		if expired, _ := cr.Sweep(); len(expired) != 0 {
			t.Fatalf("heartbeating command reaped at beat %d", i)
		}
	}
	if cr.Running() != 1 {
		t.Fatalf("Running() = %d, want 1", cr.Running())
	}
}

func TestHeartbeatForUnknownIDIsRejected(t *testing.T) {
	cr := newTestRegistry(newFakeClock())
	if _, ok := cr.Heartbeat("never-started"); ok {
		t.Fatal("heartbeat accepted for an unknown id")
	}
}

// A reporter is a separate process: its start and end POSTs can race. Neither
// ordering may lose the outcome.
func TestEndBeforeStartStillRecordsATerminalRecord(t *testing.T) {
	clock := newFakeClock()
	cr := newTestRegistry(clock)

	code := 1
	rec, ok := cr.End(CommandEnd{ID: "raced", State: CommandFailed, ExitCode: &code, Outcome: "error"})
	if !ok {
		t.Fatal("End dropped an unknown-id report")
	}
	if rec.State != CommandFailed {
		t.Fatalf("state = %q, want %q", rec.State, CommandFailed)
	}

	// The late start must not resurrect it as running.
	after, created := cr.Start(CommandStart{ID: "raced", Verb: "send"})
	if created {
		t.Fatal("late Start resurrected a terminal record")
	}
	if after.State != CommandFailed {
		t.Fatalf("state = %q after late start, want %q", after.State, CommandFailed)
	}
	if cr.Running() != 0 {
		t.Fatalf("Running() = %d, want 0", cr.Running())
	}
}

func TestRepeatedStartActsAsAHeartbeatNotADuplicate(t *testing.T) {
	clock := newFakeClock()
	cr := newTestRegistry(clock)

	first, _ := cr.Start(CommandStart{ID: "inv", Verb: "listen"})
	clock.Advance(80 * time.Second)
	second, _ := cr.Start(CommandStart{ID: "inv", Verb: "listen"})

	if len(cr.List()) != 1 {
		t.Fatalf("re-start created a duplicate: %+v", cr.List())
	}
	if second.StartedAt != first.StartedAt {
		t.Fatalf("startedAt moved on re-start: %q -> %q", first.StartedAt, second.StartedAt)
	}
	if second.UpdatedAt == first.UpdatedAt {
		t.Fatal("re-start did not refresh updatedAt")
	}
	clock.Advance(20 * time.Second)
	if expired, _ := cr.Sweep(); len(expired) != 0 {
		t.Fatalf("re-started command reaped: %+v", expired)
	}
}

func TestFirstTerminalVerdictWins(t *testing.T) {
	cr := newTestRegistry(newFakeClock())
	cr.Start(CommandStart{ID: "inv", Verb: "send"})

	cr.End(CommandEnd{ID: "inv", State: CommandFailed, Outcome: "error"})
	if _, ok := cr.End(CommandEnd{ID: "inv", State: CommandFinished, Outcome: "ok"}); ok {
		t.Fatal("a second End rewrote a terminal record")
	}
	rec, _ := findCommand(cr.List(), "inv")
	if rec.State != CommandFailed {
		t.Fatalf("state = %q, want the first verdict %q", rec.State, CommandFailed)
	}
}

// The redaction policy is enforced at the storage layer, not trusted to
// callers — this endpoint is unauthenticated like every other route here.
func TestFlagValuesAreNeverStored(t *testing.T) {
	cr := newTestRegistry(newFakeClock())
	rec, _ := cr.Start(CommandStart{
		ID:    "inv",
		Verb:  "send",
		Flags: []string{"--json", "sk-live-super-secret", "--token=abcdef", "--message", "hi there"},
	})

	for _, f := range rec.Flags {
		if !strings.HasPrefix(f, "-") {
			t.Fatalf("bare value token %q kept as a flag", f)
		}
		if strings.Contains(f, "=") {
			t.Fatalf("flag value survived in %q", f)
		}
	}
	want := []string{"--json", "--token", "--message"}
	if len(rec.Flags) != len(want) {
		t.Fatalf("flags = %v, want %v", rec.Flags, want)
	}
	for i, f := range want {
		if rec.Flags[i] != f {
			t.Fatalf("flags = %v, want %v", rec.Flags, want)
		}
	}
}

func TestHostileTokensAreSanitized(t *testing.T) {
	cr := newTestRegistry(newFakeClock())
	rec, _ := cr.Start(CommandStart{
		ID:      "inv-2",
		Verb:    "<script>alert(1)</script>",
		Agent:   "scout; rm -rf /",
		Channel: strings.Repeat("x", 500),
	})

	if strings.ContainsAny(rec.Verb, "<>/() ") {
		t.Fatalf("verb not sanitized: %q", rec.Verb)
	}
	if strings.ContainsAny(rec.Agent, "; /") {
		t.Fatalf("agent not sanitized: %q", rec.Agent)
	}
	if len(rec.Channel) > 64 {
		t.Fatalf("channel not length-capped: %d chars", len(rec.Channel))
	}
}

func TestStartWithNoUsableVerbIsLabelledUnknown(t *testing.T) {
	cr := newTestRegistry(newFakeClock())
	rec, created := cr.Start(CommandStart{ID: "inv", Verb: "!!!"})
	if !created || rec.Verb != "unknown" {
		t.Fatalf("verb = %q (created=%v), want %q", rec.Verb, created, "unknown")
	}
}

func TestStartWithNoUsableIDIsRejected(t *testing.T) {
	cr := newTestRegistry(newFakeClock())
	if _, ok := cr.Start(CommandStart{ID: "!!!", Verb: "send"}); ok {
		t.Fatal("Start accepted an unusable id")
	}
	if len(cr.List()) != 0 {
		t.Fatalf("registry not empty: %+v", cr.List())
	}
}

// A flood of terminal records must never push a live one out of the view.
func TestEvictionShedsTerminalRecordsFirst(t *testing.T) {
	clock := newFakeClock()
	cr := NewCommandRegistry(CommandRegistryConfig{Now: clock.Now, MaxRecords: 3})

	cr.Start(CommandStart{ID: "live", Verb: "listen"})
	for _, id := range []string{"a", "b", "c", "d"} {
		clock.Advance(time.Second)
		cr.Start(CommandStart{ID: id, Verb: "send"})
		cr.End(CommandEnd{ID: id, State: CommandFinished, Outcome: "ok"})
	}

	if _, ok := findCommand(cr.List(), "live"); !ok {
		t.Fatalf("running record evicted by terminal flood: %+v", cr.List())
	}
	if len(cr.List()) > 3 {
		t.Fatalf("registry grew past MaxRecords: %d", len(cr.List()))
	}
}

func TestListIsNewestFirst(t *testing.T) {
	clock := newFakeClock()
	cr := newTestRegistry(clock)

	cr.Start(CommandStart{ID: "old", Verb: "send"})
	clock.Advance(time.Second)
	cr.Start(CommandStart{ID: "new", Verb: "send"})

	list := cr.List()
	if len(list) != 2 || list[0].ID != "new" {
		t.Fatalf("List not newest-first: %+v", list)
	}
}

func TestConcurrentReportsAreSafe(t *testing.T) {
	cr := newTestRegistry(newFakeClock())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "inv-" + string(rune('a'+i%26))
			cr.Start(CommandStart{ID: id, Verb: "send"})
			cr.Heartbeat(id)
			cr.List()
			cr.Sweep()
			cr.End(CommandEnd{ID: id, State: CommandFinished, Outcome: "ok"})
		}(i)
	}
	wg.Wait()
}
