package evalengine

import (
	"testing"
)

// matcher_test.go — direct unit tests for the phrase→regex matching engine
// (matcher.go). These exercise the compiled RE2 layer beneath the command
// registry: escRe metacharacter escaping, phraseCore tolerance building, the
// three MatchMode anchors, {slot} capture extraction, and the compile-skip
// safety valves. Engine-level integration lives in engine_state_test.go /
// commands_test.go; this file stays at the matcher primitive.

// mustMatchers compiles one phrase in one mode and fails the test if nothing
// compiled. It isolates the "phrase → []CompiledMatcher" step so a matcher test
// never silently runs against an empty matcher set.
func mustMatchers(t *testing.T, phrase string, mode MatchMode) []CompiledMatcher {
	t.Helper()
	cms := compilePhrases([]string{phrase}, mode)
	if len(cms) == 0 {
		t.Fatalf("phrase %q (%s) compiled to zero matchers", phrase, mode)
	}
	return cms
}

// firstMatch runs every compiled matcher for a phrase over value and returns the
// first non-nil result (mirroring how runPass iterates a command's matchers).
func firstMatch(cms []CompiledMatcher, value string) *matchResult {
	for _, cm := range cms {
		if m := cm.match(value); m != nil {
			return m
		}
	}
	return nil
}

func TestEscRe(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain word untouched", "hello", "hello"},
		{"dot escaped", "a.b", `a\.b`},
		{"star and plus", "a*b+", `a\*b\+`},
		{"parens and brackets", "(x)[y]", `\(x\)\[y\]`},
		{"backslash", `a\b`, `a\\b`},
		{"anchors and braces", "^${}", `\^\$\{\}`},
		{"pipe and question", "a|b?", `a\|b\?`},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := escRe(c.in); got != c.want {
				t.Errorf("escRe(%q): got %q want %q", c.in, got, c.want)
			}
		})
	}
}

func TestModeWholeMatch(t *testing.T) {
	t.Parallel()
	cms := mustMatchers(t, "channel list", ModeWhole)
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"exact whole", "channel list", true},
		{"leading/trailing space", "  channel list  ", true},
		{"trailing punctuation", "channel list!!!", true},
		{"comma separator", "channel, list", true},
		{"case fold", "CHANNEL LIST", true},
		{"prefix text rejects whole", "please channel list", false},
		{"suffix text rejects whole", "channel list now", false},
		{"partial rejects", "channel", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := firstMatch(cms, c.value) != nil
			if got != c.want {
				t.Errorf("whole %q: got match=%v want %v", c.value, got, c.want)
			}
		})
	}
}

func TestModeTrailingMatch(t *testing.T) {
	t.Parallel()
	cms := mustMatchers(t, "spoken pause", ModeTrailing)
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"trailing with prefix", "quiet down spoken pause", true},
		{"whole buffer counts as trailing", "spoken pause", true},
		{"trailing with punct", "quiet spoken pause.", true},
		{"case fold", "quiet SPOKEN PAUSE", true},
		{"mid buffer rejects", "spoken pause then more", false},
		{"leading only rejects", "spoken pause hello", false},
		{"absent rejects", "just talking", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := firstMatch(cms, c.value) != nil
			if got != c.want {
				t.Errorf("trailing %q: got match=%v want %v", c.value, got, c.want)
			}
		})
	}
}

func TestModeAnywhereMatch(t *testing.T) {
	t.Parallel()
	// No builtin ships in anywhere mode, so this is the only coverage of the
	// RE2 boundary-consuming anchor (matcher.go note 1). Rebindable via settings.
	cms := mustMatchers(t, "scratch that", ModeAnywhere)
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"at start", "scratch that please do it", true},
		{"in middle", "please scratch that now", true},
		{"at end", "please scratch that", true},
		{"whole buffer", "scratch that", true},
		{"comma boundary", "wait, scratch that, go", true},
		{"embedded in word rejects", "scratchthat here", false},
		{"absent rejects", "keep it as written", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := firstMatch(cms, c.value) != nil
			if got != c.want {
				t.Errorf("anywhere %q: got match=%v want %v", c.value, got, c.want)
			}
		})
	}
}

func TestModeRegexUnknownModeReturnsNil(t *testing.T) {
	t.Parallel()
	// An unrecognized mode must yield nil (the default branch) so a bad spec is
	// skipped rather than panicking.
	if re := modeRegex("anything", MatchMode("bogus")); re != nil {
		t.Errorf("unknown mode: got non-nil regexp, want nil")
	}
}

