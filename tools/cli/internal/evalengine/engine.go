package evalengine

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"sync"
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

const submitDelayMs = 1000 // mirrors builtins.ts:24 setTimeout(…, 1000)

// Tab is one agent tab the client reports so {agent}/{page} resolution can run
// server-side (the engine has no DOM; the client sends its live tab set).
// Nicknames are optional spoken aliases (CHANNEL_PICKER_CONTRACT §EvalRequest);
// they participate in resolveAgent + resolveChannelSelection matching. The array
// may be nil/empty and the backend must tolerate that.
type Tab struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Nicknames []string `json:"nicknames"`
}

// EvalRequest is the body of POST /eval — the versioned buffer snapshot.
type EvalRequest struct {
	StreamID     string    `json:"streamId"`
	Version      int64     `json:"version"`      // client-owned monotonic input version
	Text         string    `json:"text"`         // full buffer (already voice-settled by the client)
	Cursor       CursorPos `json:"cursor"`       // selection at snapshot time
	Reason       string    `json:"reason"`       // "input" | "blur" | "resync" | "timer-fire"
	VoiceEnabled bool      `json:"voiceEnabled"` // master gate (input.ts:100 / registry.ts:91)
	Tabs         []Tab     `json:"tabs"`         // live agent tabs for {agent} resolution
	Mode         string    `json:"mode"`         // "" = normal eval; "channel-select" = resolve text as a channel pick
	Platform     string    `json:"platform"`     // "" = default (parlay); which surface this buffer belongs to

	// Commands is an OPTIONAL per-request command manifest. When present and valid
	// it wholly replaces the engine's command set FOR THIS REQUEST ONLY (contract
	// §Loading precedence: request > file > embedded, highest wins). This is how a
	// client ships user-customized phrases (today's voiceSubmitPhrases generalize to
	// it) with no server state. It is a raw message so an invalid override is simply
	// ignored — the request still evaluates against the live file/embedded set —
	// rather than failing the whole request at JSON-decode time.
	Commands json.RawMessage `json:"commands,omitempty"`
}

type CursorPos struct {
	Anchor int `json:"anchor"`
	Active int `json:"active"`
}

// EvalResponse is what /eval returns synchronously. The actions ALSO carry the
// envelope so the TS relay can forward them verbatim over SSE. engineEvalMs is
// the pure compiled-evaluation time (the number the captain wants to compare
// against network RTT).
type EvalResponse struct {
	StreamID     string   `json:"streamId"`
	Actions      []Action `json:"actions"`
	BaseVersion  int64    `json:"baseVersion"`
	Seq          int64    `json:"seq"`
	ProtocolV    int      `json:"v"`
	EngineEvalNs int64    `json:"engineEvalNs"` // compiled eval time, nanoseconds
	Fired        string   `json:"fired"`        // command id that fired, or "" (debug/observe)
	Platform     string   `json:"platform"`     // the surface these actions target (echoes the request)
}

// streamState is the per-input-box server-side state: the armed submit timer and
// the seq counter. This is the distributed input state brain-v4vje §3 warns about.
type streamState struct {
	mu          sync.Mutex
	seq         int64
	lastVersion int64
	// platform is the surface this stream belongs to, recorded from the request so
	// an ASYNC server-owned action (a submit fire) knows which surface to update —
	// the sync path already returns to its caller, but a fire has no caller to
	// return to. Defaults to parlay until a request names otherwise.
	platform string

	// Server-owned submit countdown.
	submitTimer   *time.Timer
	submitTail    string // the tail we matched when we armed
	submitBaseVer int64  // the version we armed against
	timerGen      int64  // generation guard: a fired timer whose gen != current is stale
}

// compiledCommand pairs a manifest command (DATA) with its compiled matchers
// (MACHINERY). The engine's live command set is a priority-sorted slice of these,
// rebuilt whenever a new manifest is loaded (hot-reload) — the matchers are
// compiled once per load, not per eval.
type compiledCommand struct {
	cmd      CommandManifest
	matchers []CompiledMatcher
}

// Engine holds all live streams and the compiled command set.
type Engine struct {
	mu      sync.Mutex
	streams map[string]*streamState
	// commands is the live, priority-sorted, enabled-only command set the pass
	// iterates. It comes from a Manifest (embedded default today; a loaded file or
	// per-request override later), NEVER from a hardcoded switch.
	commands []compiledCommand

	// onSubmit carries the stream's platform so the fire lands on the right surface.
	// onSubmit is called when a SERVER-OWNED timer fires and decides to submit.
	// The service layer wires this to push a submitNow over SSE (engine has no
	// network of its own). base is the version armed against; tail is what to
	// re-verify; text is the stripped remainder.
	onSubmit func(streamID string, seq int64, base int64, tail, text, platform string)

	// Observability counters (exposed at /stats).
	stats Stats
}

