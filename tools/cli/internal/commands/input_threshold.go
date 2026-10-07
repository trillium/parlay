// The lines that tell the operator what the view is comparing against: the
// hold threshold in force, how much confidence the window actually carried,
// and which input came closest to the line. Split from the row rendering
// because they are about the ledger's posture rather than any one input, and
// because all of them are load-bearing honesty: a "held" row without a
// visible threshold, a window with no reported confidence shown as though it
// were confident, or a near-miss that is never named, would each be a view
// that lies.
package commands

import (
	"fmt"
	"math"
)

// inputLegend is the one-line key to the state and CONF columns.
const inputLegend = "Legend: delivered = handed a listener or typed at the target; queued = waiting;\n" +
	"refused = an intake or the target declined it; no match = it named a destination that\n" +
	"did not match; command = the engine read it as that phrase command (WHY names which),\n" +
	"so it was acted on rather than left as text; low confidence = measured below the\n" +
	"threshold; held = actually stopped by it; superseded = a later input replaced it before\n" +
	"it was acted on. WHY names the reason in every case. CONF is the recognition confidence\n" +
	"the surface reported for that input; \"-\" means none was reported, which is not a\n" +
	"confidence of zero. Every INPUT id is printed whole, and pasting one into\n" +
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

// confidenceClause is the per-input fact this seam exists to show: the
// confidence a surface actually reported for THIS input, and where it sat
// against the threshold in force. It is rendered for every input that
// reported one — not only for the ones that failed — because an input that
// cleared a strict threshold by a hundredth is the one an operator wants to
// look at before it misses, and a window where nothing ever comes near the
// line is a threshold doing no work at all.
//
// An unreported confidence returns "" rather than a number: "not reported" is
// a different answer from a confidence of zero, and the two must not render
// alike (see inputlog.Judge).
//
// Margins are shown at the 0.01 this view prints, so a difference smaller
// than half a hundredth reads as "at threshold" rather than as a signed zero.
func confidenceClause(confidence, threshold *float64) string {
	if confidence == nil {
		return ""
	}
	if threshold == nil {
		return fmt.Sprintf("confidence %.2f (no threshold set)", *confidence)
	}
	margin := roundMargin(*confidence - *threshold)
	switch {
	case margin > 0:
		return fmt.Sprintf("confidence %.2f above threshold %.2f (margin %.2f)", *confidence, *threshold, margin)
	case margin < 0:
		// The wording the held and low-confidence rows already used, kept so
		// one fact does not acquire two spellings.
		return fmt.Sprintf("confidence %.2f below threshold %.2f", *confidence, *threshold)
	default:
		return fmt.Sprintf("confidence %.2f at threshold %.2f", *confidence, *threshold)
	}
}

// confidenceWhy renders a hold so the threshold behind it is visible rather
// than implied.
func confidenceWhy(e inputEvent) string {
	if e.Confidence == nil || e.Threshold == nil {
		if e.Reason != "" {
			return e.Reason
		}
		return "held by policy"
	}
	return confidenceClause(e.Confidence, e.Threshold)
}

// confidenceCell is the CONF column: the reported confidence, or "-" when the
// surface reported none — which is not zero, and not a quiet hold.
func confidenceCell(r inputRow) string {
	if r.Confidence == nil {
		return "-"
	}
	return fmt.Sprintf("%.2f", *r.Confidence)
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

// thresholdMarginLine names the input nearest the hold line. The threshold on
// its own says what the policy is; it says nothing about whether anything came
// close to it, which is the fact an operator needs in order to judge whether
// the threshold sits where they want it.
//
// Every number here comes from the input's OWN record — the confidence it
// reported and the threshold that was in force when it was measured — so a
// threshold changed since then cannot silently re-judge an older input. Rows
// whose confidence was reported while no threshold was set are therefore
// incomparable rather than "far above", and that case says so.
func thresholdMarginLine(rows []inputRow, min *float64) string {
	if min == nil {
		// The threshold line already states that nothing is held, so there is
		// no line to be near.
		return ""
	}
	var closest *inputRow
	var margin float64
	reported, measured := 0, 0
	for i := range rows {
		r := &rows[i]
		if r.Confidence == nil {
			continue
		}
		reported++
		if r.Threshold == nil {
			continue // measured with the threshold off; not comparable
		}
		measured++
		m := roundMargin(*r.Confidence - *r.Threshold)
		if closest == nil {
			closest, margin = r, m
			continue
		}
		if best := math.Abs(margin); math.Abs(m) < best ||
			(math.Abs(m) == best && (r.At.After(closest.At) || (r.At.Equal(closest.At) && r.ID < closest.ID))) {
			closest, margin = r, m
		}
	}
	switch {
	case measured == 0 && reported == 0:
		return "Threshold margin: no input in this window reported a confidence, so none can be measured against it."
	case measured == 0:
		return "Threshold margin: the inputs in this window were measured with no threshold set, so none is comparable with the one in force now."
	}
	return fmt.Sprintf("Threshold margin: the closest input in this window is %s — %s. Nothing else came nearer the line.",
		closest.ID, confidenceClause(closest.Confidence, closest.Threshold))
}

// roundMargin rounds a confidence difference to the two decimals this view
// prints, so the printed margin and the printed verdict cannot disagree.
func roundMargin(v float64) float64 { return math.Round(v*100) / 100 }
