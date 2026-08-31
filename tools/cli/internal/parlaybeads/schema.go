// The crew-bead schema constants (status-lift unit 2). This file is the
// machine-readable half of docs/crew-bead-schema.md — the two must move
// together. It deliberately mirrors the shape of gascity's
// internal/session/info_codec.go: a key→setter fold table over flat string
// metadata, so the schema is one table to read and one table to test, not
// assignments scattered across call sites.
//
// Nothing consumes this yet: the writer (unit 3) and reconciler (unit 4)
// adopt it later. No beads-library import belongs here (the topology seam is
// libclient.go alone; TestBeadsImportConfined enforces it).
package parlaybeads

import "fmt"

// Bead shape constants (docs/crew-bead-schema.md "The bead").
const (
	// BeadTypeAgent is the issue type of every crew bead.
	BeadTypeAgent = "agent"
	// LabelCrew is the enumeration handle: ListByLabel(LabelCrew) finds the
	// crew without knowing ids.
	LabelCrew = "parlay-crew"
)

// The writer verb vocabulary — exactly the 7 verbs of
// commands/status_verb.go's statusVerbs, spelled identically.
const (
	VerbWorking       = "working"
	VerbNeedsDecision = "needs-decision"
	VerbBlocked       = "blocked"
	VerbPaused        = "paused"
	VerbDone          = "done"
	VerbFailed        = "failed"
	VerbResolved      = "resolved"
)

// VerbCaptainHeld is reader-plane vocabulary (firstmate's classifier;
// accepted by the crew-state fold). No parlay verb emits it and it is never
// stored in a crew bead — it exists here only so readers have one spelling.
const VerbCaptainHeld = "captain-held"

// Metadata keys (docs/crew-bead-schema.md "Metadata key vocabulary").
const (
	KeyAgentID    = "agent_id"
	KeyStatusVerb = "status_verb"
	KeyStatusKey  = "status_key"
	KeyStatusNote = "status_note"
	KeyStatusAt   = "status_at"
	// DecisionKeyPrefix + <slug> tracks one keyed decision; values are
	// DecisionOpen or DecisionResolved.
	DecisionKeyPrefix = "decision."
	// KeyGCSession is the attachment pointer to the agent record the SPAWN
	// seam owns (report §6.1 point 4): the gc session bead id stamped into
	// identity.md at gc-spawn. Written when the stamp exists; a crew bead
	// carries status ABOUT that record, it never replaces it.
	KeyGCSession = "gc_session"
)

// Keyed-decision states (the values under DecisionKeyPrefix keys).
const (
	DecisionOpen     = "open"
	DecisionResolved = "resolved"
)

// verbSpec is one row of the normative status-mapping table: how a parlay
// verb projects onto a beads status, and whether it terminates the bead.
type verbSpec struct {
	Verb string
	// BeadStatus is the projection written alongside the verbatim verb. The
	// verb is data, the beads status is a view; the mapping is many-to-one
	// and not required to be invertible.
	BeadStatus string
	// CloseReason is non-empty exactly when Terminal: the reason passed to
	// CloseBead.
	CloseReason string
	Terminal    bool
}

// verbSpecs is the status-mapping table from docs/crew-bead-schema.md,
// row-for-row. Ordering matches statusVerbs in commands/status_verb.go so a
// diff against the writer vocabulary is a column read.
var verbSpecs = []verbSpec{
	{Verb: VerbWorking, BeadStatus: StatusInProgress},
	{Verb: VerbNeedsDecision, BeadStatus: StatusBlocked},
	{Verb: VerbBlocked, BeadStatus: StatusBlocked},
	{Verb: VerbPaused, BeadStatus: StatusDeferred},
	{Verb: VerbDone, BeadStatus: StatusClosed, CloseReason: "done", Terminal: true},
	{Verb: VerbFailed, BeadStatus: StatusClosed, CloseReason: "failed", Terminal: true},
	{Verb: VerbResolved, BeadStatus: StatusInProgress},
}

