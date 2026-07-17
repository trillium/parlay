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

func TestClearTrailing(t *testing.T) {
	// TRAILING-match (robots-41w clear half): a clear phrase clears the WHOLE box
	// ONLY when it is at the very END of the buffer. A phrase in the middle does
	// NOTHING. A phrase that IS the whole buffer counts as trailing (it's at the
	// end). Both phrases; case/punctuation tolerant.
	cases := []struct {
		name      string
		text      string
		wantClear bool
	}{
		// "change inside input" — the natural phrasing.
		{"natural trailing", "hello world change inside input", true},
		{"natural non-trailing", "change inside input hello", false},
		{"natural whole box", "change inside input", true},
		{"natural mid buffer", "please change inside input then more", false},
		// "change inside in input" — the "in input" variant.
		{"variant trailing", "hello world change inside in input", true},
		{"variant non-trailing", "change inside in input hello", false},
		{"variant whole box", "change inside in input", true},
		{"variant mid buffer", "please change inside in input now", false},
		// Case + trailing-punctuation tolerance (still trailing).
		{"upper + punct", "CHANGE INSIDE INPUT!!!", true},
		{"trailing with comma", "reset it, change inside input,", true},
		// Negative: no clear phrase → must not clear.
		{"plain text", "just some normal text here", false},
		{"near phrase not exact", "change the input box please", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := NewEngine()
			r := eval(e, c.text, 1, nil)
			gotClear := r.Fired == "clear" && hasVerb(r, "clear")
			if gotClear != c.wantClear {
				t.Fatalf("text %q: got clear=%v (fired=%q verbs=%v), want clear=%v",
					c.text, gotClear, r.Fired, verbs(r), c.wantClear)
			}
		})
	}
}

func TestBothClearPhrasesRegistered(t *testing.T) {
	// Guard against regressing to a single hardcoded phrase: BOTH clear phrases
	// must be present in the clear command spec.
	for _, spec := range builtins {
		if spec.id != "clear" {
			continue
		}
		want := map[string]bool{"change inside input": false, "change inside in input": false}
		for _, p := range spec.phrases {
			if _, ok := want[p]; ok {
				want[p] = true
			}
		}
		for phrase, present := range want {
			if !present {
				t.Fatalf("clear spec missing required phrase %q; have %v", phrase, spec.phrases)
			}
		}
		return
	}
	t.Fatalf("no clear command found in builtins")
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
		stream, tail, platform string
		base                   int64
	}{}
	e.onSubmit = func(streamID string, seq, base int64, tail, text, platform string) {
		mu.Lock()
		fires = append(fires, struct {
			stream, tail, platform string
			base                   int64
		}{streamID, tail, platform, base})
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
	// The async fire must know which surface it lands on (default parlay here).
	if fires[0].platform != "parlay" {
		t.Fatalf("fire should carry the stream's platform 'parlay', got %q", fires[0].platform)
	}
}

