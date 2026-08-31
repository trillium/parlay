package supersession

import "fmt"

// EventKind names the append-only event types the ledger folds. The event
// log IS the history (#128 §7): every state the ledger can report is a
// deterministic fold over these events, so "why is the world like this" is
// always answerable by reading the log.
type EventKind string

const (
	// EventRegister: a chain root was created.
	EventRegister EventKind = "register"
	// EventSupersede: a new record superseded the head of its chain.
	EventSupersede EventKind = "supersede"
	// EventActedOn: the captain (or another actor) acted on a record —
	// the mark that makes later supersession of it captain-visible.
	EventActedOn EventKind = "acted-on"
	// EventRequirementResolved: a reprocessing requirement was discharged
	// with evidence (see requirements.go).
	EventRequirementResolved EventKind = "resolve"
)

// Event is one ledger entry. It is a flat struct on purpose — one JSONL
// line per event — with fields used per Kind:
//
//	register:  Record, Actor, At
//	supersede: Record (the successor; Record.Supersedes names the target),
//	           Changes, Reason, DeclaredSeverity, Actor, At
//	acted-on:  RecordID, Actor, Note, At
type Event struct {
	// Seq is the 1-based position in the log, assigned by the ledger.
	Seq  int       `json:"seq"`
	Kind EventKind `json:"kind"`

	Record  *Record  `json:"record,omitempty"`
	Changes []Change `json:"changes,omitempty"`
	// Reason is the human answer to "why was this record superseded".
	// Required on supersede events: an unexplained supersession is an
	// unanswerable Explain query later.
	Reason string `json:"reason,omitempty"`
	// DeclaredSeverity is the BumpKind of the version step — how severe
	// the author claims the change is. It is the EFFECTIVE severity: the
	// one reprocessing keys on.
	DeclaredSeverity Severity `json:"declaredSeverity,omitempty"`
	// ClassifiedSeverity is the floor the changeset proves (Classify).
	// Always <= DeclaredSeverity, or the supersession was rejected. Both
	// are recorded so Explain can show claim vs evidence.
	ClassifiedSeverity Severity `json:"classifiedSeverity,omitempty"`

	RecordID string `json:"recordId,omitempty"`
	// RequirementID names the requirement a resolve event discharges.
	RequirementID string `json:"requirementId,omitempty"`
	Note          string `json:"note,omitempty"`

	// Actor is who caused the event. "captain" is load-bearing on
	// acted-on events (see ActedOnMark); everywhere else it is history.
	Actor string `json:"actor,omitempty"`
	// At is a caller-supplied timestamp, stored verbatim. The engine has
	// no clock; time is provenance, not input.
	At string `json:"at,omitempty"`
}

// ActorCaptain is the actor string that marks captain authority. Acted-on
// marks by this actor are what the captain-visibility rule keys on
// (VISION.md:21).
const ActorCaptain = "captain"

// ActedOnMark records that an actor acted on a record.
type ActedOnMark struct {
	Actor string `json:"actor"`
	Note  string `json:"note,omitempty"`
	At    string `json:"at,omitempty"`
}

// Ledger is the folded state of an event log: every record ever registered,
// per-name chains, current heads, and acted-on marks. Zero value is not
// usable; construct with NewLedger or Replay.
type Ledger struct {
	events           []Event
	records          map[string]Record
	heads            map[string]string // chain name → head record id
	supersededBy     map[string]string // record id → successor record id
	actedOn          map[string][]ActedOnMark
	requirements     map[string]*Requirement
	requirementOrder []string
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{
		records:      map[string]Record{},
		heads:        map[string]string{},
		supersededBy: map[string]string{},
		actedOn:      map[string][]ActedOnMark{},
		requirements: map[string]*Requirement{},
	}
}

