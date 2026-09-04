// The `parlay doctor --json` document — schema "parlay.doctor/v1". Renders
// the exact same []CheckResult the text mode renders (doctor.go's
// runDoctorChecks), so an LLM or script gets structured evidence and
// argv-form fixes instead of parsing PASS/WARN/FAIL prose.
// Design: https://github.com/trillium/parlay/discussions/256 §1.
package commands

import (
	"encoding/json"
	"fmt"
	"os"
)

// doctorJSONSchema is this document's schema id — bump only alongside a
// documented breaking shape change.
const doctorJSONSchema = "parlay.doctor/v1"

// doctorCheckDoc is one check's --json representation — CheckResult minus
// its text-only Lines.
type doctorCheckDoc struct {
	ID       string         `json:"id"`
	Verdict  verdict        `json:"verdict"`
	Summary  string         `json:"summary"`
	Evidence map[string]any `json:"evidence,omitempty"`
	Fixes    []Fix          `json:"fixes"`
}

// doctorSummaryDoc counts each verdict across every check in the document.
type doctorSummaryDoc struct {
	Pass    int `json:"pass"`
	Warn    int `json:"warn"`
	Fail    int `json:"fail"`
	Unknown int `json:"unknown"`
}

// doctorDocument is the whole `parlay doctor --json` output.
type doctorDocument struct {
	Schema  string           `json:"schema"`
	AgentID string           `json:"agent_id,omitempty"`
	Verdict verdict          `json:"verdict"`
	Checks  []doctorCheckDoc `json:"checks"`
	Summary doctorSummaryDoc `json:"summary"`
}

// aggregateVerdict rolls per-check verdicts up to one document-level
// verdict, aligned with the exit-code contract: FAIL if any check FAILed,
// else WARN if any WARNed, else PASS. UNKNOWN checks (e.g. context-rotation
// with no CLAUDE_CONTEXT_PERCENTAGE set) never raise the aggregate — an
// unknown is not a known problem.
func aggregateVerdict(results []CheckResult) verdict {
	summary := verdictSummary(results)
	switch {
	case summary.Fail > 0:
		return vFail
	case summary.Warn > 0:
		return vWarn
	default:
		return vPass
	}
}

// verdictSummary counts every verdict in results, independent of
// tallyVerdicts (which only counts FAIL/WARN for the exit-code contract).
func verdictSummary(results []CheckResult) doctorSummaryDoc {
	var s doctorSummaryDoc
	for _, r := range results {
		switch r.Verdict {
		case vPass:
			s.Pass++
		case vWarn:
			s.Warn++
		case vFail:
			s.Fail++
		default:
			s.Unknown++
		}
	}
	return s
}

// buildDoctorDocument assembles the --json document from the same results
// the text renderer consumes.
func buildDoctorDocument(results []CheckResult) doctorDocument {
	checks := make([]doctorCheckDoc, 0, len(results))
	for _, r := range results {
		fixes := r.Fixes
		if fixes == nil {
			fixes = []Fix{}
		}
		checks = append(checks, doctorCheckDoc{
			ID:       r.ID,
			Verdict:  r.Verdict,
			Summary:  r.Summary,
			Evidence: r.Evidence,
			Fixes:    fixes,
		})
	}
	return doctorDocument{
		Schema:  doctorJSONSchema,
		AgentID: doctorAgentID(),
		Verdict: aggregateVerdict(results),
		Checks:  checks,
		Summary: verdictSummary(results),
	}
}

// renderDoctorJSON prints the --json document. fails/warns are unused here
// (the document computes its own verdict/summary from results) but kept in
// the signature to match renderDoctorText's call shape in Doctor().
func renderDoctorJSON(results []CheckResult, fails, warns int) {
	doc := buildDoctorDocument(results)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintf(os.Stderr, "parlay doctor --json: encode error: %v\n", err)
	}
}
