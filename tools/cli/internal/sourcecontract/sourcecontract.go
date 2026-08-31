// Package sourcecontract is the source-enrollment-contract model from issue
// #128 (§28–§33, §71–§79, §104–§109), the representation-plane declaration of
// everything parlay knows about one human input surface: identity, origin
// metadata, delivery semantics, interaction capabilities, trust posture, and
// version. Full model: docs/source-contracts.md.
//
// The core rule is #128 §29: "When a source is enrolled into Parlay, its
// metadata contract is defined. The source's metadata does not simply get
// invented ad hoc every time an input arrives." A surface enrolls by
// declaring a contract — a checked-in file landed through the protected-PR
// flow — never by an API call, so enrollment authority is exactly repo
// authority: the captain's.
//
// The engine is deliberately pure, following internal/routing,
// internal/staleness and internal/supersession: no I/O, no clock, no
// inference. Callers hand it declaration bytes; every answer is a
// deterministic function of those bytes.
//
// The security invariant this package co-owns (docs/source-contracts.md "The
// security story"): a contract must never be able to widen the guarded
// surface. Validation refuses any contract that names a route outside the
// closed ingress-route table, or an event name in the refused rosters below.
// The enforcement points keep their own independent hard-coded refusals —
// the two checks share a vocabulary but not a code path.
package sourcecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/supersession"
)

// Trust is a surface's posture: what it may aim at the system. Closed set —
// #128 is silent on trust, so the classes are derived from the two refusal
// reasons events_ingress.go already enforces plus VISION.md's security
// boundary (docs/source-contracts.md §"Trust posture").
type Trust string

const (
	// TrustObservability: may emit non-persisted observability frames (the
	// tool_event class) through the events ingress. May not originate
	// persisted content, may not aim anything at the panel or captain.
	TrustObservability Trust = "observability"
	// TrustContent: may originate persisted input (messages/beads) through
	// a persisting route, stamped with its origin metadata. May not emit
	// raw SSE frames.
	TrustContent Trust = "content"
	// TrustControl: a captain-facing interactive surface that participates
	// in interaction workflows (#128 §33, §108). May declare the full
	// capability vocabulary.
	TrustControl Trust = "control"
)

// FieldType types one declared origin-metadata field. Closed set.
type FieldType string

const (
	FieldString    FieldType = "string"
	FieldInt       FieldType = "int"
	FieldBool      FieldType = "bool"
	FieldTimestamp FieldType = "timestamp"
	FieldID        FieldType = "id"
)

var fieldTypes = map[FieldType]bool{
	FieldString: true, FieldInt: true, FieldBool: true,
	FieldTimestamp: true, FieldID: true,
}

// MetadataField is one origin-metadata field the surface supplies with every
// input (#128 §28–§30). In v1 these are descriptive, versioned truth — the
// declaration exists and is versioned instead of being invented ad hoc per
// call site; runtime shape enforcement arrives with bead-store integration.
type MetadataField struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type"`
	Required    bool      `json:"required,omitempty"`
	Description string    `json:"description,omitempty"`
}

// Origin groups the declared metadata fields. The origin travels with the
// bead forever — later edits through other surfaces land in edit history and
// never replace it (#128 §32).
type Origin struct {
	Fields []MetadataField `json:"fields,omitempty"`
}

// DeliveryMode says whether the surface posts in or parlay tails/polls it.
type DeliveryMode string

const (
	DeliveryPush DeliveryMode = "push"
	DeliveryPull DeliveryMode = "pull"
)

// Ordering and Guarantee are the delivery-semantics vocabulary — values
// chosen from what today's producers already exhibit (chosen default; #128
// names delivery nowhere).
type Ordering string

const (
	Ordered   Ordering = "ordered"
	Unordered Ordering = "unordered"
)

type Guarantee string

const (
	AtLeastOnce Guarantee = "at-least-once"
	AtMostOnce  Guarantee = "at-most-once"
)

// Delivery declares how inputs travel from the surface into parlay. Route
// must name an entry of the closed ingress-route table — a contract cannot
// name a route that does not already exist, guarded or not, real or
// invented.
type Delivery struct {
	Mode      DeliveryMode `json:"mode"`
	Route     string       `json:"route"`
	Ordering  Ordering     `json:"ordering"`
	Guarantee Guarantee    `json:"guarantee"`
}

// Interaction is one verb of the structured capability declaration (#128
// §72–§74: structured, not boolean; "exact representation is TBD" — chosen
// representation is a flat closed vocabulary: the verbs §72 itself
// enumerates, plus "originate" for pure input sources).
type Interaction string

