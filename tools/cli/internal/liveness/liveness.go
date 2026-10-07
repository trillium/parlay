// Package liveness classifies ONE agent's liveness from the records an
// operator surface can actually read — and keeps every distinction the fleet
// already makes instead of collapsing them into a boolean "up".
//
// The problem it exists to fix: "is this agent alive?" has four independent
// answers in this fleet and only one of them is ever measured.
//
//   - registration is a row on the server that nothing removes when a listener
//     dies, so a registered agent can be a ghost that accepts a message and
//     silently drops it (robots-jkwc: 148 registered against 11 real
//     listeners).
//   - a process-table probe answers whether something on THIS host is reading
//     the channel, and it can fail, in which case "no listener" carries no
//     information at all — so a failed probe must never be reported as a ghost.
//   - the server's presence row carries a per-channel lastSeen, and it has
//     three distinct shapes that mean different things: a stamp (the agent was
//     heard at a time), a row with NO stamp (registered, never observed), and
//     no row at all (the server has never recorded this channel). Only the
//     first can expire; the other two are ABSENT, and calling an absent
//     heartbeat "stale" is exactly the lie this package refuses.
//   - activity also exists in records the server knows nothing about: the
//     agent's own status file and the relay's delivery trail. An agent that is
//     working but not talking has an old channel stamp and a fresh status
//     file, so silence measured from the channel alone would raise a false
//     alarm on a healthy agent.
//
// So Classify takes every dated observation and answers three questions
// separately: what state the fleet is in, whether the heartbeat is absent or
// merely expired, and how long the agent has actually been silent — with
// "unknown" and the reason whenever a question cannot be answered. Nothing
// here is a default: a missing record never becomes a healthy value.
//
// Pure and injectable by construction (no clock, no files, no network), which
// is what lets the classification be tested exhaustively without a fleet.
package liveness

import (
	"fmt"
	"strings"
	"time"
)

// State is how the fleet's own records classify the agent.
const (
	// StateLive: registered, and a listener for this agent is running here.
	StateLive = "live"
	// StateGhost: registered, and nothing is listening. A message sent to this
	// channel is spooled for a reader that does not exist.
	StateGhost = "ghost"
	// StateOffline: not in the registry at all.
	StateOffline = "offline"
	// StateUnknown: the registry could not be read, so registration — the
	// premise of the other three — is unknown.
	StateUnknown = "unknown"
)

// Heartbeat is the server's own per-channel activity record. Fresh and expired
// are two ages of the SAME thing (a stamp exists); never-observed and no-row
// are the two shapes of ABSENCE, which is not staleness.
const (
	HeartbeatFresh         = "fresh"
	HeartbeatExpired       = "expired"
	HeartbeatNeverObserved = "never-observed"
	HeartbeatNoRow         = "no-row"
	HeartbeatUnknown       = "unknown"
)

// Silence is how long the agent has actually been quiet, measured over every
// dated record rather than the channel alone.
const (
	SilenceFresh   = "fresh"
	SilenceExpired = "expired"
	// SilenceUnknown: no dated activity record exists, so silence is
	// unmeasurable. Deliberately NOT "never": an empty record is not evidence
	// that nothing happened.
	SilenceUnknown = "unknown"
)

// Activity source names, a closed vocabulary a --json consumer can branch on.
const (
	SourceChannel  = "channel"
	SourceStatus   = "status"
	SourceDelivery = "delivery"
)

// Activity is one dated record of the agent doing something. Source is from
// the vocabulary above; Detail is the prose an operator reads.
type Activity struct {
	Source string
	Detail string
	At     time.Time
}

// Observation is everything the classifier is allowed to know about one agent.
// Every *Known flag means "the record was read and answered" — a failed read
// leaves its flag false so the verdict can say unknown instead of guessing.
type Observation struct {
	// --- the server's registry snapshot ---
	RegistryKnown bool
	Registered    bool

	// --- this host's process table ---
	ListenersKnown bool
	HasListener    bool

	// --- the server's presence row for this channel ---
	PresenceKnown  bool
	HasPresenceRow bool
	// LastSeen is the row's stamp verbatim. Empty (or blank) is the server's
	// own "never observed" shape and must not be parsed as a time.
	LastSeen string

	// --- the relay's own enrollment list ---
	RelayKnown    bool
	RelayEnrolled bool

	// Activities are every dated activity record, in any order. Empty means no
	// dated record exists — not that nothing happened.
	Activities []Activity

	// Looked names the record stores the gatherer consulted, in the operator's
	// words, so an unmeasurable silence can say what it looked at rather than
	// asserting emptiness.
	Looked []string

	// Now and Window are the clock and the "silent beyond" threshold. A zero
	// Now means time.Now(); a zero Window means DefaultWindow.
	Now    time.Time
	Window time.Duration
}

// DefaultWindow is the silence threshold used when a caller supplies none.
// Ten minutes is deliberately long: this fleet's agents go quiet for whole
// tool-call turns, and a window tight enough to page on ordinary work is a
// window an operator learns to ignore.
const DefaultWindow = 10 * time.Minute

// Verdict is the classification. Every field is populated on every call: a
// field that disappears when a source is down is a field the reader will not
// notice is missing.
type Verdict struct {
	State     string
	StateNote string

	Heartbeat     string
	HeartbeatNote string
	// HeartbeatAt / HeartbeatFor are the parsed stamp and its age when the
	// heartbeat is fresh or expired. Kept apart from SilentFor on purpose: the
	// channel record's age and the agent's measured silence can come from
	// different records and must never be printed as each other.
	HeartbeatAt  time.Time
	HeartbeatFor time.Duration

	Silence     string
	SilentFor   time.Duration
	SilentSince time.Time
	SilenceNote string

	// Last is the newest dated activity of any source. LastKnown false with a
	// non-empty LastDetail means a record exists but this reader holds no
	// dated one.
	LastSource string
	LastDetail string
	LastAt     time.Time
	LastFor    time.Duration
	LastKnown  bool

	// RelayKnown/RelayEnrolled are the relay's answer when it gave one.
	RelayKnown    bool
	RelayEnrolled bool
}

// Classify turns one observation into a verdict. It never fails and never
// invents: a record that was not read produces "unknown" with the reason
// attached.
func Classify(o Observation) Verdict {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	window := o.Window
	if window <= 0 {
		window = DefaultWindow
	}
	v := Verdict{RelayKnown: o.RelayKnown, RelayEnrolled: o.RelayEnrolled}

	classifyState(&v, o)
	classifyHeartbeat(&v, o, now, window)
	classifySilence(&v, o, now, window)
	return v
}

// age is now - at, floored at zero: a stamp in the future is a clock skew
// artefact, not negative silence.
func age(now, at time.Time) time.Duration {
	d := now.Sub(at)
	if d < 0 {
		return 0
	}
	return d
}

// ParseStamp parses one server timestamp. RFC3339 and RFC3339Nano are both
// accepted because the server encodes with the standard library's time.Time
// marshaller, whose precision depends on the value.
func ParseStamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Short renders a duration for a one-line column: the coarsest unit that
// still says something useful. Deliberately not time.Duration.String()'s
// "2h14m3.5s" — an operator scanning a column wants 2h14m.
func Short(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
