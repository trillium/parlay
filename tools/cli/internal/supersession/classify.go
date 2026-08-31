package supersession

import "fmt"

// This file is the severity-classification half of the #128 §17 rule:
// "SemVer determines the severity of workflow change". The declared version
// bump says how severe the author CLAIMS the change is; the changeset says
// how severe the change provably IS. The engine holds the two against each
// other with one asymmetric rule:
//
//	declared severity >= classified severity, or the supersession is
//	rejected.
//
// Understating is refused because it is how a breaking change sneaks past
// reprocessing as a "patch" — the ledger would then report downstream work
// valid that the changeset already proves invalid. Overstating is allowed:
// an author who does not trust a change may escalate it, and the EFFECTIVE
// severity (the one that drives reprocessing) is the declared one, so
// escalation only ever buys more revalidation, never less.

// MinSeverity is the severity floor a single change class imposes. The
// mapping is the concrete rule set (#128 §17's non-breaking / optimization /
// breaking, made mechanical):
//
//	annotation → patch  (consumer-invisible; nothing downstream can be wrong)
//	additive   → minor  (new optional structure; existing consumers
//	                     unaffected until they opt in — SemVer MINOR)
//	compatible → minor  (behavior refined, contract preserved; existing
//	                     outputs presumed valid but revalidation is owed —
//	                     the presumption is not a proof)
//	breaking   → major  (removal / new requirement / changed meaning;
//	                     existing dependent outputs presumed invalid)
func MinSeverity(c ChangeClass) (Severity, error) {
	switch c {
	case ChangeAnnotation:
		return SeverityPatch, nil
	case ChangeAdditive, ChangeCompatible:
		return SeverityMinor, nil
	case ChangeBreaking:
		return SeverityMajor, nil
	}
	return "", fmt.Errorf("classify: unknown change class %q", c)
}

// Classify returns the severity floor a whole changeset imposes: the
// maximum of the per-change floors. One breaking change makes the whole
// supersession major, however many annotations ride along with it. An empty
// changeset is an error — a supersession that cannot say what changed
// cannot be classified, and parlay does not guess.
func Classify(changes []Change) (Severity, error) {
	if len(changes) == 0 {
		return "", fmt.Errorf("classify: empty changeset")
	}
	floor := SeverityPatch
	for i, c := range changes {
		min, err := MinSeverity(c.Class)
		if err != nil {
			return "", fmt.Errorf("classify: change %d: %w", i, err)
		}
		if min.AtLeast(floor) {
			floor = min
		}
	}
	return floor, nil
}