// Stats is the observable cost surface — the whole point of the instrumentation.
type Stats struct {
	mu               sync.Mutex
	Evals            int64 `json:"evals"`
	SubmitsArmed     int64 `json:"submitsArmed"`
	SubmitsFired     int64 `json:"submitsFired"`
	SubmitsCancelled int64 `json:"submitsCancelled"`
	StaleTimerFires  int64 `json:"staleTimerFires"` // timer fired but generation moved on
	TotalEvalNs      int64 `json:"totalEvalNs"`
	MaxEvalNs        int64 `json:"maxEvalNs"`
}

func (s *Stats) recordEval(ns int64) {
	s.mu.Lock()
	s.Evals++
	s.TotalEvalNs += ns
	if ns > s.MaxEvalNs {
		s.MaxEvalNs = ns
	}
	s.mu.Unlock()
}

func (s *Stats) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	avg := int64(0)
	if s.Evals > 0 {
		avg = s.TotalEvalNs / s.Evals
	}
	return map[string]any{
		"evals":            s.Evals,
		"submitsArmed":     s.SubmitsArmed,
		"submitsFired":     s.SubmitsFired,
		"submitsCancelled": s.SubmitsCancelled,
		"staleTimerFires":  s.StaleTimerFires,
		"avgEvalNs":        avg,
		"maxEvalNs":        s.MaxEvalNs,
	}
}

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

// compileManifest turns a validated Manifest into the engine's live command set:
// drop disabled commands, sort by priority ascending (lower wins, first match ends
// the pass — registry.ts:21), and compile each command's phrases once. The
// manifest is pre-validated, so every phrase is known to compile.
func compileManifest(man *Manifest) []compiledCommand {
	cmds := make([]compiledCommand, 0, len(man.Commands))
	for _, c := range man.Commands {
		if !c.isEnabled() {
			continue
		}
		cmds = append(cmds, compiledCommand{
			cmd:      c,
			matchers: compilePhrases(c.Phrases, MatchMode(c.Mode)),
		})
	}
	sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].cmd.Priority < cmds[j].cmd.Priority })
	return cmds
}

// SetCommands atomically swaps the engine's live command set from a validated
// manifest (hot-reload). Compilation happens before the lock so the swap itself is
// a single pointer assignment — an in-flight Eval either sees the whole old set or
// the whole new set, never a torn mix. Callers must pass only a validated Manifest;
// an empty set is impossible because validateManifest rejects zero commands (never
// fall open to no commands).
func (e *Engine) SetCommands(man *Manifest) {
	cmds := compileManifest(man)
	e.mu.Lock()
	e.commands = cmds
	e.mu.Unlock()
}

func (e *Engine) stream(id string) *streamState {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.streams[id]
	if !ok {
		st = &streamState{}
		e.streams[id] = st
	}
	return st
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

// commandSet resolves which command set a request evaluates against, implementing
// the request > file > embedded precedence. A per-request Commands override is
// compiled and used ONLY if it parses+validates; an invalid override is ignored
// (fail-closed to the live set), never a 400 — the request still evaluates. The
// override is not cached: it is opt-in per request, so the common no-override hot
// path pays nothing and only override-carrying requests pay the compile.
func (e *Engine) commandSet(req EvalRequest) []compiledCommand {
	if raw := bytes.TrimSpace(req.Commands); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if man, err := parseManifest(raw); err == nil {
			return compileManifest(man)
		}
		// Invalid override: fall through to the live file/embedded set.
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.commands
}

// isSubmitHandler reports whether an emit delegates to the stateful submit handler.
func isSubmitHandler(emit Emit) bool {
	return emit.Kind == "handler" && emit.Handler == "submit"
}

// submitDelay reads the submit handler's countdown from its config, defaulting to
// submitDelayMs when unset. Validation already rejected a negative delayMs.
func submitDelay(emit Emit) int {
	if len(emit.Config) == 0 {
		return submitDelayMs
	}
	var sc submitConfig
	if err := json.Unmarshal(emit.Config, &sc); err == nil && sc.DelayMs > 0 {
		return sc.DelayMs
	}
	return submitDelayMs
}

// armSubmit starts (or re-arms) the SERVER-OWNED submit timer (delayMs from the
// handler's config, 1000ms by default). This is the crux of the pure model — the
// countdown that in the client build is a local setTimeout now runs on the server,
// one network hop away from the live buffer.
func (e *Engine) armSubmit(req EvalRequest, m *matchResult, out *actionList, delayMs int) {
	st := e.stream(req.StreamID)
	st.mu.Lock()
	// Re-arm: clear any prior timer (builtins.ts:22 clearTimeout(submitTimer)).
	if st.submitTimer != nil {
		st.submitTimer.Stop()
	}
	st.timerGen++
	gen := st.timerGen
	st.submitTail = m.matchedText
	st.submitBaseVer = req.Version
	streamID := req.StreamID

	st.submitTimer = time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() {
		e.fireSubmit(streamID, gen)
	})
	st.mu.Unlock()

	e.stats.mu.Lock()
	e.stats.SubmitsArmed++
	e.stats.mu.Unlock()

	// Advisory armTimer so the client can render a "submitting in 1s…" countdown.
	// The AUTHORITATIVE timer is the server one above; the client timer never
	// submits on its own.
	out.add(actArmTimer("submit", delayMs))
	out.add(actShowHint("submit-countdown", "auto-sending in 1s…", "info"))
}

