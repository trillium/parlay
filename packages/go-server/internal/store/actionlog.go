package store

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// ActionLog is the command log for INPUT EVALUATION: one record per string that
// was evaluated, whether it came from the sandbox test site or from the live
// panel. It sits next to CommandRegistry (which logs process invocations — one
// record per `parlay <verb>` process) and answers a different question: not
// "what is parlay running", but "what did these strings resolve to, and did the
// result reach anyone".
//
// # What is recorded, and what is never recorded
//
// The same discipline as CommandRegistry applies, for the same reason: this
// endpoint is unauthenticated and its writers are caller-supplied. A record
// holds identifiers, verb names, an outcome token, and timings. It never holds
// the evaluated TEXT — that is a message body, and no new store may keep one
// (see the sandbox task's hard constraint). `InputAction` is the engine's own
// `fired` command id, not a phrase the user typed.
//
// # The outcome vocabulary
//
// Four values, and the whole point of the log is that they are distinguishable:
//
//	delivered — evaluated, and the resulting input_action reached >=1 live SSE
//	            client for the owning device.
//	queued    — evaluated and accepted for LATER delivery, not yet handed to
//	            anyone: the engine armed its server-owned submit timer, or a
//	            remote-input submission was accepted for injection.
//	dropped   — evaluated and produced a result, but nothing received it: zero
//	            live SSE clients, an engine that could not be reached, or a
//	            failure on the far side of an accepted submission.
//	refused   — never evaluated at all, because a gate said no: the off switch
//	            (a muted connection or a muted action), or a delivery refused
//	            after the fact. A refusal is an act, not an absence.
//
// A filter that collapsed these four would be decoration; the vocabulary is the
// feature.
type ActionLog struct {
	mu      sync.RWMutex
	records []ActionRecord // oldest first, trimmed from the front
	counter uint64
	now     func() time.Time
	max     int
}

// The log's outcome vocabulary. These are wire values — the CLI verb, the panel
// and the sandbox page all filter on the literal strings.
const (
	OutcomeDelivered = "delivered"
	OutcomeQueued    = "queued"
	OutcomeDropped   = "dropped"
	OutcomeRefused   = "refused"
)

// ActionOutcomes is the closed vocabulary in display order, so every surface
// offers exactly the outcomes this log can produce rather than guessing.
var ActionOutcomes = []string{OutcomeDelivered, OutcomeQueued, OutcomeDropped, OutcomeRefused}

// Verb names are bounded because they are rendered into a terminal table and
// into HTML; ids and reasons get the same treatment. These are resource and
// rendering bounds on caller-supplied values, not redaction.
const (
	maxActionFieldLen = 64
	maxActionVerbs    = 16
	// DefaultActionLogMaxRecords bounds the ring. Deliberately larger than
	// CommandRegistry's 500: this log's records are a few dozen bytes of
	// identifiers each and arrive per keystroke, not per process, so 500 would
	// drop a busy session's history while the operator is still reading it.
	DefaultActionLogMaxRecords = 2000
)

// ActionRecord is one evaluated string, as stored and as serialized to every
// renderer. All strings are sanitized on arrival.
type ActionRecord struct {
	ID string `json:"id"`
	// At is when the evaluation completed, RFC3339Nano UTC.
	At string `json:"at"`
	// Source names the path that produced this record: "test-site" (the sandbox
	// page), "panel" (a live input box), "engine" (a server-owned fire), or
	// "remote-input". It is the "test-site vs production" axis.
	Source string `json:"source"`
	// Device is the connection the evaluation belonged to. Turning a connection
	// off is aimed at exactly this value.
	Device string `json:"device,omitempty"`
	// StreamID is the per-input-box stream. A preview's stream is the marker
	// that keeps its result from being delivered.
	StreamID string `json:"streamId,omitempty"`
	// InputAction is what the string resolved TO — the eval engine's `fired`
	// command id, or "" when nothing matched. This is the "input action" filter
	// axis. It is a command id, never the text that produced it.
	InputAction string `json:"inputAction,omitempty"`
	// OutputActions are the verb names the string would emit, in order. This is
	// the "output action" filter axis.
	OutputActions []string `json:"outputActions"`
	// Outcome is one of ActionOutcomes.
	Outcome string `json:"outcome"`
	// Reason is the short token explaining a non-delivered outcome, e.g.
	// "no-subscriber", "engine-unreachable", "off-connection", "off-action",
	// "preview-suppressed", "submit-armed". Never a message.
	Reason string `json:"reason,omitempty"`
	// RelayMs and EngineEvalNs are the two costs the sandbox page exists to
	// compare: transport round-trip vs compiled evaluation.
	RelayMs      int64 `json:"relayMs,omitempty"`
	EngineEvalNs int64 `json:"engineEvalNs,omitempty"`
}

