package evalengine

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

	// openChannelPicker
	Channels []PickerChannel `json:"channels,omitempty"`
	Prompt   string          `json:"prompt,omitempty"`

	// openSenderPicker
	Senders []PickerSender `json:"senders,omitempty"`

	// noop
	Reason string `json:"reason,omitempty"`
}

// PickerChannel is one entry in the authoritative ordered channel list the
// backend hands the frontend via openChannelPicker (CHANNEL_PICKER_CONTRACT
// §Actions). Index is 1-based — it is the number the user speaks. Label is the
// display name (first nickname if present, else name); Nickname is a secondary
// hint string that may be empty.
type PickerChannel struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Label    string `json:"label"`
	Nickname string `json:"nickname"`
}

// PickerSender is one entry in the authoritative ordered sender list the
// backend hands the frontend via openSenderPicker. Same structure as PickerChannel.
// Index is 1-based — it is the number the user speaks. Label is the display name
// (contact name or phone number); Nickname is a secondary hint (e.g., last msg preview).
type PickerSender struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`       // phone number or identifier
	Label    string `json:"label"`    // display name
	Nickname string `json:"nickname"` // hint (preview or last seen)
}

// actionList accumulates actions in emission order.
type actionList struct {
	items []Action
}

func (l *actionList) add(a Action) { l.items = append(l.items, a) }

func intp(v int) *int       { return &v }
func strp(v string) *string { return &v }

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
func actSwitchTab(channel string) Action {
	return Action{Verb: "switchTab", Args: ActionArg{Channel: channel}}
}
func actArchiveTab(channel string) Action {
	return Action{Verb: "archiveTab", Args: ActionArg{Channel: channel}}
}
func actNextTab() Action            { return Action{Verb: "nextTab"} }
func actPrevTab() Action            { return Action{Verb: "prevTab"} }
func actNavigate(url string) Action { return Action{Verb: "navigate", Args: ActionArg{URL: url}} }
func actStopSpeech() Action         { return Action{Verb: "stopSpeech"} }
func actFlagSpeech() Action         { return Action{Verb: "flagSpeech"} }

// ── Channel picker verbs (CHANNEL_PICKER_CONTRACT §Actions) ────────────────────

// actOpenChannelPicker hands the frontend the authoritative ordered channel list
// plus the instruction prompt. The frontend renders its full-screen modal from
// exactly this data — the numbering (1-based index) is the number the user speaks.
func actOpenChannelPicker(prompt string, channels []PickerChannel) Action {
	return Action{Verb: "openChannelPicker", Args: ActionArg{Prompt: prompt, Channels: channels}}
}

// actCloseChannelPicker dismisses the modal (a hit's switch or an explicit cancel).
func actCloseChannelPicker() Action {
	return Action{Verb: "closeChannelPicker"}
}

// actPickerHint keeps the modal open and shows a transient hint after a no-match.
// It reuses the existing Text *string arg field per the contract.
func actPickerHint(text string) Action {
	return Action{Verb: "pickerHint", Args: ActionArg{Text: strp(text)}}
}

// ── Sender picker verbs (mirror of channel picker for iMessage senders) ────────

// actOpenSenderPicker hands the frontend the authoritative ordered sender list
// plus the instruction prompt. The frontend renders a full-screen modal.
func actOpenSenderPicker(prompt string, senders []PickerSender) Action {
	return Action{Verb: "openSenderPicker", Args: ActionArg{Prompt: prompt, Senders: senders}}
}

// actCloseSenderPicker dismisses the sender picker modal.
func actCloseSenderPicker() Action {
	return Action{Verb: "closeSenderPicker"}
}

// actSenderPickerHint keeps the modal open and shows a transient hint after a no-match.
func actSenderPickerHint(text string) Action {
	return Action{Verb: "senderPickerHint", Args: ActionArg{Text: strp(text)}}
}
