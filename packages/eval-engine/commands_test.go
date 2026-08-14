package main

import (
	"testing"
)

// commands_test.go — resolver + builtin-action coverage for commands.go.
// resolveAgent and parseChannelNumber are pure functions tested directly;
// every builtin's runAction effect and the priority/fallthrough behavior are
// tested through a fresh Engine so the real matcher→action wiring is exercised.

// fireEval runs one eval on a fresh Engine (no shared submit state) and returns
// the response. A fresh Engine per call keeps subtests parallel-safe.
func fireEval(t *testing.T, text string, tabs []Tab) EvalResponse {
	t.Helper()
	e := NewEngine()
	return eval(e, text, 1, tabs)
}

// wantVerb fails unless the response contains exactly the named verb, returning
// the matching action for further field assertions.
func wantVerb(t *testing.T, r EvalResponse, verb string) Action {
	t.Helper()
	for _, a := range r.Actions {
		if a.Verb == verb {
			return a
		}
	}
	t.Fatalf("expected verb %q, got %v", verb, verbs(r))
	return Action{}
}

func TestResolveAgent(t *testing.T) {
	t.Parallel()
	tabs := []Tab{
		{ID: "marcus", Name: "Marcus Webb", Nicknames: []string{"webb", "the architect"}},
		{ID: "cato", Name: "Cato", Nicknames: nil},
		{ID: "main", Name: "Main"},
	}
	cases := []struct {
		name   string
		spoken string
		want   string
	}{
		{"exact id", "marcus", "marcus"},
		{"exact name case-insensitive", "MARCUS WEBB", "marcus"},
		{"exact nickname", "webb", "marcus"},
		{"exact multiword nickname", "the architect", "marcus"},
		{"exact id other tab", "cato", "cato"},
		{"substring name", "marc", "marcus"},
		{"substring nickname", "arch", "marcus"},
		{"trailing punctuation trimmed", "cato.", "cato"},
		{"surrounding whitespace", "  cato  ", "cato"},
		{"empty string", "", ""},
		{"whitespace only", "   ", ""},
		{"punctuation only", "...", ""},
		{"no match", "nobody", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveAgent(c.spoken, tabs); got != c.want {
				t.Errorf("resolveAgent(%q): got %q want %q", c.spoken, got, c.want)
			}
		})
	}
}

func TestResolveAgentNilTabs(t *testing.T) {
	t.Parallel()
	if got := resolveAgent("marcus", nil); got != "" {
		t.Errorf("nil tabs: got %q want empty", got)
	}
}

func TestResolveAgentBlankNicknamesTolerated(t *testing.T) {
	t.Parallel()
	// Nicknames may contain blank entries; they must not spuriously match an
	// empty-after-trim query and must be skipped in substring pass.
	tabs := []Tab{{ID: "x", Name: "X", Nicknames: []string{"", "  ", "zulu"}}}
	if got := resolveAgent("zulu", tabs); got != "x" {
		t.Errorf("real nickname should still match past blanks: got %q", got)
	}
}

func TestParseChannelNumber(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		wantN   int
		wantHit bool
	}{
		{"bare digit", "2", 2, true},
		{"multi digit", "10", 10, true},
		{"channel filler", "channel 3", 3, true},
		{"number filler", "number 4", 4, true},
		{"number word", "seven", 7, true},
		{"ordinal word", "third", 3, true},
		{"ordinal tenth", "tenth", 10, true},
		{"empty", "", 0, false},
		{"filler only leaves empty", "channel ", 0, false},
		{"non numeric word", "banana", 0, false},
		{"digit with trailing text", "2 please", 0, false},
		{"digit with leading text non-filler", "pick 2", 0, false},
		{"mixed", "2nd", 0, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			gotN, gotHit := parseChannelNumber(c.in)
			if gotN != c.wantN || gotHit != c.wantHit {
				t.Errorf("parseChannelNumber(%q): got (%d,%v) want (%d,%v)",
					c.in, gotN, gotHit, c.wantN, c.wantHit)
			}
		})
	}
}

