package main

import (
	"regexp"
	"strings"
)

// ── Built-in commands (Go port of packages/client/src/commands/builtins.ts) ────
//
// The nine built-ins, ported to compiled Go. Each command declares its id,
// default phrases, match mode, and priority (lower wins; first match ends the
// pass). The ACTION of each command, instead of touching a DOM CommandContext,
// emits action-protocol verbs (see actions.go) that the client dispatcher applies.
//
// The one stateful command — `submit` — owns a 1s arm/verify/strip/submit timer.
// In the PURE server-side model that timer lives HERE, on the server (see
// engine.go submitMachine), not in the browser. That is the whole point of this
// build: the captain wants to feel the network cost of a server-owned reflex.

// commandSpec is the static description of a command.
type commandSpec struct {
	id          string
	phrases     []string
	mode        MatchMode
	priority    int
	description string
}

// builtins in registration order; the engine sorts by priority ascending.
// Mirrors builtins.ts:134-136 exactly (same set, same priorities, same phrases).
var builtins = []commandSpec{
	{id: "stop-speech", phrases: []string{"spoken pause"}, mode: ModeTrailing, priority: 5,
		description: "End the input with this to instantly silence current speech"},
	{id: "flag-speech", phrases: []string{"flag speech", "flag that"}, mode: ModeWhole, priority: 8,
		description: "Report the last-spoken sentence as mispronounced"},
	{id: "clear", phrases: []string{"change inside input", "change inside in input"}, mode: ModeTrailing, priority: 10,
		description: "End the input with a clear phrase to empty the whole box"},
	{id: "switch-tab", phrases: []string{"switch to {agent}", "go to {agent}", "show me {agent}", "channel switch {agent}"}, mode: ModeWhole, priority: 20,
		description: "Switch the active agent tab by name"},
	{id: "archive-tab", phrases: []string{"archive {agent}", "archive tab {agent}"}, mode: ModeWhole, priority: 20,
		description: "Archive an agent tab by name"},
	{id: "channel-list", phrases: []string{"channel list", "list channels", "show channels"}, mode: ModeWhole, priority: 20,
		description: "Open the agent switcher to show available channels"},
	{id: "next-tab", phrases: []string{"next tab", "next agent"}, mode: ModeWhole, priority: 20,
		description: "Switch to the next agent tab"},
	{id: "prev-tab", phrases: []string{"previous tab", "previous agent", "last tab"}, mode: ModeWhole, priority: 20,
		description: "Switch to the previous agent tab"},
	{id: "go-to-page", phrases: []string{"go to {page}", "open {page}", "show {page}", "workspace {page}"}, mode: ModeWhole, priority: 25,
		description: "Open a Pulse page in the workspace pane"},
	{id: "submit", phrases: []string{"bravely", "gravely", "briefly", "lap"}, mode: ModeTrailing, priority: 30,
		description: "End a message with this word to auto-send it after 1s"},
}

// resolveAgentFn resolves a spoken {agent} capture against the live tab set the
// client sent up. Port of ctx.ts:75-85 resolveAgent: exact id/name/nickname, then
// substring id/name/nickname, case-insensitive. Nicknames (CHANNEL_PICKER_CONTRACT)
// participate on the same footing as id/name.
func resolveAgent(spoken string, tabs []Tab) string {
	q := strings.TrimSpace(strings.ToLower(spoken))
	q = trimTrailingPunct(q)
	if q == "" {
		return ""
	}
	// Pass 1: exact match on id, name, or any nickname.
	for _, t := range tabs {
		if strings.ToLower(t.ID) == q || strings.ToLower(t.Name) == q {
			return t.ID
		}
		for _, nick := range t.Nicknames {
			if strings.ToLower(strings.TrimSpace(nick)) == q {
				return t.ID
			}
		}
	}
	// Pass 2: substring match on id, name, or any nickname.
	for _, t := range tabs {
		if strings.Contains(strings.ToLower(t.ID), q) || strings.Contains(strings.ToLower(t.Name), q) {
			return t.ID
		}
		for _, nick := range t.Nicknames {
			n := strings.ToLower(strings.TrimSpace(nick))
			if n != "" && strings.Contains(n, q) {
				return t.ID
			}
		}
	}
	return ""
}

// pickerPrompt is the instruction line the frontend renders above the numbered
// list (CHANNEL_PICKER_CONTRACT §Actions openChannelPicker.args.prompt).
const pickerPrompt = "Say a channel name, nickname, or number"

// buildPickerChannels turns the ordered tab set into the authoritative 1-based
// channel list the picker speaks against. label = the channel NAME; nickname =
// first nickname else "" — a SECONDARY hint the frontend renders as "Name (nick)"
// (CHANNEL_PICKER_CONTRACT §Actions example: label "Mayor", nickname "boss"). The
// previous code overrode label with the nickname, which hid the real name AND made
// the frontend's "(nickname)" paren dead (it only draws when nickname != label).
// The order MUST match what a later channel-select request carries so numbers stay
// stable — both derive from the same client-sent tabs order.
func buildPickerChannels(tabs []Tab) []PickerChannel {
	channels := make([]PickerChannel, 0, len(tabs))
	for i, t := range tabs {
		channels = append(channels, PickerChannel{
			Index:    i + 1,
			ID:       t.ID,
			Label:    t.Name,
			Nickname: firstNickname(t.Nicknames),
		})
	}
	return channels
}