func TestPhraseCoreCaptureSlot(t *testing.T) {
	t.Parallel()
	// {agent} at the tail — the switch-tab shape. Capture must extract the spoken
	// remainder.
	cms := mustMatchers(t, "switch to {agent}", ModeWhole)
	m := firstMatch(cms, "switch to marcus webb")
	if m == nil {
		t.Fatal("expected a match for 'switch to marcus webb'")
	}
	if got := m.captures["agent"]; got != "marcus webb" {
		t.Errorf("agent capture: got %q want %q", got, "marcus webb")
	}
}

func TestPhraseCoreInteriorSlot(t *testing.T) {
	t.Parallel()
	// A {slot} that is NOT the last word must emit a trailing separator so the
	// following literal still matches (the non-last slot branch of phraseCore).
	cms := mustMatchers(t, "tell {agent} hello", ModeWhole)
	m := firstMatch(cms, "tell marcus hello")
	if m == nil {
		t.Fatal("expected match for interior-slot phrase")
	}
	if got := m.captures["agent"]; got != "marcus" {
		t.Errorf("interior slot capture: got %q want %q", got, "marcus")
	}
	// The literal after the slot is required.
	if firstMatch(cms, "tell marcus") != nil {
		t.Errorf("interior-slot phrase should require the trailing literal 'hello'")
	}
}

func TestPhraseCoreInteriorShortWordOptional(t *testing.T) {
	t.Parallel()
	// An interior word of ≤3 chars is optional (dictation drops it). "go to {page}"
	// has interior "to" (len 2). Whole mode: buffer IS the command.
	cms := mustMatchers(t, "go to home", ModeWhole)
	if firstMatch(cms, "go to home") == nil {
		t.Errorf("full phrase should match")
	}
	if firstMatch(cms, "go home") == nil {
		t.Errorf("dropped interior short word 'to' should still match")
	}
}

func TestPhraseCoreLongInteriorWordRequired(t *testing.T) {
	t.Parallel()
	// A >3-char interior word is NOT optional.
	cms := mustMatchers(t, "open the workspace pane", ModeWhole)
	if firstMatch(cms, "open the workspace pane") == nil {
		t.Errorf("full phrase should match")
	}
	if firstMatch(cms, "open the pane") != nil {
		t.Errorf("long interior word 'workspace' must be required, drop should not match")
	}
}

func TestMatchedTextIsCore(t *testing.T) {
	t.Parallel()
	// matchedText = capture group 1 (the wrapped core), not the whole match with
	// its surrounding whitespace/punct. Trailing mode wraps prefix + core.
	cms := mustMatchers(t, "spoken pause", ModeTrailing)
	m := firstMatch(cms, "quiet down   spoken pause")
	if m == nil {
		t.Fatal("expected trailing match")
	}
	// The core should be the phrase itself, not the leading "quiet down".
	if m.matchedText != "spoken pause" {
		t.Errorf("matchedText: got %q want %q", m.matchedText, "spoken pause")
	}
}

func TestMultiPhraseSpecEitherMatches(t *testing.T) {
	t.Parallel()
	// A command with multiple phrases compiles multiple matchers; either hits.
	cms := compilePhrases([]string{"flag speech", "flag that"}, ModeWhole)
	if len(cms) != 2 {
		t.Fatalf("expected 2 matchers, got %d", len(cms))
	}
	if firstMatch(cms, "flag speech") == nil {
		t.Errorf("first phrase should match")
	}
	if firstMatch(cms, "flag that") == nil {
		t.Errorf("second phrase should match")
	}
	if firstMatch(cms, "flag nothing") != nil {
		t.Errorf("neither phrase; should not match")
	}
}

func TestCompilePhrasesSkipsBlank(t *testing.T) {
	t.Parallel()
	// Blank / whitespace-only phrases are filtered (registry.ts:78 parity).
	cms := compilePhrases([]string{"", "   ", "\t", "real phrase"}, ModeWhole)
	if len(cms) != 1 {
		t.Fatalf("blank phrases must be skipped: got %d matchers want 1", len(cms))
	}
	if firstMatch(cms, "real phrase") == nil {
		t.Errorf("the one real phrase should match")
	}
}

func TestCompilePhrasesEmptyInput(t *testing.T) {
	t.Parallel()
	if cms := compilePhrases(nil, ModeWhole); len(cms) != 0 {
		t.Errorf("nil phrases: got %d matchers want 0", len(cms))
	}
	if cms := compilePhrases([]string{}, ModeWhole); len(cms) != 0 {
		t.Errorf("empty phrases: got %d matchers want 0", len(cms))
	}
}

func TestNoMatchReturnsNilResult(t *testing.T) {
	t.Parallel()
	cms := mustMatchers(t, "channel list", ModeWhole)
	if m := firstMatch(cms, "completely unrelated"); m != nil {
		t.Errorf("no-match must return nil, got %+v", m)
	}
}
