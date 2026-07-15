package main

import (
	"sync"
	"testing"
	"time"
)

// These tests prove the Go port reproduces the client-side pipeline semantics
// documented in brain-pjr0k: the stacked checks, dictation tolerance, priority
// ordering, the voice gate, and the server-owned submit machine.

func eval(e *Engine, text string, ver int64, tabs []Tab) EvalResponse {
	return e.Eval(EvalRequest{
		StreamID: "test", Version: ver, Text: text,
		VoiceEnabled: true, Reason: "input", Tabs: tabs,
	})
}

func verbs(r EvalResponse) []string {
	out := make([]string, len(r.Actions))
	for i, a := range r.Actions {
		out[i] = a.Verb
	}
	return out
}

func hasVerb(r EvalResponse, v string) bool {
	for _, a := range r.Actions {
		if a.Verb == v {
			return true
		}
	}
	return false
}

func TestVoiceGate(t *testing.T) {
	e := NewEngine()
	r := e.Eval(EvalRequest{StreamID: "s", Version: 1, Text: "hello bravely", VoiceEnabled: false})
	if r.Fired != "" {
		t.Fatalf("voice off must not fire any command; fired=%q", r.Fired)
	}
	if !hasVerb(r, "noop") {
		t.Fatalf("voice off should return a noop; got %v", verbs(r))
	}
}

func TestClearAnywhere(t *testing.T) {
	e := NewEngine()
	// anywhere mode: phrase anywhere clears the whole box.
	r := eval(e, "please change inside in input now", 1, nil)
	if r.Fired != "clear" {
		t.Fatalf("expected clear to fire, got %q (verbs %v)", r.Fired, verbs(r))
	}
	if !hasVerb(r, "clear") {
		t.Fatalf("clear command must emit a clear action; got %v", verbs(r))
	}
}

func TestSwitchTabResolvesAgent(t *testing.T) {
	e := NewEngine()
	tabs := []Tab{{ID: "marcus", Name: "Marcus Webb"}, {ID: "cato", Name: "Cato"}}
	r := eval(e, "switch to marcus", 1, tabs)
	if r.Fired != "switch-tab" {
		t.Fatalf("expected switch-tab, got %q (%v)", r.Fired, verbs(r))
	}
	// The switchTab action should carry the resolved id.
	for _, a := range r.Actions {
		if a.Verb == "switchTab" {
			if a.Args.Channel != "marcus" {
				t.Fatalf("expected channel marcus, got %q", a.Args.Channel)
			}
			return
		}
	}
	t.Fatalf("no switchTab action emitted; got %v", verbs(r))
}

func TestSwitchTabFallsThroughToGoToPage(t *testing.T) {
	e := NewEngine()
	// "go to status" — unknown agent should fall through to go-to-page (the
	// exact fall-through documented in registry.ts:103 / builtins.ts:70).
	tabs := []Tab{{ID: "marcus", Name: "Marcus Webb"}}
	r := eval(e, "go to status", 1, tabs)
	if r.Fired != "go-to-page" {
		t.Fatalf("unknown agent should fall through to go-to-page, got %q (%v)", r.Fired, verbs(r))
	}
	for _, a := range r.Actions {
		if a.Verb == "navigate" {
			if a.Args.URL != "/status/" {
				t.Fatalf("expected /status/, got %q", a.Args.URL)
			}
			return
		}
	}
	t.Fatalf("no navigate action; got %v", verbs(r))
}

func TestGoToPageMultiWordSlug(t *testing.T) {
	e := NewEngine()
	r := eval(e, "open my dashboard", 1, nil)
	if r.Fired != "go-to-page" {
		t.Fatalf("expected go-to-page, got %q", r.Fired)
	}
	for _, a := range r.Actions {
		if a.Verb == "navigate" {
			if a.Args.URL != "/my-dashboard/" {
				t.Fatalf("multi-word slug should hyphenate: got %q", a.Args.URL)
			}
			return
		}
	}
	t.Fatalf("no navigate; got %v", verbs(r))
}

func TestDictationToleranceInteriorShortWord(t *testing.T) {
	e := NewEngine()
	// "flag speech" — the interior-word tolerance and separator tolerance.
	// Commas between words must be tolerated (SEP).
	r := eval(e, "flag, speech", 1, nil)
	if r.Fired != "flag-speech" {
		t.Fatalf("comma separator should be tolerated; fired=%q (%v)", r.Fired, verbs(r))
	}
}

func TestStopSpeechStripsTail(t *testing.T) {
	e := NewEngine()
	r := eval(e, "quiet down spoken pause", 1, nil)
	if r.Fired != "stop-speech" {
		t.Fatalf("expected stop-speech, got %q (%v)", r.Fired, verbs(r))
	}
	if !hasVerb(r, "stopSpeech") {
		t.Fatalf("must emit stopSpeech; got %v", verbs(r))
	}
	// The setText should carry the stripped remainder.
	for _, a := range r.Actions {
		if a.Verb == "setText" {
			if a.Args.Text == nil || *a.Args.Text != "quiet down" {
				got := "<nil>"
				if a.Args.Text != nil {
					got = *a.Args.Text
				}
				t.Fatalf("expected stripped 'quiet down', got %q", got)
			}
			return
		}
	}
	t.Fatalf("no setText; got %v", verbs(r))
}