// VerbSpec looks up the mapping row for a writer verb. ok is false for
// anything outside the 7-verb vocabulary — including VerbCaptainHeld, which
// is deliberately unmapped (never stored).
func VerbSpec(verb string) (beadStatus, closeReason string, terminal, ok bool) {
	for _, s := range verbSpecs {
		if s.Verb == verb {
			return s.BeadStatus, s.CloseReason, s.Terminal, true
		}
	}
	return "", "", false, false
}

// WriterVerbs returns the 7-verb writer vocabulary in table order.
func WriterVerbs() []string {
	out := make([]string, len(verbSpecs))
	for i, s := range verbSpecs {
		out[i] = s.Verb
	}
	return out
}

// CrewStatus is the typed view of one crew bead's status metadata — what the
// fold below produces and what RenderStatusLine projects back into
// firstmate's line grammar.
type CrewStatus struct {
	AgentID string
	Verb    string
	Key     string
	Note    string
	At      string // RFC3339, verbatim as stored
	// Decisions maps decision slug → DecisionOpen/DecisionResolved, folded
	// from DecisionKeyPrefix keys.
	Decisions map[string]string
}

// crewKeySpec is one entry of the metadata fold table: a metadata key and the
// setter that lands its value on CrewStatus. Mirrors gascity
// internal/session/info_codec.go's infoKeySpec.
type crewKeySpec struct {
	key string
	set func(*CrewStatus, string)
}

// crewKeyCodec drives CrewStatusFromMetadata. Every entry writes a disjoint
// CrewStatus field (asserted by TestCrewKeyCodecKeysDistinct); decision.*
// keys are handled by prefix in the fold, not listed here.
var crewKeyCodec = []crewKeySpec{
	{KeyAgentID, func(c *CrewStatus, v string) { c.AgentID = v }},
	{KeyStatusVerb, func(c *CrewStatus, v string) { c.Verb = v }},
	{KeyStatusKey, func(c *CrewStatus, v string) { c.Key = v }},
	{KeyStatusNote, func(c *CrewStatus, v string) { c.Note = v }},
	{KeyStatusAt, func(c *CrewStatus, v string) { c.At = v }},
}

// CrewStatusFromMetadata folds a bead's flat metadata into the typed view.
// Unknown keys are ignored (the schema tolerates foreign keys on read;
// growing the vocabulary is a schema change, not a call-site change).
func CrewStatusFromMetadata(meta map[string]string) CrewStatus {
	var c CrewStatus
	for _, spec := range crewKeyCodec {
		if v, ok := meta[spec.key]; ok {
			spec.set(&c, v)
		}
	}
	for k, v := range meta {
		if len(k) > len(DecisionKeyPrefix) && k[:len(DecisionKeyPrefix)] == DecisionKeyPrefix {
			if c.Decisions == nil {
				c.Decisions = map[string]string{}
			}
			c.Decisions[k[len(DecisionKeyPrefix):]] = v
		}
	}
	return c
}

// StatusMetadata is the write-side inverse of the fold: the metadata merge
// for one status write. The decision.* transition (if the write is keyed) is
// the writer's concern in unit 3, not encoded here.
func (c CrewStatus) StatusMetadata() map[string]string {
	return map[string]string{
		KeyStatusVerb: c.Verb,
		KeyStatusKey:  c.Key,
		KeyStatusNote: c.Note,
		KeyStatusAt:   c.At,
	}
}

// RenderStatusLine projects the typed status back into firstmate's exact
// line grammar. It MUST stay byte-identical to commands/status_verb.go's
// buildStatusLine (trailing newline included) — ~30 firstmate scripts parse
// this shape, and under the lift the line is a rendered view of the bead,
// not the storage format. TestRenderStatusLineByteShape pins it.
func (c CrewStatus) RenderStatusLine() string {
	verbPart := c.Verb
	if c.Key != "" {
		verbPart = fmt.Sprintf("%s [key=%s]", c.Verb, c.Key)
	}
	if c.Note != "" {
		return fmt.Sprintf("%s: %s\n", verbPart, c.Note)
	}
	return fmt.Sprintf("%s:\n", verbPart)
}