// firstNickname returns the first non-blank nickname (trimmed), or "".
func firstNickname(nicks []string) string {
	for _, n := range nicks {
		if s := strings.TrimSpace(n); s != "" {
			return s
		}
	}
	return ""
}

// numberWords maps spoken cardinals and ordinals to a 1-based position. The
// contract requires "one".."ten" and "first".."tenth".
var numberWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5,
	"sixth": 6, "seventh": 7, "eighth": 8, "ninth": 9, "tenth": 10,
}

// cancelWords are the spoken phrases that dismiss the picker with no switch
// (CHANNEL_PICKER_CONTRACT §Resolution rule 4). "never mind" is normalized to
// "nevermind" before lookup so the two-word and one-word forms both hit.
var cancelWords = map[string]bool{
	"close": true, "cancel": true, "nevermind": true,
	"dismiss": true, "exit": true,
}

// leadingNumberNoiseRe strips a leading "channel"/"number" filler word so
// "channel 2" / "number 2" resolve by their trailing digit.
var leadingNumberNoiseRe = regexp.MustCompile(`^(?:channel|number)\s+`)

// resolveChannelSelection resolves picker-input text to a channel id, following
// CHANNEL_PICKER_CONTRACT §Resolution rules IN ORDER: (1) number/ordinal, (2)
// exact id/name/nickname, (3) substring id/name/nickname, (4) cancel words, (5)
// no match. Returns (id, cancel, ok): ok=true with an id on a hit; cancel=true on
// a cancel word; both false on no match.
func resolveChannelSelection(spoken string, tabs []Tab) (id string, cancel bool, ok bool) {
	q := strings.TrimSpace(strings.ToLower(spoken))
	q = trimTrailingPunct(q)
	q = strings.TrimSpace(q)
	if q == "" {
		return "", false, false
	}

	// Rule 1: number / ordinal → tabs[n-1].
	if n, hit := parseChannelNumber(q); hit {
		if n >= 1 && n <= len(tabs) {
			return tabs[n-1].ID, false, true
		}
		// A parsed number outside range is not a valid pick and not a cancel; fall
		// through so it can still miss (rules 2-3 won't hit a bare number).
	}

	// Rule 2: exact id / name / any nickname.
	for _, t := range tabs {
		if strings.ToLower(t.ID) == q || strings.ToLower(t.Name) == q {
			return t.ID, false, true
		}
		for _, nick := range t.Nicknames {
			if strings.ToLower(strings.TrimSpace(nick)) == q {
				return t.ID, false, true
			}
		}
	}

	// Rule 3: substring id / name / any nickname, first match wins.
	for _, t := range tabs {
		if strings.Contains(strings.ToLower(t.ID), q) || strings.Contains(strings.ToLower(t.Name), q) {
			return t.ID, false, true
		}
		for _, nick := range t.Nicknames {
			n := strings.ToLower(strings.TrimSpace(nick))
			if n != "" && strings.Contains(n, q) {
				return t.ID, false, true
			}
		}
	}

	// Rule 4: cancel words. Normalize "never mind" → "nevermind".
	normalized := strings.Join(strings.Fields(q), "")
	if cancelWords[q] || cancelWords[normalized] {
		return "", true, false
	}

	// Rule 5: no match.
	return "", false, false
}

// parseChannelNumber extracts a 1-based position from a spoken number/ordinal.
// Handles bare digits ("2"), digits with a leading filler ("channel 2",
// "number 2"), and number words ("two", "second"). Returns (n, true) only when
// the whole (noise-stripped) string is a single number token.
func parseChannelNumber(q string) (int, bool) {
	s := leadingNumberNoiseRe.ReplaceAllString(q, "")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if n, ok := numberWords[s]; ok {
		return n, true
	}
	// Pure digits only (reject "2 channels", "channel 2 please").
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n, true
}

var trailingPunctRe = regexp.MustCompile(`[.!?,;:]+$`)

func trimTrailingPunct(s string) string {
	return trailingPunctRe.ReplaceAllString(s, "")
}

// The old runAction switch that hardcoded each command's behavior was deleted once
// the manifest interpreter (interp.go) was proven to produce an identical
// actionList for every command. Command behavior is now DATA (default_commands.json
// / a loaded manifest), interpreted by interpretSequence + the closed registries.
// Only the stateless resolvers/helpers below remain — they moved into named
// registry entries (registries.go) but keep their exact logic.

// lastIndexFold is a case-insensitive lastIndexOf, mirroring the JS
// val.toLowerCase().lastIndexOf(matchedTail.toLowerCase()) used by the submit and
// stop-speech strip logic (builtins.ts:28, 57). Returns a byte index into the
// ORIGINAL (non-lowered) string so slicing preserves the user's casing.
func lastIndexFold(haystack, needle string) int {
	return strings.LastIndex(strings.ToLower(haystack), strings.ToLower(needle))
}