const (
	Originate Interaction = "originate"
	View      Interaction = "view"
	Compose   Interaction = "compose"
	Send      Interaction = "send"
	Select    Interaction = "select"
	Confirm   Interaction = "confirm"
)

var interactions = map[Interaction]bool{
	Originate: true, View: true, Compose: true,
	Send: true, Select: true, Confirm: true,
}

// Contract is one surface's full declaration (#128 §30, §76). Version is
// strict numeric SemVer via internal/supersession — a contract change
// follows the supersession model with kind "source-contract" (#128 §31,
// §77); the ledger integration is a follow-up unit, the schema carries the
// version from day one so the chain is well-formed when it lands.
type Contract struct {
	Name         string        `json:"name"`
	Version      string        `json:"version"`
	Title        string        `json:"title"`
	Description  string        `json:"description,omitempty"`
	Trust        Trust         `json:"trust"`
	Origin       Origin        `json:"origin"`
	Delivery     Delivery      `json:"delivery"`
	Capabilities []Interaction `json:"capabilities,omitempty"`
	Emits        []string      `json:"emits,omitempty"`
}

// SemVer returns the parsed version. Valid only after Validate.
func (c Contract) SemVer() (supersession.Version, error) {
	return supersession.ParseVersion(c.Version)
}

// ── The closed route table ──────────────────────────────────────────────────
// Which existing ingress routes a contract may name, per posture. Growth is
// an engine change reviewed like a guard change (docs/source-contracts.md
// "The security story"): a genuinely new route needs the route AND its guard
// entry on both sides first, and only then can a contract name it.

const (
	RouteEvents  = "POST /api/chat/events"  // observability frames → the Go hub
	RouteMessage = "POST /api/chat/message" // persisted messages (type/source/meta passthrough)
	// routePluginPrefix admits plugin-RPC routes ("POST /api/chat/plugin/…"),
	// which are guarded as a prefix on both sides so a plugin added later is
	// guarded by default.
	routePluginPrefix = "POST /api/chat/plugin/"
)

// routesByTrust binds posture to the routes it may declare. observability
// may only put frames through the events ingress; content may only originate
// through the persisting message route; control surfaces speak plugin RPC.
func routeAllowed(t Trust, route string) bool {
	switch t {
	case TrustObservability:
		return route == RouteEvents
	case TrustContent:
		return route == RouteMessage
	case TrustControl:
		return strings.HasPrefix(route, routePluginPrefix) && len(route) > len(routePluginPrefix)
	}
	return false
}

// ── Refused event-name rosters ──────────────────────────────────────────────
// No declaration, at any trust level, may claim these. The enforcement point
// (events_ingress.go) remains the contract owner of the refusal doctrine and
// keeps its own hard-coded copy; this validation exists so the bad contract
// cannot land on main in the first place. Defense in depth: shared
// vocabulary, separate code paths.

// panelAiming: names that drive a device or the captain's panel. Union of
// events_ingress.go's panel-aiming refusals and the device-scoped names
// events.go's busEmitEvents keeps structurally out of the bus consume path.
var panelAiming = map[string]bool{
	"navigate": true, "reload": true, "device_cmd": true,
	"input_action": true, "draft": true, "tts_event": true,
	"pages_patch": true, "cursorless_rpc": true,
}

// serverOwned: names the server produces to report its own persisted state.
// Accepting one from outside would put a frame on the panel that is in no
// history file and that no reconnect reproduces.
var serverOwned = map[string]bool{
	"connected": true, "history": true, "agents": true,
	"agent_register": true, "presence_map": true, "message": true,
	"message_received": true, "commands": true, "command_update": true,
}

// notEventNames: vocabulary that looks like an event name but is not one.
// "system_update" is a ChatMessage.type carried on the "message" event; a
// producer that wants one posts to /api/chat/message.
var notEventNames = map[string]bool{
	"system_update": true,
}

// RefusedEventName reports whether name may never appear in a contract's
// emits, with the reason. Exported so enforcement-side tests can pin their
// hard-coded rosters against this one.
func RefusedEventName(name string) (reason string, refused bool) {
	switch {
	case panelAiming[name]:
		return "panel-aiming", true
	case serverOwned[name]:
		return "server-owned", true
	case notEventNames[name]:
		return "not an event name (a ChatMessage.type)", true
	}
	return "", false
}

// ── Parse and validate ──────────────────────────────────────────────────────

var (
	// nameRE: chain identity. Lowercase slug, no leading/trailing hyphen —
	// once enrolled, never reused for a different surface.
	nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// eventNameRE matches the existing SSE-name shape (tool_event,
	// command_update, …): lowercase snake_case.
	eventNameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	// fieldNameRE matches origin-metadata field names (note_id, session_id).
	fieldNameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
)

