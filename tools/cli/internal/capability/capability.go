// Package capability is the interface-capability-declaration engine from
// issue #128 (§65–§74, §102, §105) and grill Q2d — the representation-plane
// contract by which a surface (web panel, terminal, voice, phone widget)
// declares what it can render, so the system never aims a state at a surface
// that cannot present it. Full model: docs/interface-capabilities.md.
//
// The engine is deliberately pure (the internal/routing pattern): it
// validates declarations, classifies event names, and decides delivery per
// (declaration, event) with a reasoned verdict. No I/O, no clock, no
// transport — the live SSE path applies these decisions at its broadcast
// choke points.
//
// Two invariants:
//
//  1. Subtraction only. A declaration can only narrow what its own
//     connection receives. Decide never returns suppress for an undeclared
//     client, never consults anything but the one declaration handed to it,
//     and grants no send/aim authority — which is why declaring requires no
//     new guarded route.
//  2. Fail loud. An invalid declaration is an error, never a silent
//     fallback to legacy delivery: fail-open would widen what a narrowing
//     surface receives, the exact wrong failure mode (the
//     routing.Policy.Validate posture).
package capability

import (
	"encoding/json"
	"sort"
)

// Class buckets every SSE event name for the delivery gate
// (docs/interface-capabilities.md "The delivery gate").
type Class string

const (
	// ClassLifecycle: connection bootstrap frames. Never gated — a surface
	// that cannot receive `connected` cannot negotiate at all.
	ClassLifecycle Class = "lifecycle"
	// ClassStateReport: the server reporting its own persisted or derived
	// state (message, history, agents, presence_map, …). Never gated in v1:
	// rendering a report it does not care about is a surface's own no-op.
	ClassStateReport Class = "state-report"
	// ClassPresentationCommand: the server aiming an action at a surface.
	// Gated: delivered to a declared surface only if it accepts the name.
	ClassPresentationCommand Class = "presentation-command"
)

// presentationCommands is the gated class — exactly the five bespoke
// panel-aiming names grill Q2d generalizes (07_ARCHITECTURE-GRILL.md).
// Admitting a new name (tts_event and plugin RPC names are the known
// candidates) is a minor schema bump plus an entry here, deliberately not
// done ahead of a declaring consumer (gap-fill 1 in the doc).
var presentationCommands = map[string]bool{
	"navigate":     true,
	"reload":       true,
	"device_cmd":   true,
	"input_action": true,
	"draft":        true,
}

// Classify buckets one SSE event name. Unknown names are state reports by
// construction: only a name listed in the gated class can ever be withheld,
// so a new event is deliverable-to-everyone until deliberately admitted.
func Classify(event string) Class {
	switch {
	case event == "connected":
		return ClassLifecycle
	case presentationCommands[event]:
		return ClassPresentationCommand
	default:
		return ClassStateReport
	}
}

// PresentationCommands returns the gated names, sorted — the roster the
// connected-echo reports as enforceable (Recognize).
func PresentationCommands() []string {
	names := make([]string, 0, len(presentationCommands))
	for n := range presentationCommands {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Surface is the declaring surface's identity — the vocabulary seam shared
// with the source-contracts work (docs/interface-capabilities.md "Scope
// disambiguation"): kind names the surface family, instance the concrete
// endpoint (the panel passes its ?device= uuid).
type Surface struct {
	Kind     string `json:"kind"`
	Instance string `json:"instance,omitempty"`
}

// Declaration is one surface's capability declaration, exchanged once at
// connect and immutable for the connection's lifetime (LSP posture).
// Unknown top-level JSON fields are ignored on parse — additive minor-bump
// fields must not break an older server.
type Declaration struct {
	// Schema: SemVer of the declaration contract itself
	// (docs/interface-capabilities.md "Versioning").
	Schema  string  `json:"schema"`
	Surface Surface `json:"surface"`
	// Accepts: presentation-command names this surface executes, each with
	// an open detail object (LSP-style nesting) reserved for per-command
	// granularity. The only axis enforced in v1.
	Accepts map[string]json.RawMessage `json:"accepts,omitempty"`
	// Content: content types the surface can present. Advisory in v1 —
	// validated, registered, exposed; not yet gating.
	Content []string `json:"content,omitempty"`
	// Interactions: affordances the surface can hand back. Advisory in v1;
	// load-bearing once interaction-state routing (#128 §64–§66) lands.
	Interactions []string `json:"interactions,omitempty"`
}

// AcceptNames returns the declared accepts, sorted.
func (d *Declaration) AcceptNames() []string {
	names := make([]string, 0, len(d.Accepts))
	for n := range d.Accepts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Recognize splits a declaration's accepts into the names this engine
// actually enforces (members of the gated class) and the
// preserved-but-inert rest, both sorted — the payload of the
// connected-echo, so a surface can tell when a capability it cares about
// is not enforced by the server it reached.
func (d *Declaration) Recognize() (recognized, unknown []string) {
	recognized, unknown = []string{}, []string{}
	for _, n := range d.AcceptNames() {
		if presentationCommands[n] {
			recognized = append(recognized, n)
		} else {
			unknown = append(unknown, n)
		}
	}
	return recognized, unknown
}

// Verdict is a delivery gate answer.
type Verdict string

const (
	VerdictDeliver  Verdict = "deliver"
	VerdictSuppress Verdict = "suppress"
)

// Reason names why a verdict came out the way it did — decisions must be
// explainable (#128's observability posture, as in routing.Result).
type Reason string

const (
	// ReasonLegacy: no declaration — grandfathered full delivery.
	ReasonLegacy Reason = "legacy-undeclared"
	// ReasonUngated: the event's class is not gated.
	ReasonUngated Reason = "ungated-class"
	// ReasonAccepted: gated event, declared in accepts.
	ReasonAccepted Reason = "accepted"
	// ReasonNotAccepted: gated event, absent from accepts — the one path
	// that suppresses.
	ReasonNotAccepted Reason = "not-accepted"
)

// Decision is one delivery-gate evaluation.
type Decision struct {
	Verdict Verdict `json:"verdict"`
	Reason  Reason  `json:"reason"`
	Class   Class   `json:"class"`
	Event   string  `json:"event"`
}

// Decide applies the delivery gate for one client and one event. A nil
// declaration is the undeclared legacy client and always delivers; a
// declared client is suppressed exactly when the event is a presentation
// command absent from its accepts (docs/interface-capabilities.md, gate
// table).
func Decide(decl *Declaration, event string) Decision {
	class := Classify(event)
	d := Decision{Verdict: VerdictDeliver, Class: class, Event: event}
	switch {
	case decl == nil:
		d.Reason = ReasonLegacy
	case class != ClassPresentationCommand:
		d.Reason = ReasonUngated
	default:
		if _, ok := decl.Accepts[event]; ok {
			d.Reason = ReasonAccepted
		} else {
			d.Verdict, d.Reason = VerdictSuppress, ReasonNotAccepted
		}
	}
	return d
}