func TestSubmitSelfCancelsWhenTailChanges(t *testing.T) {
	e := NewEngine()
	fired := int32(0)
	var mu sync.Mutex
	e.onSubmit = func(string, int64, int64, string, string, string) {
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

// ── Channel picker (CHANNEL_PICKER_CONTRACT) ───────────────────────────────────

// pickerTabs is a fixed ordered tab set used across the picker tests so the
// 1-based numbering the user speaks is stable.
func pickerTabs() []Tab {
	return []Tab{
		{ID: "main", Name: "Main", Nicknames: []string{"main"}},
		{ID: "mayor", Name: "Mayor", Nicknames: []string{"boss", "chief"}},
		{ID: "cato", Name: "Cato", Nicknames: nil},
	}
}

func TestResolveChannelSelection(t *testing.T) {
	tabs := pickerTabs()
	cases := []struct {
		name       string
		spoken     string
		wantID     string
		wantCancel bool
		wantOK     bool
	}{
		// Rule 1 — number / ordinal.
		{"digit", "2", "mayor", false, true},
		{"digit with channel filler", "channel 2", "mayor", false, true},
		{"digit with number filler", "number 3", "cato", false, true},
		{"number word", "two", "mayor", false, true},
		{"ordinal word", "first", "main", false, true},
		{"ordinal tenth out of range", "tenth", "", false, false},
		{"digit out of range", "9", "", false, false},
		// Rule 2 — exact id / name / nickname.
		{"exact name", "mayor", "mayor", false, true},
		{"exact name case-insensitive", "MAYOR", "mayor", false, true},
		{"exact nickname", "boss", "mayor", false, true},
		{"exact second nickname", "chief", "mayor", false, true},
		{"exact id", "cato", "cato", false, true},
		{"exact with trailing punct", "cato.", "cato", false, true},
		// Rule 3 — substring id / name / nickname (first match wins).
		{"substring name", "may", "mayor", false, true},
		{"substring nickname", "bos", "mayor", false, true},
		// Rule 4 — cancel words.
		{"cancel close", "close", "", true, false},
		{"cancel cancel", "cancel", "", true, false},
		{"cancel never mind two words", "never mind", "", true, false},
		{"cancel nevermind one word", "nevermind", "", true, false},
		{"cancel dismiss", "dismiss", "", true, false},
		{"cancel exit", "exit", "", true, false},
		// Rule 5 — no match.
		{"no match gibberish", "zxqp", "", false, false},
		{"empty", "", "", false, false},
		{"whitespace only", "   ", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, cancel, ok := resolveChannelSelection(c.spoken, tabs)
			if id != c.wantID || cancel != c.wantCancel || ok != c.wantOK {
				t.Fatalf("resolveChannelSelection(%q) = (%q,%v,%v); want (%q,%v,%v)",
					c.spoken, id, cancel, ok, c.wantID, c.wantCancel, c.wantOK)
			}
		})
	}
}

func TestResolveAgentMatchesNicknames(t *testing.T) {
	tabs := pickerTabs()
	cases := []struct {
		spoken string
		want   string
	}{
		{"boss", "mayor"},  // exact nickname
		{"chief", "mayor"}, // exact second nickname
		{"bos", "mayor"},   // substring nickname
		{"mayor", "mayor"}, // exact name still works
		{"cato", "cato"},   // id still works
		{"nobody", ""},     // no match
	}
	for _, c := range cases {
		if got := resolveAgent(c.spoken, tabs); got != c.want {
			t.Fatalf("resolveAgent(%q) = %q; want %q", c.spoken, got, c.want)
		}
	}
}

func TestChannelListEmitsOpenChannelPicker(t *testing.T) {
	e := NewEngine()
	tabs := pickerTabs()
	r := eval(e, "channel list", 1, tabs)
	if r.Fired != "channel-list" {
		t.Fatalf("expected channel-list, got %q (%v)", r.Fired, verbs(r))
	}
	if !hasVerb(r, "openChannelPicker") {
		t.Fatalf("channel-list must emit openChannelPicker; got %v", verbs(r))
	}
	if hasVerb(r, "openSwitcher") {
		t.Fatalf("openSwitcher must NOT be emitted anymore; got %v", verbs(r))
	}
	for _, a := range r.Actions {
		if a.Verb != "openChannelPicker" {
			continue
		}
		if a.Args.Prompt != pickerPrompt {
			t.Fatalf("expected prompt %q, got %q", pickerPrompt, a.Args.Prompt)
		}
		chans := a.Args.Channels
		if len(chans) != 3 {
			t.Fatalf("expected 3 channels, got %d", len(chans))
		}
		// index 1-based; label = first nickname if present else name.
		if chans[0].Index != 1 || chans[0].ID != "main" || chans[0].Label != "main" || chans[0].Nickname != "main" {
			t.Fatalf("channel[0] wrong: %+v", chans[0])
		}
		if chans[1].Index != 2 || chans[1].ID != "mayor" || chans[1].Label != "boss" || chans[1].Nickname != "boss" {
			t.Fatalf("channel[1] wrong: %+v", chans[1])
		}
		// cato has no nickname → label falls back to name, nickname empty.
		if chans[2].Index != 3 || chans[2].ID != "cato" || chans[2].Label != "Cato" || chans[2].Nickname != "" {
			t.Fatalf("channel[2] wrong: %+v", chans[2])
		}
		return
	}
	t.Fatalf("no openChannelPicker action found")
}

func evalMode(e *Engine, text string, ver int64, tabs []Tab) EvalResponse {
	return e.Eval(EvalRequest{
		StreamID: "picker-test", Version: ver, Text: text,
		VoiceEnabled: true, Reason: "input", Tabs: tabs, Mode: "channel-select",
	})
}

func TestChannelSelectModeSwitchesTab(t *testing.T) {
	e := NewEngine()
	tabs := pickerTabs()
	r := evalMode(e, "mayor", 1, tabs)
	if r.Fired != "channel-select" {
		t.Fatalf("expected fired=channel-select, got %q", r.Fired)
	}
	if !hasVerb(r, "switchTab") || !hasVerb(r, "closeChannelPicker") {
		t.Fatalf("hit must emit switchTab + closeChannelPicker; got %v", verbs(r))
	}
	for _, a := range r.Actions {
		if a.Verb == "switchTab" && a.Args.Channel != "mayor" {
			t.Fatalf("expected switchTab mayor, got %q", a.Args.Channel)
		}
	}
	// A number pick resolves the same way.
	r2 := evalMode(e, "channel 3", 2, tabs)
	if !hasVerb(r2, "switchTab") || !hasVerb(r2, "closeChannelPicker") {
		t.Fatalf("number pick must switch+close; got %v", verbs(r2))
	}
	for _, a := range r2.Actions {
		if a.Verb == "switchTab" && a.Args.Channel != "cato" {
			t.Fatalf("number 3 should resolve to cato, got %q", a.Args.Channel)
		}
	}
}

func TestChannelSelectModeNoMatchHints(t *testing.T) {
	e := NewEngine()
	tabs := pickerTabs()
	r := evalMode(e, "zxqp", 1, tabs)
	if r.Fired != "channel-select" {
		t.Fatalf("expected fired=channel-select, got %q", r.Fired)
	}
	if !hasVerb(r, "pickerHint") {
		t.Fatalf("no-match must emit pickerHint; got %v", verbs(r))
	}
	if hasVerb(r, "closeChannelPicker") || hasVerb(r, "switchTab") {
		t.Fatalf("no-match must NOT close or switch; got %v", verbs(r))
	}
	for _, a := range r.Actions {
		if a.Verb == "pickerHint" {
			if a.Args.Text == nil || *a.Args.Text != `No channel matched "zxqp" — try again` {
				got := "<nil>"
				if a.Args.Text != nil {
					got = *a.Args.Text
				}
				t.Fatalf("unexpected hint text: %q", got)
			}
			return
		}
	}
	t.Fatalf("no pickerHint action found")
}

func TestChannelSelectModeCancelCloses(t *testing.T) {
	e := NewEngine()
	tabs := pickerTabs()
	r := evalMode(e, "never mind", 1, tabs)
	if r.Fired != "channel-select" {
		t.Fatalf("expected fired=channel-select, got %q", r.Fired)
	}
	if !hasVerb(r, "closeChannelPicker") {
		t.Fatalf("cancel must emit closeChannelPicker; got %v", verbs(r))
	}
	if hasVerb(r, "switchTab") {
		t.Fatalf("cancel must NOT switch; got %v", verbs(r))
	}
}

func TestSubmitRearmResetsCountdown(t *testing.T) {
	e := NewEngine()
	fires := int32(0)
	var mu sync.Mutex
	e.onSubmit = func(string, int64, int64, string, string, string) { mu.Lock(); fires++; mu.Unlock() }
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
