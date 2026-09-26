// Package remoteinput is the downstream control plane for Parlay's
// remote/voice input tool (task-57ltl, spec project-1ayr): it takes text
// Parlay has accepted and turns it into real keyboard input on the target
// Mac by piping it through Talon Voice and letting Talon perform the input.
//
// The pipeline per submission is strictly ordered:
//
//	focus first → inject only on focus success → report outcome
//
// On focus failure nothing is injected, the text is preserved, and the
// outcome carries StripTrigger so Parlay can remove the line-ender phrase
// that would otherwise auto-retry. While one injection is in flight,
// further submissions queue FIFO (no interleaving, no dropping, no
// busy-wait — the worker parks on a channel, never a poll loop).
//
// Talon is reached through its Python REPL (default
// ~/.talon/.venv/bin/repl, override TALON_REPL_PATH), verified live on
// 2026-09-25; see talon.go and docs/remote-input.md for the recorded
// contract. The adapter seam (TalonAdapter + FakeTalon) keeps the live
// REPL out of CI: tests prove behavior against the fake.
package remoteinput

// Statuses a submission moves through. Terminal states are Injected,
// FocusFailed, InjectFailed, and DryRunPassed; Queued and Injecting are
// transient. DryRunPassed is deliberately distinct from Injected so a
// reader that only understands "injected" never mistakes a dry run
// (nothing typed) for a real injection and clears state it should keep.
const (
	StatusQueued       = "queued"
	StatusInjecting    = "injecting"
	StatusInjected     = "injected"
	StatusFocusFailed  = "focus_failed"
	StatusInjectFailed = "inject_failed"
	StatusDryRunPassed = "dry_run_passed"
)

// FocusMode records what the focus gate did for a submission.
const (
	FocusVerified         = "verified"          // target focused and verified active
	FocusNotRequired      = "not_required"      // dry run with no target; injected as-is
	FocusAllowedUnfocused = "unfocused_allowed" // caller set allowUnfocused; injected without focus
)

// Submission is one accepted-input unit from Parlay. Text injects
// literally and multiline exactly as represented — no normalization,
// no large-paste optimization in MVP (project-1ayr).
type Submission struct {
	ID          string `json:"id"`
	Device      string `json:"device"`
	Text        string `json:"text"`
	App         string `json:"app,omitempty"`
	WindowTitle string `json:"windowTitle,omitempty"`
	Trigger     string `json:"trigger,omitempty"`
	// AllowUnfocused permits injection with no app/window target.
	// Without it a targetless live submission is refused (it would
	// otherwise type into whatever owns focus); dry runs never need it.
	AllowUnfocused bool `json:"allowUnfocused,omitempty"`
	// DryRun performs the real focus request plus real verification
	// and reports what would be inserted, typing nothing. It proves
	// the success leg without touching the live machine.
	DryRun bool `json:"dryRun,omitempty"`
}

// Outcome is the per-submission result reported back to Parlay. Parlay
// clears its shared input state only on StatusInjected; on
// StatusFocusFailed it preserves the text and strips Trigger.
type Outcome struct {
	ID              string `json:"id"`
	Device          string `json:"device"`
	Status          string `json:"status"`
	Focus           string `json:"focus,omitempty"`
	InjectAttempted bool   `json:"injectAttempted"`
	PreserveText    bool   `json:"preserveText,omitempty"`
	StripTrigger    bool   `json:"stripTrigger,omitempty"`
	Error           string `json:"error,omitempty"`
	// DryRun marks an outcome that typed nothing; WouldInsert carries
	// the exact bytes that would have been inserted.
	DryRun      bool   `json:"dryRun,omitempty"`
	WouldInsert string `json:"wouldInsert,omitempty"`
	// AllowUnfocused echoes the submission mode: true means this
	// outcome injected (or would inject, for dry runs) with no focus
	// target because the caller explicitly allowed it.
	AllowUnfocused bool `json:"allowUnfocused,omitempty"`
}

// Terminal reports whether no further transition is possible.
func (o Outcome) Terminal() bool {
	return o.Status == StatusInjected ||
		o.Status == StatusFocusFailed ||
		o.Status == StatusInjectFailed ||
		o.Status == StatusDryRunPassed
}

// SubmitRequest is the POST /api/chat/remote-input/submit wire shape.
type SubmitRequest struct {
	Device      string `json:"device"`
	Text        string `json:"text"`
	App         string `json:"app,omitempty"`
	WindowTitle string `json:"windowTitle,omitempty"`
	Trigger     string `json:"trigger,omitempty"`
	// AllowUnfocused opts into injection with no focus target.
	// Required (or ?allowUnfocused=1) for a targetless live submit;
	// dry runs never need it. Echoed back on the outcome.
	AllowUnfocused bool `json:"allowUnfocused,omitempty"`
	DryRun         bool `json:"dryRun,omitempty"`
}

// SubmitResponse is the 202 answer: the submission is queued, not done.
// The caller learns the terminal outcome via GET status or the
// remote_input_result SSE event (both carry Outcome).
type SubmitResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