// Replay rebuilds a ledger by folding a stored event log, re-validating
// every event. A log that no longer validates is corruption and fails
// loudly — a ledger that silently skipped an event would report a head that
// history does not support.
func Replay(events []Event) (*Ledger, error) {
	l := NewLedger()
	for i, ev := range events {
		if ev.Seq != i+1 {
			return nil, fmt.Errorf("replay: event %d has seq %d, want %d", i, ev.Seq, i+1)
		}
		var err error
		switch ev.Kind {
		case EventRegister:
			if ev.Record == nil {
				return nil, fmt.Errorf("replay: register event %d has no record", ev.Seq)
			}
			_, err = l.Register(Registration{Record: *ev.Record, Actor: ev.Actor, At: ev.At})
		case EventSupersede:
			if ev.Record == nil {
				return nil, fmt.Errorf("replay: supersede event %d has no record", ev.Seq)
			}
			_, err = l.Supersede(Supersession{
				Record: *ev.Record, Changes: ev.Changes, Reason: ev.Reason,
				Actor: ev.Actor, At: ev.At,
			})
		case EventActedOn:
			_, err = l.MarkActedOn(ev.RecordID, ActedOnMark{Actor: ev.Actor, Note: ev.Note, At: ev.At})
		case EventRequirementResolved:
			_, err = l.ResolveRequirement(ev.RequirementID, Resolution{Actor: ev.Actor, Note: ev.Note, At: ev.At})
		default:
			return nil, fmt.Errorf("replay: event %d has unknown kind %q", ev.Seq, ev.Kind)
		}
		if err != nil {
			return nil, fmt.Errorf("replay: event %d: %w", ev.Seq, err)
		}
	}
	return l, nil
}

