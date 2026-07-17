package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// engine_state_test.go — the server-owned submit state machine and per-stream
// isolation. These tests own timers and generation guards, so each uses a fresh
// Engine and must NOT run t.Parallel against shared engine state. Concurrency is
// tested explicitly with goroutines under -race.

// collectFires wires an Engine.onSubmit that records every fire thread-safely and
// returns the engine plus a snapshot accessor.
func collectFires(t *testing.T) (*Engine, func() int) {
	t.Helper()
	e := NewEngine()
	var n int32
	e.onSubmit = func(string, int64, int64, string, string) { atomic.AddInt32(&n, 1) }
	return e, func() int { return int(atomic.LoadInt32(&n)) }
}

func TestChannelSelectModeBypassesVoiceGate(t *testing.T) {
	// channel-select mode resolves even with VoiceEnabled=false: the picker is a
	// distinct surface that runs before the voice gate check.
	e := NewEngine()
	tabs := pickerTabs()
	r := e.Eval(EvalRequest{
		StreamID: "s", Version: 1, Text: "mayor",
		VoiceEnabled: false, Mode: "channel-select", Tabs: tabs,
	})
	if r.Fired != "channel-select" {
		t.Fatalf("channel-select must run regardless of voice gate; fired=%q", r.Fired)
	}
	if !hasVerb(r, "switchTab") {
		t.Fatalf("expected switchTab; got %v", verbs(r))
	}
}

func TestVoiceGateCancelsArmedSubmit(t *testing.T) {
	// Arm a submit with voice on, then a voice-off pass must cancel it.
	e, fires := collectFires(t)
	eval(e, "hello bravely", 1, nil)
	r := e.Eval(EvalRequest{StreamID: "test", Version: 2, Text: "hello bravely", VoiceEnabled: false})
	if !hasVerb(r, "cancelTimer") {
		t.Fatalf("voice-off must cancel armed submit; got %v", verbs(r))
	}
	time.Sleep(1200 * time.Millisecond)
	if fires() != 0 {
		t.Fatalf("cancelled submit must not fire; fired %d", fires())
	}
}

func TestFireSubmitStaleGenerationGuard(t *testing.T) {
	// Directly exercise the stale-fire branch: arm, then bump the generation
	// (as a re-arm/cancel would), then fire with the OLD gen. The guard must
	// drop it and increment StaleTimerFires without calling onSubmit.
	e, fires := collectFires(t)
	// Arm against a real request so submitTail/base are set.
	m := &matchResult{matchedText: "bravely", value: "go bravely"}
	// Delay is now data (submit handler config); these tests Stop the timer and
	// fire synchronously, so the 1s value is inert — passed to satisfy the arm API.
	e.armSubmit(EvalRequest{StreamID: "s1", Version: 5}, m, &actionList{}, 1000)

	st := e.stream("s1")
	st.mu.Lock()
	staleGen := st.timerGen - 1 // one behind current → guaranteed stale
	// Stop the real timer so it can't fire on its own and race our assertion.
	if st.submitTimer != nil {
		st.submitTimer.Stop()
	}
	st.mu.Unlock()

	before := staleFires(e)
	e.fireSubmit("s1", staleGen)
	if got := fires(); got != 0 {
		t.Fatalf("stale fire must not call onSubmit; got %d fires", got)
	}
	if after := staleFires(e); after != before+1 {
		t.Fatalf("stale fire must increment StaleTimerFires: before=%d after=%d", before, after)
	}
}

func TestFireSubmitFreshGenerationCallsBack(t *testing.T) {
	// The happy path of fireSubmit: a fire with the CURRENT gen calls onSubmit
	// with the armed tail + base and increments SubmitsFired.
	e := NewEngine()
	var mu sync.Mutex
	var gotTail string
	var gotBase int64
	e.onSubmit = func(_ string, _ int64, base int64, tail, _ string) {
		mu.Lock()
		gotTail, gotBase = tail, base
		mu.Unlock()
	}
	m := &matchResult{matchedText: "gravely", value: "send gravely"}
	e.armSubmit(EvalRequest{StreamID: "s2", Version: 9}, m, &actionList{}, 1000)
	st := e.stream("s2")
	st.mu.Lock()
	curGen := st.timerGen
	if st.submitTimer != nil {
		st.submitTimer.Stop() // fire synchronously below instead of via the AfterFunc
	}
	st.mu.Unlock()

	e.fireSubmit("s2", curGen)
	mu.Lock()
	defer mu.Unlock()
	if gotTail != "gravely" {
		t.Fatalf("fire tail: got %q want gravely", gotTail)
	}
	if gotBase != 9 {
		t.Fatalf("fire base: got %d want 9", gotBase)
	}
	e.stats.mu.Lock()
	fired := e.stats.SubmitsFired
	e.stats.mu.Unlock()
	if fired != 1 {
		t.Fatalf("SubmitsFired: got %d want 1", fired)
	}
}

