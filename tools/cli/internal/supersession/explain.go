package supersession

import (
	"fmt"
	"strings"
)

// This file is the observability contract of the policy (#128 §111.3:
// everything important is inspectable): for any record, "why was this
// record superseded and what did it trigger" must be answerable from the
// ledger alone. Explain assembles the answer as structured data; Render
// turns it into a human-readable trace, following the routing engine's
// result-plus-trace pattern.

// SupersessionDetail is one supersession seen from a record's point of
// view: the full why (changes, reason, both severities, actor) and the
// what-it-triggered (the requirement, with its live resolution state).
type SupersessionDetail struct {
	// Seq is the supersede event's position in the log.
	Seq int `json:"seq"`
	// SupersededID → NewHeadID: the replaced record and its successor.
	SupersededID string   `json:"supersededId"`
	NewHeadID    string   `json:"newHeadId"`
	Changes      []Change `json:"changes"`
	Reason       string   `json:"reason"`
	// DeclaredSeverity is effective; ClassifiedSeverity is the changeset's
	// proven floor (claim vs evidence — see classify.go).
	DeclaredSeverity   Severity `json:"declaredSeverity"`
	ClassifiedSeverity Severity `json:"classifiedSeverity"`
	Actor              string   `json:"actor,omitempty"`
	At                 string   `json:"at,omitempty"`
	// Requirement is what the supersession triggered, nil when nothing
	// was owed (patch, no captain mark).
	Requirement *Requirement `json:"requirement,omitempty"`
}

// Explanation is the full story of one record.
type Explanation struct {
	Record Record `json:"record"`
	// IsHead: nothing supersedes this record yet.
	IsHead bool `json:"isHead"`
	// Origin explains how this record came to exist: nil means it was
	// registered as a chain root; otherwise it is the supersession that
	// created it (this record is that detail's NewHeadID).
	Origin *SupersessionDetail `json:"origin,omitempty"`
	// Superseded explains why this record was replaced and what that
	// triggered: nil while the record is the head.
	Superseded *SupersessionDetail `json:"superseded,omitempty"`
	// ActedOn: who relied on this record — the marks that make its
	// supersession captain-visible.
	ActedOn []ActedOnMark `json:"actedOn,omitempty"`
}

func (l *Ledger) supersessionDetail(ev Event) *SupersessionDetail {
	d := &SupersessionDetail{
		Seq:                ev.Seq,
		SupersededID:       ev.Record.Supersedes,
		NewHeadID:          ev.Record.ID,
		Changes:            append([]Change(nil), ev.Changes...),
		Reason:             ev.Reason,
		DeclaredSeverity:   ev.DeclaredSeverity,
		ClassifiedSeverity: ev.ClassifiedSeverity,
		Actor:              ev.Actor,
		At:                 ev.At,
	}
	if r, ok := l.requirements[fmt.Sprintf("req-%d", ev.Seq)]; ok {
		req := *r
		d.Requirement = &req
	}
	return d
}

// Explain answers "why does this record exist, why was it superseded, and
// what did that trigger" from the ledger alone.
func (l *Ledger) Explain(recordID string) (Explanation, error) {
	r, ok := l.records[recordID]
	if !ok {
		return Explanation{}, fmt.Errorf("explain: record %s does not exist", recordID)
	}
	e := Explanation{
		Record:  r,
		IsHead:  l.supersededBy[recordID] == "",
		ActedOn: l.ActedOnMarks(recordID),
	}
	for i := range l.events {
		ev := &l.events[i]
		if ev.Kind != EventSupersede {
			continue
		}
		if ev.Record.ID == recordID {
			e.Origin = l.supersessionDetail(*ev)
		}
		if ev.Record.Supersedes == recordID {
			e.Superseded = l.supersessionDetail(*ev)
		}
	}
	return e, nil
}

func (d *SupersessionDetail) render(b *strings.Builder, verb string) {
	fmt.Fprintf(b, "  %s (event %d", verb, d.Seq)
	if d.Actor != "" {
		fmt.Fprintf(b, ", by %s", d.Actor)
	}
	if d.At != "" {
		fmt.Fprintf(b, " at %s", d.At)
	}
	fmt.Fprintf(b, "): declared %s, classified %s\n", d.DeclaredSeverity, d.ClassifiedSeverity)
	fmt.Fprintf(b, "    reason: %s\n", d.Reason)
	for _, c := range d.Changes {
		fmt.Fprintf(b, "    change [%s]: %s\n", c.Class, c.Detail)
	}
	if d.Requirement == nil {
		fmt.Fprintf(b, "    triggered: nothing (no reprocessing owed)\n")
		return
	}
	r := d.Requirement
	flags := ""
	if r.CaptainVisible {
		flags += " [captain-visible]"
	}
	if r.StalenessSource {
		flags += " [staleness-source]"
	}
	state := "PENDING"
	if r.Resolved {
		state = fmt.Sprintf("resolved by %s (%s)", r.ResolvedBy, r.ResolutionNote)
	}
	fmt.Fprintf(b, "    triggered: %s → %s%s — %s\n", r.ID, r.Action, flags, state)
}

// Render is the human-readable form of the explanation.
func (e Explanation) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "record %s: %s %q v%s\n", e.Record.ID, e.Record.Kind, e.Record.Name, e.Record.Version)
	if e.IsHead {
		fmt.Fprintf(&b, "  status: current head of chain %q\n", e.Record.Name)
	} else if e.Superseded != nil {
		fmt.Fprintf(&b, "  status: superseded by %s\n", e.Superseded.NewHeadID)
	}
	for _, m := range e.ActedOn {
		line := fmt.Sprintf("  acted on by %s", m.Actor)
		if m.Note != "" {
			line += fmt.Sprintf(" (%s)", m.Note)
		}
		if m.At != "" {
			line += fmt.Sprintf(" at %s", m.At)
		}
		b.WriteString(line + "\n")
	}
	if e.Origin == nil {
		fmt.Fprintf(&b, "  origin: registered as chain root\n")
	} else {
		e.Origin.render(&b, "origin: superseded "+e.Origin.SupersededID)
	}
	if e.Superseded != nil {
		e.Superseded.render(&b, "superseded")
	}
	return b.String()
}