// Events returns a copy of the full event log, oldest first.
func (l *Ledger) Events() []Event {
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

func (l *Ledger) append(ev Event) Event {
	ev.Seq = len(l.events) + 1
	l.events = append(l.events, ev)
	return ev
}

// Registration creates a chain root.
type Registration struct {
	Record Record
	Actor  string
	At     string
}

// Register creates a new chain from its root record. The record must not
// supersede anything (that is what Supersede is for), its ID must be new,
// and its Name must not already have a chain — one chain per logical name,
// or head resolution stops meaning anything.
func (l *Ledger) Register(reg Registration) (Event, error) {
	r := reg.Record
	if err := r.validate(); err != nil {
		return Event{}, err
	}
	if r.Supersedes != "" {
		return Event{}, fmt.Errorf("register %s: a chain root must not supersede anything (got %q); use Supersede", r.ID, r.Supersedes)
	}
	if _, exists := l.records[r.ID]; exists {
		return Event{}, fmt.Errorf("register %s: record id already exists", r.ID)
	}
	if head, exists := l.heads[r.Name]; exists {
		return Event{}, fmt.Errorf("register %s: chain %q already exists (head %s); supersede it instead", r.ID, r.Name, head)
	}
	l.records[r.ID] = r
	l.heads[r.Name] = r.ID
	rec := r
	return l.append(Event{Kind: EventRegister, Record: &rec, Actor: reg.Actor, At: reg.At}), nil
}

// Supersession describes one supersede operation: the successor record,
// what changed, and why.
type Supersession struct {
	// Record is the successor. Record.Supersedes must name the current
	// head of chain Record.Name.
	Record Record
	// Changes describes what changed, in the mechanical ChangeClass
	// vocabulary. Must be non-empty: a supersession that cannot say what
	// changed cannot be severity-classified.
	Changes []Change
	// Reason is the human answer to "why". Required.
	Reason string
	Actor  string
	At     string
}

// Supersede appends a successor to a chain. Structural rules, each loud:
//
//   - the target must exist and be the CURRENT head of its chain — chains
//     are linear (#128 §13 shows v2 → supersedes → v1; branching would make
//     "the head" plural and head resolution non-deterministic). Superseding
//     a non-head is a stale supersede and is rejected with the real head
//     named.
//   - the successor keeps the chain's Name and Kind — a supersession
//     replaces a version, never re-identifies the definition.
//   - the version step must be a valid BumpKind (increase + reset rule).
//   - Changes must be non-empty with known classes; Reason must be set.
//   - the declared bump must be at least the changeset's classified
//     severity (see classify.go) — understated bumps are rejected.
//
// Both severities (declared and classified) are recorded on the event.
func (l *Ledger) Supersede(s Supersession) (Event, error) {
	r := s.Record
	if err := r.validate(); err != nil {
		return Event{}, err
	}
	if _, exists := l.records[r.ID]; exists {
		return Event{}, fmt.Errorf("supersede %s: record id already exists", r.ID)
	}
	if r.Supersedes == "" {
		return Event{}, fmt.Errorf("supersede %s: no target; a supersession must name the record it supersedes", r.ID)
	}
	target, ok := l.records[r.Supersedes]
	if !ok {
		return Event{}, fmt.Errorf("supersede %s: target %s does not exist", r.ID, r.Supersedes)
	}
	if head := l.heads[target.Name]; head != target.ID {
		return Event{}, fmt.Errorf("supersede %s: target %s is not the head of chain %q (current head is %s) — stale supersede", r.ID, target.ID, target.Name, head)
	}
	if r.Name != target.Name {
		return Event{}, fmt.Errorf("supersede %s: name %q does not match chain %q — a supersession may not re-identify the definition", r.ID, r.Name, target.Name)
	}
	if r.Kind != target.Kind {
		return Event{}, fmt.Errorf("supersede %s: kind %q does not match target kind %q", r.ID, r.Kind, target.Kind)
	}
	declared, err := BumpKind(target.Version, r.Version)
	if err != nil {
		return Event{}, fmt.Errorf("supersede %s: %w", r.ID, err)
	}
	if len(s.Changes) == 0 {
		return Event{}, fmt.Errorf("supersede %s: changes must be non-empty — a supersession must say what changed", r.ID)
	}
	for i, c := range s.Changes {
		if !KnownChangeClass(c.Class) {
			return Event{}, fmt.Errorf("supersede %s: change %d has unknown class %q", r.ID, i, c.Class)
		}
	}
	if s.Reason == "" {
		return Event{}, fmt.Errorf("supersede %s: reason must not be empty — a supersession must say why", r.ID)
	}
	classified, err := Classify(s.Changes)
	if err != nil {
		return Event{}, fmt.Errorf("supersede %s: %w", r.ID, err)
	}
	// The asymmetric rule (see classify.go): an understated bump is how a
	// breaking change sneaks past reprocessing, so it is rejected; an
	// overstated bump only buys more revalidation and is allowed.
	if !declared.AtLeast(classified) {
		return Event{}, fmt.Errorf("supersede %s: declared bump %s → %s is a %s, but the changeset classifies as %s — understated severity",
			r.ID, target.Version, r.Version, declared, classified)
	}
	// Derive the reprocessing requirement before mutating anything, at the
	// seq the event is about to take, so an error leaves the ledger
	// untouched.
	req, err := l.requirementFor(len(l.events)+1, declared, r.ID, target.ID)
	if err != nil {
		return Event{}, fmt.Errorf("supersede %s: %w", r.ID, err)
	}
	l.records[r.ID] = r
	l.heads[r.Name] = r.ID
	l.supersededBy[target.ID] = r.ID
	if req != nil {
		l.requirements[req.ID] = req
		l.requirementOrder = append(l.requirementOrder, req.ID)
	}
	rec := r
	return l.append(Event{
		Kind: EventSupersede, Record: &rec, Changes: s.Changes, Reason: s.Reason,
		DeclaredSeverity: declared, ClassifiedSeverity: classified, Actor: s.Actor, At: s.At,
	}), nil
}

// MarkActedOn records that an actor acted on a record — the captain merging
// work a workflow version produced, for example. The mark is what forces a
// later supersession of this record to be captain-visible instead of silent.
// Marking a superseded record is allowed: acting on history is still acting.
func (l *Ledger) MarkActedOn(recordID string, mark ActedOnMark) (Event, error) {
	if _, ok := l.records[recordID]; !ok {
		return Event{}, fmt.Errorf("acted-on: record %s does not exist", recordID)
	}
	if mark.Actor == "" {
		return Event{}, fmt.Errorf("acted-on %s: actor must not be empty", recordID)
	}
	l.actedOn[recordID] = append(l.actedOn[recordID], mark)
	return l.append(Event{Kind: EventActedOn, RecordID: recordID, Actor: mark.Actor, Note: mark.Note, At: mark.At}), nil
}

// Record returns the record with the given id.
func (l *Ledger) Record(id string) (Record, bool) {
	r, ok := l.records[id]
	return r, ok
}

// Head resolves the current head of the chain with the given logical name.
// The head is the one record in the chain that nothing supersedes.
func (l *Ledger) Head(name string) (Record, bool) {
	id, ok := l.heads[name]
	if !ok {
		return Record{}, false
	}
	return l.records[id], true
}

// History returns every version of the chain, root first, head last. The
// full chain stays queryable forever (#128 §15: workflow beads are not
// destroyed).
func (l *Ledger) History(name string) []Record {
	id, ok := l.heads[name]
	if !ok {
		return nil
	}
	var rev []Record
	for id != "" {
		r := l.records[id]
		rev = append(rev, r)
		id = r.Supersedes
	}
	out := make([]Record, len(rev))
	for i, r := range rev {
		out[len(rev)-1-i] = r
	}
	return out
}

// ActedOnMarks returns the acted-on marks recorded against a record.
func (l *Ledger) ActedOnMarks(recordID string) []ActedOnMark {
	marks := l.actedOn[recordID]
	out := make([]ActedOnMark, len(marks))
	copy(out, marks)
	return out
}

// SupersededBy returns the successor of a record, if any. Empty string means
// the record is its chain's head.
func (l *Ledger) SupersededBy(recordID string) string {
	return l.supersededBy[recordID]
}