func TestArmSubmitReArmBumpsGenerationAndTail(t *testing.T) {
	// Re-arming must Stop the prior timer, bump timerGen, and replace the tail.
	e := NewEngine()
	e.onSubmit = func(string, int64, int64, string, string) {}
	e.armSubmit(EvalRequest{StreamID: "s", Version: 1}, &matchResult{matchedText: "bravely"}, &actionList{}, 1000)
	st := e.stream("s")
	st.mu.Lock()
	gen1, tail1 := st.timerGen, st.submitTail
	st.mu.Unlock()

	e.armSubmit(EvalRequest{StreamID: "s", Version: 2}, &matchResult{matchedText: "briefly"}, &actionList{}, 1000)
	st.mu.Lock()
	gen2, tail2, base2 := st.timerGen, st.submitTail, st.submitBaseVer
	if st.submitTimer != nil {
		st.submitTimer.Stop()
	}
	st.mu.Unlock()

	if gen2 <= gen1 {
		t.Fatalf("re-arm must bump generation: %d then %d", gen1, gen2)
	}
	if tail1 != "bravely" || tail2 != "briefly" {
		t.Fatalf("tail not replaced on re-arm: %q then %q", tail1, tail2)
	}
	if base2 != 2 {
		t.Fatalf("re-arm base version: got %d want 2", base2)
	}
	e.stats.mu.Lock()
	armed := e.stats.SubmitsArmed
	e.stats.mu.Unlock()
	if armed != 2 {
		t.Fatalf("two arms expected, got %d", armed)
	}
}

func TestCancelSubmitWithNothingArmedIsNoop(t *testing.T) {
	// Cancelling when no timer is armed must emit no cancel actions and not
	// touch the cancelled counter.
	e := NewEngine()
	out := &actionList{}
	e.cancelSubmit("never-armed", out, "test")
	if len(out.items) != 0 {
		t.Fatalf("cancel with nothing armed must emit nothing; got %v", out.items)
	}
	e.stats.mu.Lock()
	c := e.stats.SubmitsCancelled
	e.stats.mu.Unlock()
	if c != 0 {
		t.Fatalf("SubmitsCancelled should stay 0; got %d", c)
	}
}

func TestSeqStrictlyIncreasesAcrossMixedOps(t *testing.T) {
	// Every finish() bumps seq; nextSeq (used by fireSubmit) also bumps it. Seq
	// must be strictly increasing across a mix of evals on one stream.
	e := NewEngine()
	last := int64(0)
	for i := int64(1); i <= 5; i++ {
		r := eval(e, fmt.Sprintf("text %d", i), i, nil)
		if r.Seq <= last {
			t.Fatalf("seq not strictly increasing: %d then %d", last, r.Seq)
		}
		last = r.Seq
	}
}

func TestProtocolAndBaseVersionStamped(t *testing.T) {
	e := NewEngine()
	r := eval(e, "plain text", 42, nil)
	if r.ProtocolV != ProtocolVersion {
		t.Fatalf("protocol version: got %d want %d", r.ProtocolV, ProtocolVersion)
	}
	if r.BaseVersion != 42 {
		t.Fatalf("baseVersion must echo request version: got %d want 42", r.BaseVersion)
	}
	if r.StreamID != "test" {
		t.Fatalf("streamId echo: got %q", r.StreamID)
	}
}

