// Fold §3.6 + §6 Slice 3 — parlay crew-state reader. Reconciles an agent's
// last keyed status line against parlay's oracle: for now that oracle is
// relay-enrollment presence; run attribution and pane (tab) liveness are
// extension points the TS original stubs out but never implements (see its
// own header comment) — this port stops at the same point.
//
// Ported from packages/cli/src/commands-crew-state.ts.
package commands

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// knownStatusVerbs is the union of terminal (captain-relevant) and routine
// (absorbed) verbs recognized by the fold (fm-classify-lib.sh). A status
// line whose verb isn't in this set is treated as unrecognized.
var knownStatusVerbs = map[string]bool{
	"done": true, "needs-decision": true, "blocked": true, "failed": true, // terminal
	"working": true, "resolved": true, "captain-held": true, "paused": true, // routine
}

// statusLineRe parses a status line in firstmate's grammar: "<verb>
// [key=<slug>]: <note>" or "<verb>: <note>". Shared by crew-state and
// supervise (both TS originals duplicated this identical regex; one
// definition per Go package).
//
// NOTE: \w does not include '-', so a hyphenated verb like "needs-decision"
// or "captain-held" as the LAST status line does not match this pattern at
// all — readLastStatus/findNewActionable then treat the line as
// unparseable. This is a pre-existing defect in the TS original (both
// commands-crew-state.ts and commands-supervise.ts), reproduced here
// faithfully: only the statusFileForAgent fix documented in status_verb.go
// was pre-approved for this ticket. Flagged for a follow-up decision, not
// fixed.
var statusLineRe = regexp.MustCompile(`^(\w+)(?:\s*\[key=([A-Za-z0-9._-]+)\])?\s*:\s*(.*)$`)

type parsedStatus struct {
	verb, key, note string
}

func parseStatusLine(line string) (parsedStatus, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return parsedStatus{}, false
	}
	m := statusLineRe.FindStringSubmatch(line)
	if m == nil {
		return parsedStatus{}, false
	}
	return parsedStatus{verb: m[1], key: m[2], note: m[3]}, true
}

// nonEmptyLines splits content on newlines and drops blank/whitespace-only
// lines. Shared by crew-state's readLastStatus and supervise's
// readAllStatusLines.
func nonEmptyLines(content string) []string {
	var out []string
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func readLastStatus(statusFile string) (parsedStatus, bool) {
	data, err := os.ReadFile(statusFile)
	if err != nil {
		return parsedStatus{}, false
	}
	lines := nonEmptyLines(string(data))
	if len(lines) == 0 {
		return parsedStatus{}, false
	}
	return parseStatusLine(lines[len(lines)-1])
}

// isAgentSubscribed checks agent-registry presence via the relay. Failures
// (unreachable server, non-2xx) are treated as "not subscribed", not fatal —
// crew-state must keep reconciling even when the relay is down.
func isAgentSubscribed(agentID string) bool {
	subs, ok := httpc.TryGetJSON[wire.SubscribersInfo]("/api/chat/subscribers", 3*time.Second)
	if !ok || subs.Registered == nil {
		return false
	}
	for _, a := range subs.Registered.Agents {
		if a.ID == agentID {
			return true
		}
	}
	return false
}

// CrewStateResult is the reconciled {state, source, detail} triple crew-state
// prints as "<state> · source: <src> · <detail>".
type CrewStateResult struct {
	State, Source, Detail string
}

// CrewStateForAgent reconciles agentID's last keyed status line against
// parlay's oracle. Precedence (fold §4, §3.6): (1) not enrolled with the
// relay → unknown; (2) no status ever recorded → unknown; (3) an
// unrecognized verb → unknown; (4) otherwise the verb IS the state.
//
// Fidelity fix (ticket B5, pre-approved — see status_verb.go's
// statusFileForAgent doc): resolves agentID's status file directly instead
// of the caller's own env-derived one.
func CrewStateForAgent(agentID string) CrewStateResult {
	statusFile := statusFileForAgent(agentID)

	if !isAgentSubscribed(agentID) {
		return CrewStateResult{State: "unknown", Source: "none", Detail: "agent not enrolled with relay"}
	}

	last, ok := readLastStatus(statusFile)
	if !ok {
		return CrewStateResult{State: "unknown", Source: "none", Detail: "no status recorded"}
	}

	if !knownStatusVerbs[last.verb] {
		return CrewStateResult{State: "unknown", Source: "status", Detail: fmt.Sprintf("unrecognized verb: %s", last.verb)}
	}

	detail := last.note
	if detail == "" {
		detail = "(no detail)"
	}
	return CrewStateResult{State: last.verb, Source: "status", Detail: detail}
}

// CrewState ports cmdCrewState.
func CrewState(argv []string) {
	if helpWanted("crew-state", argv) {
		return
	}
	r := args.Parse("crew-state", argv, nil, nil)

	agentID := ""
	if len(r.Positionals) > 0 {
		agentID = strings.TrimSpace(r.Positionals[0])
	}
	if agentID == "" {
		httpc.Die("parlay crew-state: agent id required", config.ExitUsage)
		return
	}

	res := CrewStateForAgent(agentID)
	fmt.Printf("%s · source: %s · %s\n", res.State, res.Source, res.Detail)
}
