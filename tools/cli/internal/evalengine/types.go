package evalengine

import (
	"encoding/json"
	"sync"
	"time"
)

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