// Parse decodes one declaration, strictly: unknown fields are an error,
// loudly — parlay does not guess (#128: unknown vocabulary is rejected, not
// interpreted). Parse validates; a returned Contract is a valid Contract.
func Parse(raw []byte) (Contract, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Contract
	if err := dec.Decode(&c); err != nil {
		return Contract{}, fmt.Errorf("source contract: %v", err)
	}
	// A second document in the same file is a mistake, not an extension.
	if dec.More() {
		return Contract{}, fmt.Errorf("source contract %q: trailing content after the declaration", c.Name)
	}
	if err := Validate(c); err != nil {
		return Contract{}, err
	}
	return c, nil
}

// Validate checks one contract against the closed vocabularies, the
// posture/declaration consistency rules, and the refused-name rosters.
func Validate(c Contract) error {
	if !nameRE.MatchString(c.Name) {
		return fmt.Errorf("source contract: name %q is not a lowercase slug", c.Name)
	}
	e := func(format string, args ...any) error {
		return fmt.Errorf("source contract %q: %v", c.Name, fmt.Sprintf(format, args...))
	}
	if _, err := supersession.ParseVersion(c.Version); err != nil {
		return e("%v", err)
	}
	if strings.TrimSpace(c.Title) == "" {
		return e("title is required")
	}
	switch c.Trust {
	case TrustObservability, TrustContent, TrustControl:
	default:
		return e("trust %q is not one of observability, content, control", c.Trust)
	}

	// Origin metadata fields: closed types, unique lowercase names.
	seenField := map[string]bool{}
	for _, f := range c.Origin.Fields {
		if !fieldNameRE.MatchString(f.Name) {
			return e("origin field name %q is not lowercase snake_case", f.Name)
		}
		if seenField[f.Name] {
			return e("origin field %q declared twice", f.Name)
		}
		seenField[f.Name] = true
		if !fieldTypes[f.Type] {
			return e("origin field %q: type %q is not one of string, int, bool, timestamp, id", f.Name, f.Type)
		}
	}

	// Delivery vocabulary and the closed route table.
	if c.Delivery.Mode != DeliveryPush && c.Delivery.Mode != DeliveryPull {
		return e("delivery mode %q is not push or pull", c.Delivery.Mode)
	}
	if c.Delivery.Ordering != Ordered && c.Delivery.Ordering != Unordered {
		return e("delivery ordering %q is not ordered or unordered", c.Delivery.Ordering)
	}
	if c.Delivery.Guarantee != AtLeastOnce && c.Delivery.Guarantee != AtMostOnce {
		return e("delivery guarantee %q is not at-least-once or at-most-once", c.Delivery.Guarantee)
	}
	if !routeAllowed(c.Trust, c.Delivery.Route) {
		return e("route %q is not an ingress route the %s posture may declare", c.Delivery.Route, c.Trust)
	}

	// Capabilities: closed vocabulary, no duplicates, bounded by posture.
	seenCap := map[Interaction]bool{}
	for _, cap := range c.Capabilities {
		if !interactions[cap] {
			return e("capability %q is not in the interaction vocabulary", cap)
		}
		if seenCap[cap] {
			return e("capability %q declared twice", cap)
		}
		seenCap[cap] = true
	}
	switch c.Trust {
	case TrustObservability:
		// Frames, not interactions: an observability producer aims nothing
		// at anyone.
		if len(c.Capabilities) != 0 {
			return e("observability posture declares no capabilities, got %d", len(c.Capabilities))
		}
	case TrustContent:
		// Content surfaces originate input; the interactive verbs belong to
		// control surfaces.
		for cap := range seenCap {
			if cap != Originate {
				return e("content posture may declare only originate, got %q", cap)
			}
		}
	}

	// Emits: observability only, valid shape, never a refused name.
	if c.Trust != TrustObservability && len(c.Emits) != 0 {
		return e("%s posture may not declare emits", c.Trust)
	}
	if c.Trust == TrustObservability && len(c.Emits) == 0 {
		return e("observability posture requires at least one emits entry")
	}
	seenEmit := map[string]bool{}
	for _, name := range c.Emits {
		if !eventNameRE.MatchString(name) {
			return e("emits %q is not lowercase snake_case", name)
		}
		if seenEmit[name] {
			return e("emits %q declared twice", name)
		}
		seenEmit[name] = true
		if reason, refused := RefusedEventName(name); refused {
			return e("emits %q is refused: %s", name, reason)
		}
	}
	return nil
}

// sortedCopy returns a sorted copy so registry answers are deterministic.
func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
