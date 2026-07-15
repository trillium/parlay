package main

// ── The ACTION PROTOCOL (Go side; mirrors brain-v4vje §1) ──────────────────────
//
// The engine never manipulates the DOM. It emits a small, closed vocabulary of
// ACTIONS against a versioned snapshot of the input buffer. The client owns a
// dispatcher (dispatcher.ts) that validates each action against current local
// state and applies or rejects it. This keeps the engine declarative and the
// client authoritative over its own text field.
//
// The envelope fields (v, streamId, seq, baseVersion, ttlMs) are attached by the
// engine when it serializes the batch (engine.go), not per-action here — an
// Action carries only its verb + args; the batch carries the envelope.

// ProtocolVersion is the action-protocol major version. The client rejects
// unknown major versions (dispatcher.ts).
const ProtocolVersion = 1

// Action is one verb + its args. args uses a flat typed struct rather than a
// map so the JSON contract is explicit and the client's TypeScript types line up
// 1:1. Only the fields relevant to `verb` are populated; the rest stay zero and
// are omitted from JSON.
type Action struct {
	Verb string    `json:"verb"`
	Args ActionArg `json:"args"`
}

// ActionArg is the union of every verb's arguments. omitempty keeps the wire
// payload tight and makes the client's discriminated-union parsing clean.
type ActionArg struct {
	// replaceRange / setText
	Start *int    `json:"start,omitempty"`
	End   *int    `json:"end,omitempty"`
	Text  *string `json:"text,omitempty"`

	// stripTrigger
	TriggerText string `json:"triggerText,omitempty"`
	Tail        bool   `json:"tail,omitempty"`

	// submitNow
	RequireTail string `json:"requireTail,omitempty"`

	// armTimer / cancelTimer
	TimerID  string `json:"timerId,omitempty"`
	FireInMs int    `json:"fireInMs,omitempty"`

	// showHint / clearHint
	HintID   string `json:"id,omitempty"`
	HintKind string `json:"kind,omitempty"` // "info" | "warn"

	// tab/nav/speech targets
	Channel string `json:"channel,omitempty"`
	URL     string `json:"url,omitempty"`

	// noop
	Reason string `json:"reason,omitempty"`
}

// actionList accumulates actions in emission order.
type actionList struct {
	items []Action
}

func (l *actionList) add(a Action) { l.items = append(l.items, a) }

func intp(v int) *int          { return &v }
func strp(v string) *string    { return &v }

// ── Verb constructors ─────────────────────────────────────────────────────────

// setText replaces the WHOLE buffer (a replaceRange over [0, len)). Maps onto
// the client's ctx.input.setText — which deliberately does NOT re-run evaluation.
func actSetText(text string) Action {
	return Action{Verb: "setText", Args: ActionArg{Text: strp(text)}}
}

func actClear() Action {
	return Action{Verb: "clear"}
}

// submitNow tells the client to submit. In the pure model the SERVER decided to
// submit; requireTail is the tail the server matched, and the client re-verifies
// it against the CURRENT local buffer before firing (the irreversibility guard).
// text (when set) is the stripped remainder to send.
func actSubmitNow(text, requireTail string) Action {
	return Action{Verb: "submitNow", Args: ActionArg{Text: strp(text), RequireTail: requireTail}}
}

// armTimer is advisory in the pure model — it tells the client to show a
// countdown affordance. The AUTHORITATIVE countdown runs server-side. The client
// timer never submits on its own; it only renders "submitting in 1s…".
func actArmTimer(timerID string, fireInMs int) Action {
	return Action{Verb: "armTimer", Args: ActionArg{TimerID: timerID, FireInMs: fireInMs}}
}

func actCancelTimer(timerID string) Action {
	return Action{Verb: "cancelTimer", Args: ActionArg{TimerID: timerID}}
}

func actShowHint(id, text, kind string) Action {
	return Action{Verb: "showHint", Args: ActionArg{HintID: id, Text: strp(text), HintKind: kind}}
}

func actClearHint(id string) Action {
	return Action{Verb: "clearHint", Args: ActionArg{HintID: id}}
}

func actNoop(reason string) Action {
	return Action{Verb: "noop", Args: ActionArg{Reason: reason}}
}

// Tab / navigation / speech verbs — the stateless command effects.
func actSwitchTab(channel string) Action  { return Action{Verb: "switchTab", Args: ActionArg{Channel: channel}} }
func actArchiveTab(channel string) Action { return Action{Verb: "archiveTab", Args: ActionArg{Channel: channel}} }
func actNextTab() Action                  { return Action{Verb: "nextTab"} }
func actPrevTab() Action                  { return Action{Verb: "prevTab"} }
func actNavigate(url string) Action       { return Action{Verb: "navigate", Args: ActionArg{URL: url}} }
func actStopSpeech() Action               { return Action{Verb: "stopSpeech"} }
func actFlagSpeech() Action               { return Action{Verb: "flagSpeech"} }