func TestTrimTrailingPunct(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"hello.", "hello"},
		{"hello!!!", "hello"},
		{"a,b;c:", "a,b;c"},
		{"wait?", "wait"},
		{"", ""},
		{"...", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got := trimTrailingPunct(c.in); got != c.want {
				t.Errorf("trimTrailingPunct(%q): got %q want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLastIndexFold(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		hay, needle string
		want        int
	}{
		{"found case-insensitive", "Hello BRAVELY", "bravely", 6},
		{"last occurrence", "go go go", "go", 6},
		{"not found", "hello", "xyz", -1},
		// strings.LastIndex(s, "") returns len(s) — empty needle matches at the end.
		{"empty needle at end", "hello", "", 5},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := lastIndexFold(c.hay, c.needle); got != c.want {
				t.Errorf("lastIndexFold(%q,%q): got %d want %d", c.hay, c.needle, got, c.want)
			}
		})
	}
}

// ── Every builtin's runAction effect, driven through the real engine ──────────

func TestBuiltinClear(t *testing.T) {
	t.Parallel()
	r := fireEval(t, "wipe it change inside input", nil)
	if r.Fired != "clear" {
		t.Fatalf("fired=%q want clear (%v)", r.Fired, verbs(r))
	}
	wantVerb(t, r, "clear")
}

func TestBuiltinStopSpeech(t *testing.T) {
	t.Parallel()
	r := fireEval(t, "quiet down spoken pause", nil)
	if r.Fired != "stop-speech" {
		t.Fatalf("fired=%q want stop-speech (%v)", r.Fired, verbs(r))
	}
	wantVerb(t, r, "stopSpeech")
	a := wantVerb(t, r, "setText")
	if a.Args.Text == nil || *a.Args.Text != "quiet down" {
		got := "<nil>"
		if a.Args.Text != nil {
			got = *a.Args.Text
		}
		t.Errorf("setText stripped: got %q want %q", got, "quiet down")
	}
}

func TestBuiltinStopSpeechWholeBuffer(t *testing.T) {
	t.Parallel()
	// When the trigger IS the whole buffer, the stripped remainder is empty.
	r := fireEval(t, "spoken pause", nil)
	if r.Fired != "stop-speech" {
		t.Fatalf("fired=%q want stop-speech", r.Fired)
	}
	a := wantVerb(t, r, "setText")
	if a.Args.Text == nil || *a.Args.Text != "" {
		t.Errorf("whole-buffer trigger should strip to empty; got %v", a.Args.Text)
	}
}

func TestBuiltinFlagSpeech(t *testing.T) {
	t.Parallel()
	r := fireEval(t, "flag that", nil)
	if r.Fired != "flag-speech" {
		t.Fatalf("fired=%q want flag-speech (%v)", r.Fired, verbs(r))
	}
	wantVerb(t, r, "flagSpeech")
	wantVerb(t, r, "clear")
}

func TestBuiltinSwitchTab(t *testing.T) {
	t.Parallel()
	tabs := []Tab{{ID: "marcus", Name: "Marcus Webb"}}
	r := fireEval(t, "switch to marcus", tabs)
	if r.Fired != "switch-tab" {
		t.Fatalf("fired=%q want switch-tab (%v)", r.Fired, verbs(r))
	}
	if a := wantVerb(t, r, "switchTab"); a.Args.Channel != "marcus" {
		t.Errorf("switchTab channel: got %q want marcus", a.Args.Channel)
	}
	wantVerb(t, r, "clear")
}

func TestBuiltinArchiveTab(t *testing.T) {
	t.Parallel()
	tabs := []Tab{{ID: "cato", Name: "Cato"}}
	r := fireEval(t, "archive cato", tabs)
	if r.Fired != "archive-tab" {
		t.Fatalf("fired=%q want archive-tab (%v)", r.Fired, verbs(r))
	}
	if a := wantVerb(t, r, "archiveTab"); a.Args.Channel != "cato" {
		t.Errorf("archiveTab channel: got %q want cato", a.Args.Channel)
	}
	wantVerb(t, r, "clear")
}

