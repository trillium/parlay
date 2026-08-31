// Status-lift unit 6: the compatibility projection. Under the lift the
// per-agent status file (~/.parlay/agents/<id>/status) becomes a DERIVED
// view of the event log — but ~30 firstmate shell scripts parse that file's
// exact byte shape (fm-classify-lib.sh's grammar), so the projection must
// reproduce it byte for byte, trailing newline included. The golden tests in
// status_projection_test.go pin the identity three ways: renderer-vs-
// renderer over a corpus, the real dual-writing command against its own
// event log, and a checked-in golden fixture that catches both renderers
// drifting together.
package commands

import (
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/crewevents"
	"github.com/trillium/parlay/tools/cli/internal/parlaybeads"
)

// projectStatusFile renders the legacy status file's full content from a
// replayed event log: one buildStatusLine-shaped line per parlay.crew.status
// event, in Seq order, foreign event names skipped. It reproduces exactly
// the lines written SINCE dual-write began for the agent — replaying
// pre-cutover file lines into the log is the unit-7 migration tool's job,
// after which this projection covers the whole file.
func projectStatusFile(evs []crewevents.Event) []byte {
	var b strings.Builder
	for _, ev := range evs {
		if ev.Name != crewevents.EventCrewStatus {
			continue
		}
		b.WriteString(parlaybeads.CrewStatus{Verb: ev.Verb, Key: ev.Key, Note: ev.Note}.RenderStatusLine())
	}
	return []byte(b.String())
}