func TestPriorityOrderStopSpeechBeatsSubmit(t *testing.T) {
	e := NewEngine()
	// stop-speech (pri 5) and submit (pri 30) both are trailing. A buffer ending
	// in "spoken pause" hits stop-speech first (lower priority wins).
	r := eval(e, "hello spoken pause", 1, nil)
	if r.Fired != "stop-speech" {
		t.Fatalf("lower priority (stop-speech) should win, got %q", r.Fired)
	}
}

func TestSubmitArmsServerTimer(t *testing.T) {
	e := NewEngine()
	r := eval(e, "send this message bravely", 1, nil)
	if r.Fired != "submit" {
		t.Fatalf("trailing trigger word should arm submit, got %q (%v)", r.Fired, verbs(r))
	}
	if !hasVerb(r, "armTimer") {
		t.Fatalf("submit must emit advisory armTimer; got %v", verbs(r))
	}
	e.stats.mu.Lock()
	armed := e.stats.SubmitsArmed
	e.stats.mu.Unlock()
	if armed != 1 {
		t.Fatalf("expected 1 armed submit, got %d", armed)
	}
}

func TestSubmitFiresServerSideAndCallsBack(t *testing.T) {
	e := NewEngine()
	var mu sync.Mutex
	fires := []struct {
		stream, tail string
		base         int64
	}{}
	e.onSubmit = func(streamID string, seq, base int64, tail, text string) {
		mu.Lock()
		fires = append(fires, struct {
			stream, tail string
			base         int64
		}{streamID, tail, base})
		mu.Unlock()
	}
	eval(e, "ship it bravely", 7, nil)
	// The server-owned timer is 1000ms; wait past it.
	time.Sleep(1200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(fires) != 1 {
		t.Fatalf("expected exactly 1 server-side submit fire, got %d", len(fires))
	}
	if fires[0].tail != "bravely" {
		t.Fatalf("fire should carry the matched tail 'bravely', got %q", fires[0].tail)
	}
	if fires[0].base != 7 {
		t.Fatalf("fire should carry armed baseVersion 7, got %d", fires[0].base)
	}
}

func TestSubmitSelfCancelsWhenTailChanges(t *testing.T) {
	e := NewEngine()
	fired := int32(0)
	var mu sync.Mutex
	e.onSubmit = func(string, int64, int64, string, string) {
		mu.Lock()
		fired++
		mu.Unlock()
	}
	// Arm, then the very next pass the tail no longer matches → server cancels.
	eval(e, "hello bravely", 1, nil)
	r := eval(e, "hello bravely and more typing", 2, nil)
	if !hasVerb(r, "cancelTimer") {
		t.Fatalf("tail change should emit cancelTimer; got %v", verbs(r))
	}
	time.Sleep(1200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if fired != 0 {
		t.Fatalf("cancelled submit must NOT fire, but fired %d times", fired)
	}
	e.stats.mu.Lock()
	cancelled := e.stats.SubmitsCancelled
	e.stats.mu.Unlock()
	if cancelled != 1 {
		t.Fatalf("expected 1 cancellation recorded, got %d", cancelled)
	}
}

func TestStaleRequestVersionDropped(t *testing.T) {
	e := NewEngine()
	eval(e, "current text", 10, nil)
	// A late-arriving request with an older version is dropped (last-write-wins).
	r := eval(e, "old text bravely", 3, nil)
	if r.Fired != "" {
		t.Fatalf("stale-version request should not fire; got %q", r.Fired)
	}
	if !hasVerb(r, "noop") {
		t.Fatalf("stale request should return noop; got %v", verbs(r))
	}
}

func TestSeqMonotonic(t *testing.T) {
	e := NewEngine()
	r1 := eval(e, "a", 1, nil)
	r2 := eval(e, "b", 2, nil)
	if r2.Seq <= r1.Seq {
		t.Fatalf("seq must be monotonic per stream: %d then %d", r1.Seq, r2.Seq)
	}
}

func TestEvalTimeMeasured(t *testing.T) {
	e := NewEngine()
	r := eval(e, "some text to evaluate bravely", 1, nil)
	if r.EngineEvalNs <= 0 {
		t.Fatalf("engine eval time must be measured and positive, got %d", r.EngineEvalNs)
	}
}

func TestSubmitRearmResetsCountdown(t *testing.T) {
	e := NewEngine()
	fires := int32(0)
	var mu sync.Mutex
	e.onSubmit = func(string, int64, int64, string, string) { mu.Lock(); fires++; mu.Unlock() }
	// Arm at t=0, re-arm at t=600ms (still trailing trigger) → only ONE fire,
	// and it should be ~1000ms after the RE-arm, not the first arm.
	eval(e, "draft one bravely", 1, nil)
	time.Sleep(600 * time.Millisecond)
	eval(e, "draft two bravely", 2, nil) // re-arm
	time.Sleep(700 * time.Millisecond)   // 700ms after re-arm: should NOT have fired yet
	mu.Lock()
	early := fires
	mu.Unlock()
	if early != 0 {
		t.Fatalf("re-arm should reset the countdown; fired early %d times", early)
	}
	time.Sleep(500 * time.Millisecond) // now ~1200ms after re-arm
	mu.Lock()
	defer mu.Unlock()
	if fires != 1 {
		t.Fatalf("expected exactly 1 fire after re-arm settles, got %d", fires)
	}
}
