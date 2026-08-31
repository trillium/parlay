// Package supersession is the supersession-with-SemVer-severity model from
// issue #128 (§13–§19, §77, §100–§101), the representation-plane policy the
// plane split keeps on parlay's side (docs/gascity-plane-boundary.md §2.3,
// register row 4: Gas City surfaces the fact of drift; the migrate/supersede/
// severity POLICY is parlay's to define — this package defines it). Full
// model: docs/supersession.md.
//
// The core rule is #128 §111.5: supersession replaces mutation. A record
// (workflow version, contract version, or any other versioned definition) is
// never edited in place. A new record supersedes the old one; both remain in
// the ledger forever (§15, §79), so existing work keeps an exact reference to
// the version that governed it (§14) and history stays reproducible (§78).
//
// The engine is deliberately pure: it folds an append-only event log into
// chains, heads, and (in later units) reprocessing requirements. It has no
// I/O, no clock, and no inference — parlay has no inherent inference (#128
// §81), and every answer this package gives is a deterministic function of
// the events it was handed.
//
// One invariant guards the captain-authority boundary (VISION.md:21, "An
// agent may speak; only the captain decides what happens next"): a record the
// captain has acted on carries acted-on marks, and superseding a marked
// record must never be silent — the reprocessing consequence (a later unit)
// is forced to be captain-visible regardless of severity.
package supersession

import (
	"fmt"
	"strconv"
	"strings"
)

// Severity classifies how disruptive a supersession is, in SemVer terms
// (#128 §17: "non-breaking change, optimization, breaking change"). It is
// both the name of a version-bump kind (BumpKind) and, in the classify unit,
// the floor a changeset imposes on the declared bump.
type Severity string

const (
	// SeverityPatch: no consumer-visible behavior changed. Existing
	// downstream outputs remain valid; no reprocessing.
	SeverityPatch Severity = "patch"
	// SeverityMinor: backward-compatible addition or optimization (#128
	// §17). Existing outputs are presumed valid but should be revalidated
	// by whatever workflow owns them.
	SeverityMinor Severity = "minor"
	// SeverityMajor: breaking change. Existing dependent outputs are
	// presumed invalid; reprocessing is required, and the supersession is
	// a staleness source for dependent work (#128 §21–§22).
	SeverityMajor Severity = "major"
)

// severityRank orders severities so "at least as severe" is comparable.
// Unknown severities rank below patch so they can never satisfy a floor.
func severityRank(s Severity) int {
	switch s {
	case SeverityPatch:
		return 1
	case SeverityMinor:
		return 2
	case SeverityMajor:
		return 3
	}
	return 0
}

// AtLeast reports whether s is at least as severe as floor.
func (s Severity) AtLeast(floor Severity) bool {
	return severityRank(s) >= severityRank(floor)
}

// Version is a strict numeric SemVer triple. Pre-release and build metadata
// are deliberately rejected: a supersession chain records released
// definitions, and the severity math below needs exactly three ordered
// numeric fields.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}

