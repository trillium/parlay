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
	{id: "clear", phrases: []string{"change inside in input"}, mode: ModeAnywhere, priority: 10,
		description: "Saying this anywhere in the input empties the whole box"},
	{id: "switch-tab", phrases: []string{"switch to {agent}", "go to {agent}", "show me {agent}"}, mode: ModeWhole, priority: 20,
		description: "Switch the active agent tab by name"},
	{id: "archive-tab", phrases: []string{"archive {agent}", "archive tab {agent}"}, mode: ModeWhole, priority: 20,
		description: "Archive an agent tab by name"},
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
// client sent up. Port of ctx.ts:75-85 resolveAgent: exact id/name, then
// substring, case-insensitive.
func resolveAgent(spoken string, tabs []Tab) string {
	q := strings.TrimSpace(strings.ToLower(spoken))
	q = trimTrailingPunct(q)
	if q == "" {
		return ""
	}
	for _, t := range tabs {
		if strings.ToLower(t.ID) == q || strings.ToLower(t.Name) == q {
			return t.ID
		}
	}
	for _, t := range tabs {
		if strings.Contains(strings.ToLower(t.ID), q) || strings.Contains(strings.ToLower(t.Name), q) {
			return t.ID
		}
	}
	return ""
}

var trailingPunctRe = regexp.MustCompile(`[.!?,;:]+$`)

func trimTrailingPunct(s string) string {
	return trailingPunctRe.ReplaceAllString(s, "")
}

// runAction executes a matched command's action, appending actions to `out`.
// Returns handled=false to signal "not handled — continue the pass" exactly like
// the JS actions that return false (registry.ts:103), letting switch-tab fall
// through to go-to-page.
//
// The `submit` command is intentionally NOT handled here — it is stateful and is
// driven by the engine's per-stream submitMachine (engine.go), because in the
// pure model the countdown is server-owned. runAction handles the stateless
// commands (everything that maps 1:1 onto a client action with no timer).
func runAction(spec commandSpec, m *matchResult, tabs []Tab, out *actionList) (handled bool) {
	switch spec.id {
	case "clear":
		out.add(actClear())
		return true

	case "stop-speech":
		// Silence speech + strip the trailing phrase from the buffer.
		idx := lastIndexFold(m.value, m.matchedText)
		var stripped string
		if idx >= 0 {
			stripped = strings.TrimRight(m.value[:idx], " \t\n")
		} else {
			stripped = m.value
		}
		out.add(actStopSpeech())
		out.add(actSetText(stripped))
		return true

	case "flag-speech":
		out.add(actFlagSpeech())
		out.add(actClear())
		return true

	case "switch-tab":
		id := resolveAgent(m.captures["agent"], tabs)
		if id == "" {
			return false // unknown agent — let go-to-page try
		}
		out.add(actSwitchTab(id))
		out.add(actClear())
		return true

	case "archive-tab":
		id := resolveAgent(m.captures["agent"], tabs)
		if id == "" {
			return false
		}
		out.add(actArchiveTab(id))
		out.add(actClear())
		return true

	case "next-tab":
		out.add(actNextTab())
		out.add(actClear())
		return true

	case "prev-tab":
		out.add(actPrevTab())
		out.add(actClear())
		return true

	case "go-to-page":
		raw := trimTrailingPunct(strings.ToLower(strings.TrimSpace(m.captures["page"])))
		if raw == "" {
			return false
		}
		url := "/" + strings.ReplaceAll(raw, " ", "-") + "/"
		out.add(actNavigate(url))
		out.add(actClear())
		return true

	case "submit":
		// Handled by the stateful submitMachine, not here. Reaching this means
		// the caller mis-routed; treat as not-handled so nothing fires by accident.
		return false
	}
	return false
}

// lastIndexFold is a case-insensitive lastIndexOf, mirroring the JS
// val.toLowerCase().lastIndexOf(matchedTail.toLowerCase()) used by the submit and
// stop-speech strip logic (builtins.ts:28, 57). Returns a byte index into the
// ORIGINAL (non-lowered) string so slicing preserves the user's casing.
func lastIndexFold(haystack, needle string) int {
	return strings.LastIndex(strings.ToLower(haystack), strings.ToLower(needle))
}
