package evalengine

import "strings"

// ── The closed compiled surface: resolvers, transforms, handlers, verbs ─────────
//
// This file is the MACHINERY half of the machinery/policy split (see
// docs/COMMAND_DESIGN_CONTRACT.md). A manifest (DATA) may only reference names
// that appear in these closed registries; anything else is rejected at load
// (fail-closed). Adding an entry here is the one legitimate reason to recompile —
// a genuinely new capability. Recombining existing entries is pure data.
//
// The registries hold the SAME logic the old inline runAction switch held; they
// were extracted verbatim from commands.go so the interpreter produces a
// byte-identical actionList to the pre-externalization binary.

// evalCtx carries everything a resolver/transform needs while interpreting one
// matched command's emit. It is the compiled context the DOM-free engine sees:
// the raw captures, the matched trailing phrase, the whole buffer, and the live
// tab set the client reported.
type evalCtx struct {
	captures    map[string]string
	matchedText string
	buffer      string
	mode        MatchMode
	tabs        []Tab
}

// resolver is a named dynamic lookup. It takes a spoken string (already
// interpolated from the arg's `from`) plus context, and returns a resolved value
// (string OR []PickerChannel) and whether it hit. A miss (ok=false) skips the
// action and lets emit.onResolveFail decide the pass outcome.
type resolver func(input string, ctx *evalCtx) (value any, ok bool)

// transform is a pure string function. It never fails — an empty result is a
// legitimate value (stop-speech's stripTrigger can legitimately empty the box).
type transform func(input string, ctx *evalCtx) string

// resolverRegistry is the closed set of named resolvers. channelSelection is
// registered for closed-vocabulary completeness; its authoritative multi-return
// form (id, cancel, ok) is invoked directly by the channel-select MODE machinery
// in engine.go (a mode is a named entry-point, not a manifest command).
var resolverRegistry = map[string]resolver{
	"agent":               resolveAgentR,
	"page":                resolvePageR,
	"number":              resolveNumberR,
	"channelList":         resolveChannelListR,
	"channelSelection":    resolveChannelSelectionR,
	"recentSenders":       resolveRecentSendersR,
	"sentenceDeleteStart": resolveSentenceDeleteStartR,
	"cursorPos":           resolveCursorPosR,
}

// transformRegistry is the closed set of named pure string transforms.
var transformRegistry = map[string]transform{
	"slugify":      slugifyT,
	"stripTrigger": stripTriggerT,
}

// handlerRegistry is the closed set of named stateful handlers. A handler owns
// its own state and reflex timing (the machinery); the manifest may only select
// and configure one. `submit` is the sole handler — its 1s server-owned
// arm/verify/strip/submit countdown lives in engine.go (armSubmit/fireSubmit).
var handlerRegistry = map[string]bool{
	"submit": true,
}

// actionVerbs is exactly today's actions.go set — the only effects the engine can
// order. A manifest referencing a verb outside this set is rejected at load. A new
// verb ⇒ a Go change here ⇒ a new client dispatcher case: the intended, auditable
// coupling.
var actionVerbs = map[string]bool{
	"clear": true, "setText": true, "submitNow": true, "armTimer": true,
	"cancelTimer": true, "showHint": true, "clearHint": true, "noop": true,
	"switchTab": true, "archiveTab": true, "nextTab": true, "prevTab": true,
	"navigate": true, "stopSpeech": true, "flagSpeech": true,
	"openChannelPicker": true, "closeChannelPicker": true, "pickerHint": true,
	"openSenderPicker": true, "closeSenderPicker": true, "senderPickerHint": true,
	"openSwitcher": true, "replaceRange": true,
}

// ── Resolver implementations (extracted from the old runAction switch) ──────────

// resolveAgentR wraps resolveAgent: exact→substring over id/name/nickname. A miss
// (empty id) is the fall-through signal switch-tab/archive-tab rely on.
func resolveAgentR(input string, ctx *evalCtx) (any, bool) {
	id := resolveAgent(input, ctx.tabs)
	return id, id != ""
}

// resolvePageR reproduces the old go-to-page guard exactly: trim, lowercase, strip
// trailing punctuation, and if what remains is empty MISS (so onResolveFail:
// fallthrough fires, matching `if raw == "" { return false }`). Otherwise it wraps
// the slug as `/multi-word-slug/`.
func resolvePageR(input string, ctx *evalCtx) (any, bool) {
	raw := trimTrailingPunct(strings.ToLower(strings.TrimSpace(input)))
	if raw == "" {
		return "", false
	}
	return "/" + strings.ReplaceAll(raw, " ", "-") + "/", true
}