// ParseVersion parses "MAJOR.MINOR.PATCH" with plain non-negative decimal
// fields. Anything else — signs, prerelease tags, a leading "v", empty
// fields — is an error, loudly: a version that parses two ways is a version
// two agents disagree about.
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version %q: want MAJOR.MINOR.PATCH", s)
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return Version{}, fmt.Errorf("version %q: field %q is not a plain non-negative integer", s, p)
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("version %q: %v", s, err)
		}
		n[i] = v
	}
	return Version{Major: n[0], Minor: n[1], Patch: n[2]}, nil
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare returns -1, 0, or 1 by SemVer precedence.
func (v Version) Compare(o Version) int {
	for _, d := range []int{v.Major - o.Major, v.Minor - o.Minor, v.Patch - o.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}

// BumpKind classifies the version step from one record to its successor as
// exactly one Severity, or rejects it. The rules are concrete, not vibes:
//
//   - major bump: Major increases, Minor and Patch are 0 (SemVer: lower
//     fields MUST reset on a higher-field increment).
//   - minor bump: Major unchanged, Minor increases, Patch is 0.
//   - patch bump: Major and Minor unchanged, Patch increases.
//
// Skips are allowed (1.0.0 → 3.0.0 is a major bump — the versions between
// simply never existed in this chain); any non-increase, and any increase
// that fails the reset rule, is an error.
func BumpKind(from, to Version) (Severity, error) {
	switch {
	case to.Major > from.Major:
		if to.Minor != 0 || to.Patch != 0 {
			return "", fmt.Errorf("version step %s → %s: a major bump must reset minor and patch to 0", from, to)
		}
		return SeverityMajor, nil
	case to.Major == from.Major && to.Minor > from.Minor:
		if to.Patch != 0 {
			return "", fmt.Errorf("version step %s → %s: a minor bump must reset patch to 0", from, to)
		}
		return SeverityMinor, nil
	case to.Major == from.Major && to.Minor == from.Minor && to.Patch > from.Patch:
		return SeverityPatch, nil
	}
	return "", fmt.Errorf("version step %s → %s: a supersession must increase the version", from, to)
}

// ChangeClass is the mechanical vocabulary a supersession describes itself
// in. Parlay knows mechanics, not domain meaning (#128 §86): the classes say
// what kind of edit happened to the definition's consumer-visible surface,
// never what the definition is about.
type ChangeClass string

const (
	// ChangeAnnotation: consumer-invisible — descriptions, metadata,
	// documentation. Cannot alter any consumer-visible behavior.
	ChangeAnnotation ChangeClass = "annotation"
	// ChangeAdditive: new optional structure (a new optional stage, field,
	// or capability). Existing consumers are unaffected until they opt in.
	ChangeAdditive ChangeClass = "additive"
	// ChangeCompatible: behavior refined while the declared contract is
	// preserved — the "optimization" of #128 §17. Existing outputs may
	// remain valid but that validity is a presumption, not a proof.
	ChangeCompatible ChangeClass = "compatible"
	// ChangeBreaking: a removal, a new requirement, or a change to the
	// meaning of existing structure. Existing dependent outputs are
	// presumed invalid.
	ChangeBreaking ChangeClass = "breaking"
)

// KnownChangeClass reports whether c is part of the closed vocabulary. The
// vocabulary is closed on purpose: an unknown class would force the engine
// to guess a severity floor, and parlay does not guess.
func KnownChangeClass(c ChangeClass) bool {
	switch c {
	case ChangeAnnotation, ChangeAdditive, ChangeCompatible, ChangeBreaking:
		return true
	}
	return false
}

// Change is one described edit inside a supersession. Detail is free-form
// human context ("removed the review stage"); Class is what the engine acts
// on.
type Change struct {
	Class  ChangeClass `json:"class"`
	Detail string      `json:"detail"`
}

// Record is one immutable version of a versioned definition. Records are
// beads in the #128 model (§14: "each version of a workflow is its own
// bead"); here they are the pure-data projection of one.
type Record struct {
	// ID uniquely names this version. Caller-supplied: the engine has no
	// randomness, so identity comes from outside (a bead id, a hash, …).
	ID string `json:"id"`
	// Kind is the open-vocabulary definition kind — "workflow",
	// "contract", "architecture", … (#128 §25: relationships and kinds
	// are user-defined; parlay does not own the ontology).
	Kind string `json:"kind"`
	// Name is the logical identity shared by every version in the chain
	// ("triage-workflow"). Head resolution is per Name.
	Name string `json:"name"`
	// Version is this record's strict SemVer triple.
	Version Version `json:"version"`
	// Supersedes is the ID of the record this one supersedes, or "" for
	// the chain root. Exactly the #128 §13 first-class relationship.
	Supersedes string `json:"supersedes,omitempty"`
}

func (r Record) validate() error {
	if r.ID == "" {
		return fmt.Errorf("record: id must not be empty")
	}
	if r.Kind == "" {
		return fmt.Errorf("record %s: kind must not be empty", r.ID)
	}
	if r.Name == "" {
		return fmt.Errorf("record %s: name must not be empty", r.ID)
	}
	return nil
}
