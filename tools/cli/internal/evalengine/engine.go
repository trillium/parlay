package evalengine

import (
	"strings"
	"time"
)

// ── The evaluation engine + the SERVER-OWNED submit machine ────────────────────
//
// This is the compiled brain. It receives a versioned snapshot of one input box
// (from one device/stream) and returns the actions the client should apply.
//
// PURE server-side model: the trigger-word auto-submit countdown lives HERE, per
// stream, not in the browser. When a trailing trigger word appears, the engine
// arms a real 1000ms server-side timer. On fire, the engine emits a submitNow.
// Because the client keeps typing during that second, the fire is stale by one
// network round-trip — the client re-verifies the tail before actually sending.
// This is the exact fragility the captain wants to observe (brain-v4vje design A,
// which the design deliberately rejected for being racy).

// NewEngine builds the engine from the embedded default manifest, compiling every
// command's matchers once. The command set is DATA (default_commands.json), not a
// hardcoded slice; loadManifest swaps in a new set at runtime (hot-reload).
func NewEngine() *Engine {
	e := &Engine{
		streams: map[string]*streamState{},
	}
	e.commands = compileManifest(embeddedManifest())
	return e
}

// Eval runs one full evaluation pass over a versioned buffer snapshot and returns
// the actions. This is the compiled hot path; its wall time is measured and
// returned so the captain can compare it to network RTT.
func (e *Engine) Eval(req EvalRequest) EvalResponse {
	start := time.Now()
	st := e.stream(req.StreamID)

	st.mu.Lock()
	// Last-write-wins: drop a stale in-flight request whose version is older than
	// the newest we've seen for this stream (brain-v4vje §2 coalescing). We still
	// return a noop so the client's seq accounting stays intact.
	if req.Version < st.lastVersion {
		st.mu.Unlock()
		out := &actionList{}
		out.add(actNoop("stale-request-version"))
		return e.finish(req, st, out, "", start)
	}
	st.lastVersion = req.Version
	// Record which surface this stream is on, so a later async submit fire on this
	// stream knows where to land (the sync response already returns to its caller).
	st.platform = requestPlatform(req)
	st.mu.Unlock()

	out := &actionList{}

	// CHANNEL-SELECT mode (CHANNEL_PICKER_CONTRACT §Resolution). The picker input
	// is a distinct surface with its own streamId; it BYPASSES normal command
	// matching and the submit machine entirely, running deterministic channel
	// resolution instead.
	if req.Mode == "channel-select" {
		e.resolveChannelPick(req, out)
		return e.finish(req, st, out, "channel-select", start)
	}

	// SENDER-SELECT mode (mirror of channel-select for iMessage senders).
	// Resolves spoken text to a sender (contact) ID or cancel word.
	if req.Mode == "sender-select" {
		e.resolveSenderPick(req, out)
		return e.finish(req, st, out, "sender-select", start)
	}

	// CHECK A/B — voice-enabled master gate (input.ts:100, registry.ts:91). With
	// voice off, NO command matching runs; typed text only submits via the
	// client's Enter/button path (untouched by this build).
	if !req.VoiceEnabled {
		// A change with voice off cancels any armed submit and does nothing else.
		e.cancelSubmit(req.StreamID, out, "voice-disabled")
		out.add(actNoop("voice-disabled"))
		return e.finish(req, st, out, "", start)
	}

	fired := e.runPass(req, out)
	return e.finish(req, st, out, fired, start)
}

// resolveChannelPick runs the deterministic channel-select resolution and emits
// the contract's actions (CHANNEL_PICKER_CONTRACT §Resolution / The Loop step 5):
//   - cancel word  → closeChannelPicker (+ clear).
//   - hit          → switchTab + closeChannelPicker (+ clear).
//   - no match     → pickerHint (modal stays open; NO close).
func (e *Engine) resolveChannelPick(req EvalRequest, out *actionList) {
	id, cancel, ok := resolveChannelSelection(req.Text, req.Tabs)
	switch {
	case cancel:
		out.add(actCloseChannelPicker())
		out.add(actClear())
	case ok:
		out.add(actSwitchTab(id))
		out.add(actCloseChannelPicker())
		out.add(actClear())
	default:
		out.add(actPickerHint(`No channel matched "` + strings.TrimSpace(req.Text) + `" — try again`))
	}
}

// resolveSenderPick runs sender-select resolution (mirror of resolveChannelPick).
// Actions emitted (same pattern as channel picker):
//   - cancel word  → closeSenderPicker (+ clear).
//   - hit          → store sender ID, close picker (TODO: TBD state management).
//   - no match     → senderPickerHint (modal stays open; NO close).
func (e *Engine) resolveSenderPick(req EvalRequest, out *actionList) {
	// For now, get a fixed list of N senders. In production, this might be
	// parameterized via the request (which sender list to show, etc.).
	senders := getRecentSenders(5)

	senderID, cancel, ok := resolveSenderSelection(req.Text, senders)
	switch {
	case cancel:
		out.add(actCloseSenderPicker())
		out.add(actClear())
	case ok:
		// TODO: emit an action to transition to compose mode for senderID.
		// For now, just close the picker. Client will handle state (placeholder).
		_ = senderID // TODO: store and use for compose state
		out.add(actCloseSenderPicker())
		out.add(actClear())
	default:
		out.add(actSenderPickerHint(`No contact matched "` + strings.TrimSpace(req.Text) + `" — try again`))
	}
}

// runPass mirrors registry.ts:88-113: iterate commands by priority; first match
// ends matching, but the submit machine's self-cancel (watch) runs every pass.
// Every command's behavior now comes from its manifest emit — a declarative
// `sequence` interpreted by interpretSequence, or a `handler` delegation (submit
// arms the server-owned countdown). The old runAction switch is gone.
func (e *Engine) runPass(req EvalRequest, out *actionList) string {
	value := req.Text
	fired := ""

	// Resolve the command set for this pass: a valid per-request override wins,
	// otherwise the live file/embedded set (snapshotted so a concurrent hot-reload
	// swap is race-free).
	cmds := e.commandSet(req)
	platform := requestPlatform(req)

	for _, cc := range cmds {
		// Platform scoping: skip commands not eligible for this request's surface, so
		// a Herdr buffer never matches Parlay-only commands and vice versa.
		if !platformEligible(&cc.cmd, platform) {
			continue
		}
		matched := false
		submitHandler := isSubmitHandler(cc.cmd.Emit)
		if fired == "" {
			for _, cm := range cc.matchers {
				m := cm.match(value)
				if m == nil {
					continue
				}
				if submitHandler {
					// Stateful handler: arm the SERVER-OWNED countdown instead of a
					// direct action. matched=true so the self-cancel below won't disarm it.
					e.armSubmit(req, m, out, submitDelay(cc.cmd.Emit))
					matched = true
					fired = cc.cmd.ID
					break
				}
				handled := e.interpretSequence(&cc.cmd.Emit, m, MatchMode(cc.cmd.Mode), req.Tabs, out)
				if !handled {
					continue // not handled (onResolveFail:fallthrough) — try next phrase / later command
				}
				matched = true
				fired = cc.cmd.ID
				break
			}
		}
		// watch(): the submit machine self-cancels the moment the buffer no longer
		// ends with the armed trigger (builtins.ts:34-36).
		if submitHandler && !matched {
			e.cancelSubmit(req.StreamID, out, "tail-changed")
		}
	}
	return fired
}

// normalize trims a buffer for logging without altering evaluation semantics.
func normalize(s string) string { return strings.TrimSpace(s) }
