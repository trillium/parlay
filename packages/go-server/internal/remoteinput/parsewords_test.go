package remoteinput

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func copyThat() *MatchedCommand {
	return &MatchedCommand{Name: "__copy__that__", Words: []string{"copy", "that"}, ExactWords: 2}
}

func newParseSvc(f *FakeTalon) *Service {
	return NewService(f, 0, nil)
}

// The hard rule of Stage 1: parsing executes nothing. The snippet names no
// executing action, and the service never calls the Fake's executing methods.
func TestParseWordsSnippetIsReadOnly(t *testing.T) {
	snip := parseWordsSnippet("copy that")
	for _, banned := range []string{"mimic", "actions.", "insert", "key(", "focus", "press", "exec(", "eval(", "os.system", "subprocess"} {
		if strings.Contains(snip, banned) {
			t.Errorf("snippet contains %q: parse must execute nothing\n%s", banned, snip)
		}
	}
	if !strings.Contains(snip, `speech_system.parse("copy that")`) {
		t.Errorf("snippet does not call speech_system.parse: %s", snip)
	}
}

// Hostile words stay inside one JSON string literal on one line: the repl
// compiles a single statement, and the words are data, never code.
func TestParseWordsSnippetQuotesHostileWords(t *testing.T) {
	snip := parseWordsSnippet("copy that\"); actions.mimic(\"x\nimport os")
	if strings.Contains(snip, "\n") {
		t.Errorf("snippet is not one line: %q", snip)
	}
	if !strings.Contains(snip, `speech_system.parse("copy that\"); actions.mimic(\"x\nimport os")`) {
		t.Errorf("words were not JSON-quoted into a string literal: %s", snip)
	}
}

