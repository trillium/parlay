package main

import (
	"regexp"
	"strings"
)

// ── Registry + matching engine (Go port of packages/client/src/commands/registry.ts) ──
//
// This is a faithful, compiled port of the client's phrase→regex matching engine.
// One evaluator normalizes the buffer and runs registered commands by priority;
// first match wins. Phrase→regex building carries the house dictation tolerance
// everywhere: punctuation/commas allowed between phrase words, interior words of
// ≤3 chars optional (dictation drops them), case-insensitive. {slot} tokens
// become lazy named captures validated by the command's action.
//
// PORTING FIDELITY NOTES (divergences from the JS original, all deliberate):
//
//  1. Go's regexp is RE2 — it has NO lookahead. The JS `anywhere` mode used a
//     trailing lookahead `(?=$|BOUND)` so it would not consume the boundary.
//     RE2 rejects that. Here `anywhere` consumes a trailing boundary instead
//     `(?:$|BOUND)`. No built-in currently ships in anywhere mode (`clear` is
//     trailing), but a user may rebind a command to it via settings, and for a
//     whole-box-clearing action consuming vs. asserting the boundary is
//     behaviourally identical. Documented so nobody "fixes" it back into a
//     lookahead RE2 can't compile.
//
//  2. Named captures use RE2 syntax `(?P<name>...)` instead of JS `(?<name>...)`.
//
//  3. The JS `m[1]` (first capture group) vs `m[0]` (whole match) fallback for
//     matchedText is reproduced exactly: capture group 1 is the wrapped core.

const sep = `[\s,.!?;:]+`
const bound = `[\s,.!?;:]`

// escRe escapes regex metacharacters, mirroring registry.ts:29 escRe.
var reMeta = regexp.MustCompile(`[.*+?^${}()|\[\]\\]`)

func escRe(s string) string {
	return reMeta.ReplaceAllString(s, `\$0`)
}

// slotRe matches a `{name}` token, mirroring registry.ts:36.
var slotRe = regexp.MustCompile(`^\{([a-zA-Z][a-zA-Z0-9_]*)\}$`)

// phraseCore builds the tolerant core for one phrase (port of registry.ts:32-42).
// `{slot}` words become named captures.
func phraseCore(phrase string) string {
	words := strings.Fields(strings.TrimSpace(phrase))
	var b strings.Builder
	for i, w := range words {
		last := i == len(words)-1
		if m := slotRe.FindStringSubmatch(w); m != nil {
			b.WriteString(`(?P<` + m[1] + `>.+?)`)
			if !last {
				b.WriteString(sep)
			}
			continue
		}
		interior := i > 0 && !last
		if interior && len(w) <= 3 {
			b.WriteString(`(?:` + escRe(w) + sep + `)?`)
			continue
		}
		b.WriteString(escRe(w))
		if !last {
			b.WriteString(sep)
		}
	}
	return b.String()
}

// MatchMode mirrors the client's three modes (commands/types.ts:8-11).
type MatchMode string

const (
	ModeTrailing MatchMode = "trailing" // phrase at end of buffer, text before it (submit-style)
	ModeAnywhere MatchMode = "anywhere" // phrase anywhere in the buffer (clear-style)
	ModeWhole    MatchMode = "whole"    // the buffer IS the command (tab ops)
)

// modeRegex compiles the mode-appropriate anchor around a core (port of
// registry.ts:44-50). Returns nil on a compile error so a single bad phrase can
// never take down evaluation — the caller skips a nil matcher.
func modeRegex(core string, mode MatchMode) *regexp.Regexp {
	var pat string
	switch mode {
	case ModeTrailing:
		// Leading anchor `(?:^|\s+)` so a phrase that IS the whole buffer counts as
		// trailing too — required by `clear` ("change inside input" alone must
		// clear). Safe for submit (empty-strip guard) and stop-speech (empty set).
		pat = `(?i)(?:^|\s+)(` + core + `)[.!?,;]*\s*$`
	case ModeAnywhere:
		// RE2 has no lookahead; consume the trailing boundary (see note 1).
		pat = `(?i)(?:^|` + bound + `)(` + core + `)(?:$|` + bound + `)`
	case ModeWhole:
		pat = `(?i)^\s*(` + core + `)[.!?,;]*\s*$`
	default:
		return nil
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil
	}
	return re
}

// CompiledMatcher pairs a regexp with the index of its first capture group so we
// can pull matchedText the same way the JS did (m[1] ?? m[0]).
type CompiledMatcher struct {
	re    *regexp.Regexp
	names []string // re.SubexpNames(), cached
}

// matchResult carries what a single successful match produced, mirroring the JS
// CommandMatch (commands/types.ts:13-17).
type matchResult struct {
	captures    map[string]string
	matchedText string
	value       string
}

// match runs one compiled matcher over the buffer. Returns nil on no match.
func (cm CompiledMatcher) match(value string) *matchResult {
	m := cm.re.FindStringSubmatch(value)
	if m == nil {
		return nil
	}
	caps := map[string]string{}
	for i, name := range cm.names {
		if name != "" && i < len(m) {
			caps[name] = m[i]
		}
	}
	// matchedText = first capture group (the wrapped core) ?? whole match.
	matched := m[0]
	if len(m) > 1 && m[1] != "" {
		matched = m[1]
	}
	return &matchResult{captures: caps, matchedText: matched, value: value}
}

// compilePhrases builds matchers for a list of phrases in one mode. Blank
// phrases are skipped (registry.ts:78 .filter(p => p.trim())). A phrase that
// fails to compile is skipped rather than fatal.
func compilePhrases(phrases []string, mode MatchMode) []CompiledMatcher {
	out := make([]CompiledMatcher, 0, len(phrases))
	for _, p := range phrases {
		if strings.TrimSpace(p) == "" {
			continue
		}
		re := modeRegex(phraseCore(p), mode)
		if re == nil {
			continue
		}
		out = append(out, CompiledMatcher{re: re, names: re.SubexpNames()})
	}
	return out
}
