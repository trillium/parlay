// Parse-words (task-z0caa, Talon-on-iPhone Stage 1): recognized WORDS in,
// the command Talon would resolve them to out. The phone never matches
// anything itself; the answer comes from the same speech_system.parse()
// the desktop engine uses, so matching semantics are identical by
// construction.
//
// READ-ONLY, by hard rule: parse() resolves words to a command and runs
// nothing. This file must never reach actions.mimic, actions.insert,
// actions.key or any focus call; executing a resolved command stays held
// under task-dhszn. parsewords_test.go pins that on the snippet text and
// on the Fake's recorded calls.
//
// The Talon mode set rides along on every answer because an ASLEEP Talon
// is otherwise indistinguishable from a grammar miss: with `sleep` active
// parse() falls through to nothing useful and a bare noMatch would read as
// "that is not a command". The response says asleep, and why, instead.
package remoteinput

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// MaxParseWordsLen caps the words field in runes. A spoken command is a
// handful of words; the cap keeps one request from feeding Talon a page.
const MaxParseWordsLen = 200

// Parse-words outcomes (the `outcome` field of ParseWordsResponse).
const (
	// ParseMatched: parse() resolved the words to a command.
	ParseMatched = "matched"
	// ParseNoMatch: parse() resolved nothing and Talon is awake.
	ParseNoMatch = "no_match"
	// ParseMismatch: the caller's commandId is not what parse() resolved.
	ParseMismatch = "parse_mismatch"
	// ParseStaleGrammar: the caller's grammarVersion is not the desktop's.
	ParseStaleGrammar = "stale_grammar"
	// ParseTalonAsleep: no match AND Talon is asleep, so the miss says
	// nothing about the grammar.
	ParseTalonAsleep = "talon_asleep"
)

// ParseWordsRequest is the POST wire shape. words is the only field
// parse needs; the rest is the report's command wire format, accepted so
// the phone can send one shape in Stage 1 and Stage 3:
//
//   - commandId, when set, is cross-checked against what parse() resolved
//     (a differing id is parse_mismatch).
//   - grammarVersion, when set, is checked against the desktop's version
//     (a differing one is stale_grammar, which outranks parse_mismatch).
//   - actionArgs and text are accepted and ignored: nothing executes in
//     Stage 1, so there is nothing to pass them to.
type ParseWordsRequest struct {
	Words          string          `json:"words"`
	CommandID      string          `json:"commandId,omitempty"`
	ActionArgs     json.RawMessage `json:"actionArgs,omitempty"`
	Text           string          `json:"text,omitempty"`
	GrammarVersion string          `json:"grammarVersion,omitempty"`
}

// MatchedCommand is what parse() resolved the words to.
type MatchedCommand struct {
	// Name is Talon's rule key (e.g. "__copy__that__"): the same id the
	// grammar export uses, and what commandId is compared against.
	Name string `json:"name"`
	// Words are the pieces the matched rule consumed: literal words, the
	// value of a list capture, or "<name>" for a nested capture.
	Words []string `json:"words"`
	// ExactWords counts literal words matched (Talon's specificity input).
	ExactWords int `json:"exactWords"`
	// DynamicLists names the dynamic lists parse() consulted (e.g.
	// "user.onscreen_ocr_text"): a match that depends on live screen text
	// cannot be reproduced from a static snapshot.
	DynamicLists []string `json:"dynamicLists,omitempty"`
}

// ParseResult is one Talon readout: the match (nil on none) and the mode set.
type ParseResult struct {
	Match *MatchedCommand `json:"match"`
	Modes []string        `json:"modes"`
}

// ParseWordsResponse is the 200 wire shape.
type ParseWordsResponse struct {
	Outcome string `json:"outcome"`
	// Words echoes the normalized words that were parsed.
	Words          string          `json:"words"`
	MatchedCommand *MatchedCommand `json:"matchedCommand"`
	NoMatch        bool            `json:"noMatch"`
	// Modes is Talon's active mode set, sorted.
	Modes []string `json:"modes"`
	// Asleep is true when Talon is not listening for commands. A noMatch
	// with asleep set is a sleeping Talon, not a grammar miss.
	Asleep bool `json:"asleep"`
	// GrammarVersion is the desktop's version, when the server has one.
	GrammarVersion string `json:"grammarVersion,omitempty"`
	// Reason explains any outcome other than matched.
	Reason string `json:"reason,omitempty"`
}

// NormalizeWords reduces what a phone recogniser returns to the form the
// desktop engine feeds parse(): lowercase, single-spaced, with sentence
// punctuation stripped from word edges (iOS returns "Copy that."). The
// result is empty when nothing is left.
func NormalizeWords(s string) string {
	fields := strings.Fields(strings.ToLower(s))
	out := fields[:0]
	for _, f := range fields {
		f = strings.TrimFunc(f, func(r rune) bool {
			return unicode.IsPunct(r) && r != '\'' || unicode.IsSymbol(r)
		})
		if f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, " ")
}