func TestConcurrentStreamsIsolated(t *testing.T) {
	// Two distinct streamIds must not clobber each other's submit state. Run many
	// concurrent evals across two streams under -race; each stream's fires must
	// carry only its own tail. This is the distributed-state hazard the design doc
	// warns about — the test proves per-stream mutex isolation holds.
	e := NewEngine()
	var mu sync.Mutex
	tails := map[string][]string{}
	e.onSubmit = func(streamID string, _ int64, _ int64, tail, _ string) {
		mu.Lock()
		tails[streamID] = append(tails[streamID], tail)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(v int64) {
			defer wg.Done()
			e.Eval(EvalRequest{StreamID: "alpha", Version: v, Text: "alpha bravely", VoiceEnabled: true})
		}(int64(i + 1))
		go func(v int64) {
			defer wg.Done()
			e.Eval(EvalRequest{StreamID: "beta", Version: v, Text: "beta gravely", VoiceEnabled: true})
		}(int64(i + 1))
	}
	wg.Wait()
	time.Sleep(1300 * time.Millisecond) // let the last-armed timer per stream fire

	mu.Lock()
	defer mu.Unlock()
	// Each stream should have fired at most once (last-arm wins after re-arms),
	// and whatever fired must carry that stream's own tail — never the other's.
	for _, tl := range tails["alpha"] {
		if tl != "bravely" {
			t.Fatalf("alpha stream fired with foreign tail %q", tl)
		}
	}
	for _, tl := range tails["beta"] {
		if tl != "gravely" {
			t.Fatalf("beta stream fired with foreign tail %q", tl)
		}
	}
	// Sanity: both streams exist as independent state entries.
	if e.stream("alpha") == e.stream("beta") {
		t.Fatalf("distinct streamIds must map to distinct state")
	}
}

func TestConcurrentStalePathUnderRace(t *testing.T) {
	// Hammer one stream with rapid re-arms so real AfterFunc timers fire against
	// bumped generations — the genuine stale-fire race. Under -race this proves
	// the generation guard + mutex prevent a data race and a wrong-tail fire.
	e := NewEngine()
	var fires int32
	e.onSubmit = func(string, int64, int64, string, string) { atomic.AddInt32(&fires, 1) }
	for i := int64(1); i <= 200; i++ {
		eval(e, "spam bravely", i, nil)
	}
	time.Sleep(1300 * time.Millisecond)
	// Exactly one fire survives (the final arm); all earlier arms were superseded.
	if got := atomic.LoadInt32(&fires); got != 1 {
		t.Fatalf("rapid re-arm should yield exactly one fire, got %d", got)
	}
}

// ── Debug/introspection surfaces ──────────────────────────────────────────────

func TestDescribeCommands(t *testing.T) {
	t.Parallel()
	e := NewEngine()
	rows := e.describeCommands()
	if len(rows) != len(builtins) {
		t.Fatalf("describeCommands rows: got %d want %d", len(rows), len(builtins))
	}
	// Rows are priority-sorted ascending (stop-speech pri 5 first).
	if rows[0]["id"] != "stop-speech" {
		t.Fatalf("first row should be lowest priority (stop-speech); got %v", rows[0]["id"])
	}
	// Every row carries the documented fields and marshals cleanly.
	for _, row := range rows {
		for _, f := range []string{"id", "priority", "mode", "phrases", "description"} {
			if _, ok := row[f]; !ok {
				t.Fatalf("row missing field %q: %v", f, row)
			}
		}
	}
	if _, err := json.Marshal(rows); err != nil {
		t.Fatalf("describeCommands must marshal: %v", err)
	}
}

func TestStatsSnapshot(t *testing.T) {
	t.Parallel()
	e := NewEngine()
	eval(e, "just text", 1, nil)
	eval(e, "more text", 2, nil)
	snap := e.stats.snapshot()
	for _, k := range []string{"evals", "submitsArmed", "submitsFired", "submitsCancelled", "staleTimerFires", "avgEvalNs", "maxEvalNs"} {
		if _, ok := snap[k]; !ok {
			t.Fatalf("snapshot missing key %q: %v", k, snap)
		}
	}
	if snap["evals"].(int64) != 2 {
		t.Fatalf("evals: got %v want 2", snap["evals"])
	}
	// avgEvalNs must be derived, not zero, after real evals.
	if snap["avgEvalNs"].(int64) <= 0 {
		t.Fatalf("avgEvalNs should be positive after evals; got %v", snap["avgEvalNs"])
	}
}

func TestStatsSnapshotZeroEvalsNoDivideByZero(t *testing.T) {
	t.Parallel()
	// snapshot must not divide by zero when Evals==0.
	e := NewEngine()
	snap := e.stats.snapshot()
	if snap["avgEvalNs"].(int64) != 0 {
		t.Fatalf("avgEvalNs with zero evals must be 0; got %v", snap["avgEvalNs"])
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"  hi  ", "hi"},
		{"\thi\n", "hi"},
		{"hi", "hi"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalize(c.in); got != c.want {
			t.Errorf("normalize(%q): got %q want %q", c.in, got, c.want)
		}
	}
}

// staleFires reads the StaleTimerFires counter under its mutex.
func staleFires(e *Engine) int64 {
	e.stats.mu.Lock()
	defer e.stats.mu.Unlock()
	return e.stats.StaleTimerFires
}