func TestBuiltinArchiveTabUnknownAgentNoFire(t *testing.T) {
	t.Parallel()
	// archive-tab with an unresolvable agent returns handled=false. There is no
	// lower-priority command that also matches "archive nobody", so nothing fires.
	tabs := []Tab{{ID: "cato", Name: "Cato"}}
	r := fireEval(t, "archive nobody", tabs)
	if r.Fired == "archive-tab" {
		t.Fatalf("unknown archive target must not fire archive-tab (%v)", verbs(r))
	}
	if hasVerb(r, "archiveTab") {
		t.Errorf("must not emit archiveTab for unknown agent; got %v", verbs(r))
	}
}

func TestBuiltinNextTab(t *testing.T) {
	t.Parallel()
	r := fireEval(t, "next tab", nil)
	if r.Fired != "next-tab" {
		t.Fatalf("fired=%q want next-tab (%v)", r.Fired, verbs(r))
	}
	wantVerb(t, r, "nextTab")
	wantVerb(t, r, "clear")
}

func TestBuiltinPrevTab(t *testing.T) {
	t.Parallel()
	r := fireEval(t, "previous tab", nil)
	if r.Fired != "prev-tab" {
		t.Fatalf("fired=%q want prev-tab (%v)", r.Fired, verbs(r))
	}
	wantVerb(t, r, "prevTab")
	wantVerb(t, r, "clear")
}

func TestBuiltinGoToPage(t *testing.T) {
	t.Parallel()
	r := fireEval(t, "go to status", nil)
	if r.Fired != "go-to-page" {
		t.Fatalf("fired=%q want go-to-page (%v)", r.Fired, verbs(r))
	}
	if a := wantVerb(t, r, "navigate"); a.Args.URL != "/status/" {
		t.Errorf("navigate URL: got %q want /status/", a.Args.URL)
	}
	wantVerb(t, r, "clear")
}

func TestBuiltinGoToPageHyphenatesMultiWord(t *testing.T) {
	t.Parallel()
	r := fireEval(t, "open my dashboard", nil)
	if a := wantVerb(t, r, "navigate"); a.Args.URL != "/my-dashboard/" {
		t.Errorf("multi-word slug: got %q want /my-dashboard/", a.Args.URL)
	}
}

func TestSwitchTabFallsThroughToGoToPageWhenAgentUnknown(t *testing.T) {
	t.Parallel()
	// Priority ordering: switch-tab (pri 20) matches "go to X" shape but returns
	// handled=false for an unknown agent, so the pass continues to go-to-page
	// (pri 25). This is the documented fall-through (registry.ts:103).
	tabs := []Tab{{ID: "marcus", Name: "Marcus Webb"}}
	r := fireEval(t, "go to settings", tabs)
	if r.Fired != "go-to-page" {
		t.Fatalf("unknown agent should fall through to go-to-page; fired=%q (%v)", r.Fired, verbs(r))
	}
	if a := wantVerb(t, r, "navigate"); a.Args.URL != "/settings/" {
		t.Errorf("navigate URL: got %q want /settings/", a.Args.URL)
	}
}

func TestSwitchTabWinsWhenAgentKnown(t *testing.T) {
	t.Parallel()
	// When the agent IS known, switch-tab (pri 20) fires first and go-to-page
	// (pri 25) never runs — no navigate emitted.
	tabs := []Tab{{ID: "marcus", Name: "Marcus Webb"}}
	r := fireEval(t, "go to marcus", tabs)
	if r.Fired != "switch-tab" {
		t.Fatalf("known agent should fire switch-tab; fired=%q (%v)", r.Fired, verbs(r))
	}
	if hasVerb(r, "navigate") {
		t.Errorf("switch-tab win should not also navigate; got %v", verbs(r))
	}
}

// The old TestRunAction* tests were deleted with the runAction switch: submit is
// now a handler (never routed through interpretSequence), and the interpreter only
// ever runs known, pre-validated manifest commands, so both invariants those tests
// guarded are now structural. Submit routing is covered by the engine submit tests;
// unknown/invalid command shapes are rejected at load by the manifest validator
// (see manifest_test.go).