// IsAsleep reports whether a Talon mode set means it is not listening for
// commands: `sleep` is active, or neither `command` nor `dictation` is.
func IsAsleep(modes []string) bool {
	command, dictation := false, false
	for _, m := range modes {
		switch m {
		case "sleep":
			return true
		case "command":
			command = true
		case "dictation":
			dictation = true
		}
	}
	return !command && !dictation
}

// parseWordsSnippet builds the one-statement, read-only Talon program: a
// single print of a JSON dump of speech_system.parse(words) plus the mode
// set. words is JSON-quoted into a Python string literal and never formatted
// in raw. Pure function so tests prove the shape and the absence of any
// executing action without a live Talon.
//
// parse() returns None on no match, else (VMCapture, set, bool) where
// VMCapture.data[0] is the matched rule capture and the set names the dynamic
// lists consulted.
func parseWordsSnippet(words string) string {
	q, _ := json.Marshal(words)
	return fmt.Sprintf(`print(__import__("json").dumps((lambda r, m: (lambda c: {"modes": m, "match": (None if c is None else {"name": str(getattr(c, "name", "")), "words": [w if isinstance(w, str) else (str(w.value) if hasattr(w, "value") else "<" + str(getattr(w, "name", "")) + ">") for w in getattr(c, "data", ())], "exactWords": int(getattr(c, "exact_words", 0)), "lists": sorted(str(x) for x in r[1])})})(r[0].data[0] if r and r[0].data else None))(speech_system.parse(%s), sorted(scope.get("mode") or []))))`, string(q))
}

// parseParseWordsJSON decodes one repl readout. The repl may print other
// lines (warnings) first, so the JSON is the last non-empty line.
func parseParseWordsJSON(out string) (ParseResult, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	var wire struct {
		Modes []string `json:"modes"`
		Match *struct {
			Name       string   `json:"name"`
			Words      []string `json:"words"`
			ExactWords int      `json:"exactWords"`
			Lists      []string `json:"lists"`
		} `json:"match"`
	}
	if err := json.Unmarshal([]byte(last), &wire); err != nil {
		return ParseResult{}, fmt.Errorf("talon parse: bad JSON: %w (output %q)", err, truncate(out, 200))
	}
	res := ParseResult{Modes: wire.Modes}
	sort.Strings(res.Modes)
	if res.Modes == nil {
		res.Modes = []string{}
	}
	if wire.Match != nil {
		res.Match = &MatchedCommand{
			Name: wire.Match.Name, Words: wire.Match.Words,
			ExactWords: wire.Match.ExactWords, DynamicLists: wire.Match.Lists,
		}
		if res.Match.Words == nil {
			res.Match.Words = []string{}
		}
	}
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ParseWords resolves words through Talon's speech_system.parse. One
// bounded repl call; read-only (no focus, no keystroke, no action).
func (t *REPLTalon) ParseWords(words string) (ParseResult, error) {
	out, err := t.runSnippet(parseWordsSnippet(words))
	if err != nil {
		return ParseResult{}, err
	}
	return parseParseWordsJSON(out)
}

// GrammarVersionFunc reports the desktop's current grammar version, or ""
// when the server has none to compare against.
type GrammarVersionFunc func() string

// ParseWords answers one parse-words request through the service so the
// handler stays a thin wire adapter. It never queues and never touches the
// injection pipeline. words must already be validated and normalized.
func (s *Service) ParseWords(req ParseWordsRequest, version GrammarVersionFunc) (ParseWordsResponse, error) {
	var want string
	if version != nil {
		want = version()
	}
	res, err := s.talon.ParseWords(req.Words)
	if err != nil {
		return ParseWordsResponse{}, err
	}
	resp := ParseWordsResponse{
		Words: req.Words, GrammarVersion: want, Modes: res.Modes,
		Asleep: IsAsleep(res.Modes), MatchedCommand: res.Match, NoMatch: res.Match == nil,
	}
	// Grammar version outranks everything below: a phone holding a stale
	// grammar cannot trust its own command id. Talon is still read so modes
	// (and a sleeping Talon) are reported on every outcome.
	if req.GrammarVersion != "" && want != "" && req.GrammarVersion != want {
		resp.Outcome = ParseStaleGrammar
		resp.MatchedCommand, resp.NoMatch = nil, true
		resp.Reason = fmt.Sprintf("grammarVersion %q is not the desktop's %q: refetch the grammar", req.GrammarVersion, want)
		return resp, nil
	}
	switch {
	case res.Match == nil && resp.Asleep:
		resp.Outcome = ParseTalonAsleep
		resp.Reason = "Talon is asleep (modes: " + strings.Join(res.Modes, ", ") + "): this miss says nothing about the grammar"
	case res.Match == nil:
		resp.Outcome = ParseNoMatch
		resp.Reason = "no command matches these words"
	case req.CommandID != "" && req.CommandID != res.Match.Name:
		resp.Outcome = ParseMismatch
		resp.Reason = fmt.Sprintf("commandId %q is not what the desktop resolved: %q", req.CommandID, res.Match.Name)
	default:
		resp.Outcome = ParseMatched
		if resp.Asleep {
			resp.Reason = "Talon is asleep: this is what parse() resolves, but the desktop would not act on it"
		}
	}
	return resp, nil
}