// resolveNumberR resolves a spoken number/ordinal to a tab id by 1-based index
// against the live tab set. Out-of-range or non-number ⇒ miss.
func resolveNumberR(input string, ctx *evalCtx) (any, bool) {
	q := trimTrailingPunct(strings.TrimSpace(strings.ToLower(input)))
	if n, hit := parseChannelNumber(q); hit && n >= 1 && n <= len(ctx.tabs) {
		return ctx.tabs[n-1].ID, true
	}
	return "", false
}

// resolveChannelListR returns the authoritative 1-based channel list for the
// openChannelPicker verb. It reads only the live tabs; it never misses.
func resolveChannelListR(_ string, ctx *evalCtx) (any, bool) {
	return buildPickerChannels(ctx.tabs), true
}

// resolveChannelSelectionR is the arg-usable projection of resolveChannelSelection
// (number→name→nickname). It discards the cancel signal — the channel-select mode
// path in engine.go calls resolveChannelSelection directly to see cancel.
func resolveChannelSelectionR(input string, ctx *evalCtx) (any, bool) {
	id, _, ok := resolveChannelSelection(input, ctx.tabs)
	return id, ok
}

// ── Sender resolver (for iMessage reply tool) ───────────────────────────────────

// resolveRecentSendersR returns the list of recent iMessage senders, formatted
// as PickerSender[] for the openSenderPicker action. On error, returns an empty list.
func resolveRecentSendersR(_ string, ctx *evalCtx) (any, bool) {
	senders := getRecentSenders(5)
	return buildPickerSenders(senders), true
}

// ── Edit-action resolvers (`change sentence`, discussion #246) ─────────────────
//
// Both resolvers below rely on a property specific to ModeTrailingCursor matching
// (engine.go runPass): ctx.buffer IS the buffer truncated at the cursor (not the
// whole buffer), because that's the substring the command matched against. So
// len(ctx.buffer) IS the cursor position, and everything at/after it is simply not
// present here — deletion can never reach past the cursor without extra plumbing.

// resolveSentenceDeleteStartR finds where a `replaceRange` deleting `change
// sentence` should START: the beginning of the sentence immediately before the
// trigger phrase. Combined with resolveCursorPosR as `end`, the single
// replaceRange removes the sentence core AND the trigger phrase itself (plus its
// auto-inserted leading whitespace) in one shot — see docs/agent-notes for the
// worked trace. A miss (ok=false) is the empty-input no-op: nothing before the
// trigger phrase to delete.
func resolveSentenceDeleteStartR(_ string, ctx *evalCtx) (any, bool) {
	triggerStart := lastIndexFold(ctx.buffer, ctx.matchedText)
	if triggerStart < 0 {
		return 0, false
	}
	prefix := strings.TrimRight(ctx.buffer[:triggerStart], " \t\n\r")
	if prefix == "" {
		return 0, false
	}
	segs := splitSentences(prefix)
	if len(segs) == 0 {
		return 0, false
	}
	return segs[len(segs)-1].coreStart, true
}

// resolveCursorPosR resolves to the cursor position. Under ModeTrailingCursor
// matching, ctx.buffer is exactly the buffer up to the cursor, so its length IS
// that position — always a hit (a cursor position always exists).
func resolveCursorPosR(_ string, ctx *evalCtx) (any, bool) {
	return len(ctx.buffer), true
}

// ── Transform implementations ───────────────────────────────────────────────────

// slugifyT is the pure `/slugified/` form: trim, lowercase, strip trailing punct,
// spaces→`-`, wrapped in slashes. (resolvePageR performs the same slug but adds
// the empty-guard miss; slugify stays a pure transform available for reuse.)
func slugifyT(input string, _ *evalCtx) string {
	raw := trimTrailingPunct(strings.ToLower(strings.TrimSpace(input)))
	return "/" + strings.ReplaceAll(raw, " ", "-") + "/"
}

// stripTriggerT removes the matched trailing phrase from the buffer and returns
// the remainder, right-trimmed of whitespace — the exact strip the old stop-speech
// case did (lastIndexFold(m.value, m.matchedText) then TrimRight). It reads the
// match context, not its `input` arg (the manifest's `from: "buffer"` is
// declarative); an empty remainder is a valid result, never a fall-through.
func stripTriggerT(_ string, ctx *evalCtx) string {
	idx := lastIndexFold(ctx.buffer, ctx.matchedText)
	if idx >= 0 {
		return strings.TrimRight(ctx.buffer[:idx], " \t\n")
	}
	return ctx.buffer
}