// fireSubmit runs when the SERVER-OWNED timer elapses. It cannot see the live
// client buffer (that is the fundamental limitation of the pure model), so it
// re-verifies against the version it armed with and hands the client a submitNow
// carrying requireTail. The client does the FINAL re-verify against its truly
// current buffer before sending — the irreversibility guard.
func (e *Engine) fireSubmit(streamID string, gen int64) {
	st := e.stream(streamID)
	st.mu.Lock()
	// Generation guard: if a newer arm/cancel happened, this fire is stale.
	if gen != st.timerGen {
		st.mu.Unlock()
		e.stats.mu.Lock()
		e.stats.StaleTimerFires++
		e.stats.mu.Unlock()
		return
	}
	tail := st.submitTail
	base := st.submitBaseVer
	platform := st.platform // the surface this fire must land on
	st.submitTimer = nil
	st.timerGen++ // consume this generation
	seq := st.seq // the submitNow will get its own seq from pushSubmit
	_ = seq
	st.mu.Unlock()

	e.stats.mu.Lock()
	e.stats.SubmitsFired++
	e.stats.mu.Unlock()

	// The stripped text is computed by the CLIENT at apply time against its live
	// buffer (it knows the true current text); the server supplies the tail to
	// strip and re-verify. We pass text="" to mean "strip requireTail from your
	// current buffer and send the remainder" — see dispatcher.ts submitNow.
	if e.onSubmit != nil {
		// seq is assigned inside onSubmit via nextSeq to keep ordering correct.
		e.onSubmit(streamID, e.nextSeq(streamID), base, tail, "", platform)
	}
}

// cancelSubmit disarms the server-owned timer and tells the client to drop its
// advisory countdown. In the pure model this cancel must beat the fire across the
// network — the race brain-v4vje §3 design-A flagged. On a slow link the client's
// advisory armTimer may already have visually "fired" before this arrives.
func (e *Engine) cancelSubmit(streamID string, out *actionList, reason string) {
	st := e.stream(streamID)
	st.mu.Lock()
	had := st.submitTimer != nil
	if had {
		st.submitTimer.Stop()
		st.submitTimer = nil
		st.timerGen++ // invalidate any in-flight fire
	}
	st.mu.Unlock()
	if had {
		e.stats.mu.Lock()
		e.stats.SubmitsCancelled++
		e.stats.mu.Unlock()
		out.add(actCancelTimer("submit"))
		out.add(actClearHint("submit-countdown"))
	}
}

func (e *Engine) nextSeq(streamID string) int64 {
	st := e.stream(streamID)
	st.mu.Lock()
	st.seq++
	s := st.seq
	st.mu.Unlock()
	return s
}

// finish stamps the envelope (seq, baseVersion, protocol version) and records
// the eval-time stat. Every /eval response gets a fresh seq so the client's
// strict-ordering dispatcher can detect gaps.
func (e *Engine) finish(req EvalRequest, st *streamState, out *actionList, fired string, start time.Time) EvalResponse {
	ns := time.Since(start).Nanoseconds()
	e.stats.recordEval(ns)

	st.mu.Lock()
	st.seq++
	seq := st.seq
	st.mu.Unlock()

	return EvalResponse{
		StreamID:     req.StreamID,
		Actions:      out.items,
		BaseVersion:  req.Version,
		Seq:          seq,
		ProtocolV:    ProtocolVersion,
		EngineEvalNs: ns,
		Fired:        fired,
		Platform:     requestPlatform(req),
	}
}

// describeCommands returns the registered command table for /commands (debug).
func (e *Engine) describeCommands() []map[string]any {
	e.mu.Lock()
	cmds := e.commands
	e.mu.Unlock()
	rows := make([]map[string]any, 0, len(cmds))
	for _, cc := range cmds {
		s := cc.cmd
		rows = append(rows, map[string]any{
			"id": s.ID, "priority": s.Priority, "mode": s.Mode,
			"phrases": s.Phrases, "description": s.Description,
			"emit": s.Emit.Kind,
		})
	}
	return rows
}

// normalize trims a buffer for logging without altering evaluation semantics.
func normalize(s string) string { return strings.TrimSpace(s) }