func TestParseWordsNeverExecutes(t *testing.T) {
	f := &FakeTalon{
		ParseModes: []string{"all", "command"},
		ParseTable: map[string]*MatchedCommand{"copy that": copyThat()},
	}
	svc := newParseSvc(f)
	defer svc.Stop()
	for _, w := range []string{"copy that", "nonsense words"} {
		if _, err := svc.ParseWords(ParseWordsRequest{Words: w, Text: "typed", ActionArgs: []byte(`{"x":1}`)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.Inserts)+len(f.RunActions)+len(f.FocusAppCalls)+len(f.FocusWindowCalls) != 0 {
		t.Fatalf("parse-words executed something: inserts=%v actions=%v focus=%v/%v",
			f.Inserts, f.RunActions, f.FocusAppCalls, f.FocusWindowCalls)
	}
	if !reflect.DeepEqual(f.ParseCalls, []string{"copy that", "nonsense words"}) {
		t.Fatalf("parse calls = %v", f.ParseCalls)
	}
}

func TestParseWordsOutcomes(t *testing.T) {
	awake := []string{"all", "command"}
	asleep := []string{"all", "sleep"}
	table := map[string]*MatchedCommand{"copy that": copyThat()}
	version := func() string { return "g1" }
	for _, tc := range []struct {
		name    string
		modes   []string
		req     ParseWordsRequest
		version GrammarVersionFunc
		outcome string
		noMatch bool
		asleep  bool
	}{
		{"match", awake, ParseWordsRequest{Words: "copy that"}, nil, ParseMatched, false, false},
		{"no match awake", awake, ParseWordsRequest{Words: "flibberty"}, nil, ParseNoMatch, true, false},
		{"no match asleep is not a silent miss", asleep, ParseWordsRequest{Words: "copy that2"}, nil, ParseTalonAsleep, true, true},
		{"match while asleep still says asleep", asleep, ParseWordsRequest{Words: "copy that"}, nil, ParseMatched, false, true},
		{"commandId agrees", awake, ParseWordsRequest{Words: "copy that", CommandID: "__copy__that__"}, nil, ParseMatched, false, false},
		{"commandId differs", awake, ParseWordsRequest{Words: "copy that", CommandID: "__paste__that__"}, nil, ParseMismatch, false, false},
		{"commandId on a miss", awake, ParseWordsRequest{Words: "flibberty", CommandID: "__copy__that__"}, nil, ParseNoMatch, true, false},
		{"grammar current", awake, ParseWordsRequest{Words: "copy that", GrammarVersion: "g1"}, version, ParseMatched, false, false},
		{"grammar stale", awake, ParseWordsRequest{Words: "copy that", GrammarVersion: "g0"}, version, ParseStaleGrammar, true, false},
		{"stale wins over mismatch", awake, ParseWordsRequest{Words: "copy that", GrammarVersion: "g0", CommandID: "x"}, version, ParseStaleGrammar, true, false},
		{"no server version: check off", awake, ParseWordsRequest{Words: "copy that", GrammarVersion: "g0"}, nil, ParseMatched, false, false},
		{"no phone version: check off", awake, ParseWordsRequest{Words: "copy that"}, version, ParseMatched, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &FakeTalon{ParseModes: tc.modes, ParseTable: table}
			svc := newParseSvc(f)
			defer svc.Stop()
			resp, err := svc.ParseWords(tc.req, tc.version)
			if err != nil {
				t.Fatal(err)
			}
			if resp.Outcome != tc.outcome || resp.NoMatch != tc.noMatch || resp.Asleep != tc.asleep {
				t.Fatalf("got outcome=%s noMatch=%v asleep=%v, want %s/%v/%v (%s)",
					resp.Outcome, resp.NoMatch, resp.Asleep, tc.outcome, tc.noMatch, tc.asleep, resp.Reason)
			}
			if !reflect.DeepEqual(resp.Modes, tc.modes) {
				t.Fatalf("modes = %v, want %v: a sleeping Talon must be visible", resp.Modes, tc.modes)
			}
			if tc.noMatch && resp.MatchedCommand != nil {
				t.Fatalf("matchedCommand set on %s", tc.outcome)
			}
		})
	}
}

func TestParseWordsTalonErrorPropagates(t *testing.T) {
	f := &FakeTalon{ParseErr: errors.New("repl gone")}
	svc := newParseSvc(f)
	defer svc.Stop()
	if _, err := svc.ParseWords(ParseWordsRequest{Words: "copy that"}, nil); err == nil {
		t.Fatal("transport failure must be an error, not a silent no-match")
	}
}

func TestIsAsleep(t *testing.T) {
	for modes, want := range map[string]bool{
		"all command":       false,
		"all dictation":     false,
		"all command sleep": true,
		"all sleep":         true,
		"all":               true,
		"":                  true,
		"all noise hotkey":  true,
		"command dictation": false,
	} {
		if got := IsAsleep(strings.Fields(modes)); got != want {
			t.Errorf("IsAsleep(%q) = %v, want %v", modes, got, want)
		}
	}
}

func TestNormalizeWords(t *testing.T) {
	for in, want := range map[string]string{
		"Copy that.":       "copy that",
		"  Copy   THAT!  ": "copy that",
		`"Select" all,`:    "select all",
		"don't stop":       "don't stop",
		"...":              "",
		"":                 "",
		"go to line 5?":    "go to line 5",
	} {
		if got := NormalizeWords(in); got != want {
			t.Errorf("NormalizeWords(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseParseWordsJSON(t *testing.T) {
	out := "some warning\n" + `{"modes": ["command", "all"], "match": {"name": "__copy__that__", "words": ["copy", "that"], "exactWords": 2, "lists": ["user.onscreen_ocr_text"]}}`
	res, err := parseParseWordsJSON(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Modes, []string{"all", "command"}) {
		t.Errorf("modes not sorted: %v", res.Modes)
	}
	if res.Match == nil || res.Match.Name != "__copy__that__" || res.Match.ExactWords != 2 ||
		!reflect.DeepEqual(res.Match.DynamicLists, []string{"user.onscreen_ocr_text"}) {
		t.Errorf("match = %+v", res.Match)
	}
	res, err = parseParseWordsJSON(`{"modes": ["all"], "match": null}`)
	if err != nil || res.Match != nil {
		t.Errorf("null match: %+v %v", res, err)
	}
	if _, err := parseParseWordsJSON("Traceback (most recent call last)"); err == nil {
		t.Error("a Python error must not decode as a no-match")
	}
}
