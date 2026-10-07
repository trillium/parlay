// The two lines that tell the operator what the view is comparing against:
// the hold threshold in force, and how much confidence the window actually
// carried. Split from the row rendering because they are about the ledger's
// posture rather than any one input, and because both are load-bearing
// honesty: a "held" row without a visible threshold, or a window with no
// reported confidence shown as though it were confident, would each be a
// view that lies.
package commands

import "fmt"

// inputLegend is the one-line key to the state column.
const inputLegend = "Legend: delivered = handed a listener or typed at the target; queued = waiting;\n" +
	"refused = an intake or the target declined it; no match = it named a destination that\n" +
	"did not match; low confidence = measured below the threshold; held = actually stopped\n" +
	"by it; superseded = a later input replaced it before it was acted on. WHY names the\n" +
	"reason in every case. Every INPUT id is printed whole, and pasting one into\n" +
	"`parlay input --input <id>` replays its hops. See docs/input-seam.md."

// thresholdLine states the hold threshold in force, so a hold is never
// inferred from a row: the operator can see the number the server compares
// against, and can tell a disabled threshold from a strict one.
func thresholdLine(min *float64) string {
	if min == nil {
		return "threshold: none — no confidence threshold is set, so nothing is held (set " +
			"PARLAY_INPUT_MIN_CONFIDENCE on the server to hold low-confidence input)"
	}
	return fmt.Sprintf("threshold: hold below confidence %.2f (server PARLAY_INPUT_MIN_CONFIDENCE); "+
		"a hold needs a reported confidence — unreported input is never held", *min)
}

// inputConfidenceNote reports what the window contains, not what the product
// does today: "not reported" is not "confident".
func inputConfidenceNote(rows []inputRow) string {
	reported := 0
	for _, r := range rows {
		if r.Confidence != nil {
			reported++
		}
	}
	if reported == 0 {
		return "Confidence: not reported by any surface in this window — \"not reported\" is not \"confident\"."
	}
	return fmt.Sprintf("Confidence: reported for %d of %d input(s) in this window.", reported, len(rows))
}