// ActionLogFilter selects records. Every field is optional; a zero value means
// "do not filter on this". Matching is exact (case-insensitive) for the token
// fields and inclusive for the time window.
type ActionLogFilter struct {
	Source       string
	InputAction  string
	OutputAction string
	Outcome      string
	Reason       string
	Device       string
	Since        time.Time
	Until        time.Time
}

// ActionLogConfig overrides the log's bounds; a zero field takes the default.
type ActionLogConfig struct {
	Now        func() time.Time
	MaxRecords int
}

// NewActionLog builds a log. Exported like NewCommandRegistry because it holds
// no files and is constructed directly by tests and by Open alike.
func NewActionLog(cfg ActionLogConfig) *ActionLog {
	al := &ActionLog{now: cfg.Now, max: cfg.MaxRecords}
	if al.now == nil {
		al.now = time.Now
	}
	if al.max <= 0 {
		al.max = DefaultActionLogMaxRecords
	}
	return al
}

// Append sanitizes and stores one record, assigning its id and timestamp, and
// returns what was stored. Sanitization is repeated here rather than trusted
// from the handler because this route's callers are unauthenticated.
func (al *ActionLog) Append(in ActionRecord) ActionRecord {
	rec := ActionRecord{
		At:           al.now().UTC().Format(time.RFC3339Nano),
		Source:       sanitizeToken(in.Source, maxActionFieldLen),
		Device:       sanitizeToken(in.Device, maxActionFieldLen),
		StreamID:     sanitizeToken(in.StreamID, maxActionFieldLen),
		InputAction:  sanitizeToken(in.InputAction, maxActionFieldLen),
		Outcome:      sanitizeToken(in.Outcome, maxActionFieldLen),
		Reason:       sanitizeToken(in.Reason, maxActionFieldLen),
		RelayMs:      in.RelayMs,
		EngineEvalNs: in.EngineEvalNs,
	}
	rec.OutputActions = sanitizeVerbs(in.OutputActions)

	al.mu.Lock()
	defer al.mu.Unlock()
	al.counter++
	rec.ID = "act-" + itoaSmall(al.counter)
	al.records = append(al.records, rec)
	if len(al.records) > al.max {
		al.records = append([]ActionRecord(nil), al.records[len(al.records)-al.max:]...)
	}
	return rec
}

// sanitizeVerbs keeps verb names only, deduplicated, capped. An action verb is a
// lowerCamelCase identifier from the engine's closed registry, so a token that
// is not that shape is dropped WHOLE, never trimmed into shape — the same rule
// CommandRegistry's flag names follow, and for the same reason: a trimmed
// payload still carries its first characters, and it would arrive looking like a
// verb the engine could emit.
func sanitizeVerbs(in []string) []string {
	out := make([]string, 0, min(len(in), maxActionVerbs))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		if len(out) >= maxActionVerbs {
			break
		}
		v := strings.TrimSpace(raw)
		if v == "" || seen[v] || !actionVerbShape.MatchString(v) {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// actionVerbShape is what an emitted verb name must look like: a letter, then
// letters and digits. No dashes, no dots, no spaces, no `=` — a verb is an
// identifier the engine emits, not a caller-supplied token.
var actionVerbShape = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
