package main

import (
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
type Tab struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
}

// streamState is the per-input-box server-side state: the armed submit timer and
// the seq counter. This is the distributed input state brain-v4vje §3 warns about.
type streamState struct {
	mu          sync.Mutex
	seq         int64
	lastVersion int64

	// Server-owned submit countdown.
	submitTimer   *time.Timer
	submitTail    string // the tail we matched when we armed
	submitBaseVer int64  // the version we armed against
	timerGen      int64  // generation guard: a fired timer whose gen != current is stale
}

// Engine holds all live streams and the compiled command set.
type Engine struct {
	mu       sync.Mutex
	streams  map[string]*streamState
	specs    []commandSpec
	matchers map[string][]CompiledMatcher // command id → compiled matchers (built once)

	// onSubmit is called when a SERVER-OWNED timer fires and decides to submit.
	// The service layer wires this to push a submitNow over SSE (engine has no
	// network of its own). base is the version armed against; tail is what to
	// re-verify; text is the stripped remainder.
	onSubmit func(streamID string, seq int64, base int64, tail, text string)

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

// NewEngine builds the engine, compiling every command's matchers once (the
// compiled-matcher cache from registry.ts:73-82, but eager since phrases are
// static here — settings-driven rebinding is a documented future extension).
func NewEngine() *Engine {
	e := &Engine{
		streams:  map[string]*streamState{},
		specs:    append([]commandSpec(nil), builtins...),
		matchers: map[string][]CompiledMatcher{},
	}
	// Sort by priority ascending — lower wins, first match ends the pass
	// (registry.ts:21).
	sort.SliceStable(e.specs, func(i, j int) bool { return e.specs[i].priority < e.specs[j].priority })
	for _, spec := range e.specs {
		e.matchers[spec.id] = compilePhrases(spec.phrases, spec.mode)
	}
	return e
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
	st.mu.Unlock()

	out := &actionList{}

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

// runPass mirrors registry.ts:88-113: iterate commands by priority; first match
// ends matching, but the submit machine's self-cancel (watch) runs every pass.
func (e *Engine) runPass(req EvalRequest, out *actionList) string {
	value := req.Text
	fired := ""

	for _, spec := range e.specs {
		matched := false
		if fired == "" {
			for _, cm := range e.matchers[spec.id] {
				m := cm.match(value)
				if m == nil {
					continue
				}
				if spec.id == "submit" {
					// Stateful: arm the SERVER-OWNED countdown instead of a direct
					// action. matched=true so the self-cancel below won't disarm it.
					e.armSubmit(req, m, out)
					matched = true
					fired = spec.id
					break
				}
				handled := runAction(spec, m, req.Tabs, out)
				if !handled {
					continue // not handled — try next phrase / later command
				}
				matched = true
				fired = spec.id
				break
			}
		}
		// watch(): the submit machine self-cancels the moment the buffer no longer
		// ends with the armed trigger (builtins.ts:34-36).
		if spec.id == "submit" && !matched {
			e.cancelSubmit(req.StreamID, out, "tail-changed")
		}
	}
	return fired
}

// armSubmit starts (or re-arms) the SERVER-OWNED 1000ms submit timer. This is the
// crux of the pure model — the countdown that in the client build is a local
// setTimeout now runs on the server, one network hop away from the live buffer.
func (e *Engine) armSubmit(req EvalRequest, m *matchResult, out *actionList) {
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

	st.submitTimer = time.AfterFunc(time.Duration(submitDelayMs)*time.Millisecond, func() {
		e.fireSubmit(streamID, gen)
	})
	st.mu.Unlock()

	e.stats.mu.Lock()
	e.stats.SubmitsArmed++
	e.stats.mu.Unlock()

	// Advisory armTimer so the client can render a "submitting in 1s…" countdown.
	// The AUTHORITATIVE timer is the server one above; the client timer never
	// submits on its own.
	out.add(actArmTimer("submit", submitDelayMs))
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
		e.onSubmit(streamID, e.nextSeq(streamID), base, tail, "")
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
	}
}

// describeCommands returns the registered command table for /commands (debug).
func (e *Engine) describeCommands() []map[string]any {
	rows := make([]map[string]any, 0, len(e.specs))
	for _, s := range e.specs {
		rows = append(rows, map[string]any{
			"id": s.id, "priority": s.priority, "mode": string(s.mode),
			"phrases": s.phrases, "description": s.description,
		})
	}
	return rows
}

// normalize trims a buffer for logging without altering evaluation semantics.
func normalize(s string) string { return strings.TrimSpace(s) }
