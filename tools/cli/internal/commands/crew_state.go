// Fold §3.6 + §6 Slice 3 — parlay crew-state reader. Reconciles an agent's
// last keyed status line against parlay's oracle: for now that oracle is
// relay-enrollment presence; run attribution and pane (tab) liveness are
// extension points the TS original stubs out but never implements (see its
// own header comment) — this port stops at the same point.
//
// crew-state is the supervision oracle: a supervisor polling it decides
// whether an agent is alive, so a false "dead" is the expensive failure
// (recovery/teardown run against healthy work), and a transient answer that
// masks a real terminal state is nearly as bad (a poll-based supervisor
// never sees that transition again). Two rules follow, and robots-me7m
// exists because the original violated both: never report "not enrolled"
// from a lookup that merely FAILED, and never report "unknown" when a
// stale-but-valid status line is sitting on disk.
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
// Fidelity fix (follow-up to ticket B5): the TS original's \w does not
// include '-', so a hyphenated verb like "needs-decision" or "captain-held"
// never matched this pattern — despite both being in the code's own
// declared TERMINAL_VERBS/ROUTINE_VERBS vocabulary (knownStatusVerbs /
// terminalStatusVerbs below) — and silently read back as unparseable in
// both crew-state and supervise. Fixed here: the verb class now accepts
// hyphens.
var statusLineRe = regexp.MustCompile(`^([\w-]+)(?:\s*\[key=([A-Za-z0-9._-]+)\])?\s*:\s*(.*)$`)

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
// lines. Shared by crew-state's readStatusFor and supervise's
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

// Exit codes crew-state adds on top of the CLI-wide 0/1/2 (config.ExitOK /
// ExitRuntime / ExitUsage), following context_check.go's ExitRotate
// precedent of a command-local advisory code. A supervisor must be able to
// tell "no news" from "gone" from "I couldn't ask" WITHOUT string-matching
// the detail text (robots-me7m):
//
//	0  a state was determined — including a stale-but-valid one read back
//	   from disk while the relay was unreachable
//	3  enrolled, but nothing usable on disk yet ("no news")
//	4  the relay answered and does NOT list this agent ("gone")
//	5  a status file exists but cannot be read or parsed
//	6  the relay lookup failed AND there is no status to fall back on
//	   ("couldn't ask") — the ONLY code that means crew-state has no opinion
const (
	ExitCrewNoStatus         = 3
	ExitCrewNotEnrolled      = 4
	ExitCrewStatusUnreadable = 5
	ExitCrewRelayUnreachable = 6
)

// enrollment is the relay's answer about an agent's registry presence. The
// third case is the point of the type: "the relay did not answer" is NOT
// "the relay says no" (robots-me7m).
type enrollment int

const (
	enrolledYes enrollment = iota
	enrolledNo
	enrollmentUnknown
)

// relayLookupAttempts / relayLookupBackoff bound the retry on a failed
// subscribers read. The observed failure was load-dependent (43 concurrent
// `parlay listen`/`parlay monitor` processes) and cleared on an immediate
// retry, so a couple of cheap retries absorb the contention before
// crew-state has to report a degraded answer at all.
const (
	relayLookupAttempts = 3
	relayLookupBackoff  = 250 * time.Millisecond
	relayLookupTimeout  = 3 * time.Second
)

// sleep is time.Sleep, overridable in tests so the retry backoff costs
// nothing there.
var sleep = time.Sleep

// agentEnrollment asks the relay whether agentID is in the agent registry,
// distinguishing "not registered" from "could not ask".
//
// robots-me7m: this used to be isAgentSubscribed() bool, collapsing a failed
// lookup (timeout, non-2xx, undecodable body) into "not subscribed" — so a
// transient relay hiccup made a live, registered agent with a valid status
// file report as "unknown · source: none · agent not enrolled with relay",
// which reads as dead to any supervisor polling this command. Retries first,
// and only reports enrolledNo when the relay actually answered.
func agentEnrollment(agentID string) enrollment {
	reg, ok := fetchRegisteredAgents()
	return enrollmentOf(reg, ok, agentID)
}

// fetchRegisteredAgents reads the relay's whole registry once (with the
// retry/backoff above). ok=false means the relay could not be asked; ok=true
// with an empty set is a real "nobody is registered" answer.
//
// robots-8783: sweep used to call agentEnrollment per candidate — 3 attempts
// × 3s timeout each, serially, so a dead relay cost ~9.5s × ~254 agents
// (>120s wall clock, sometimes far more). The registry answer does not vary
// by agent, so batch callers fetch it once and share it across the pass.
func fetchRegisteredAgents() (map[string]bool, bool) {
	for attempt := 0; attempt < relayLookupAttempts; attempt++ {
		if attempt > 0 {
			sleep(relayLookupBackoff)
		}
		subs, ok := httpc.TryGetJSON[wire.SubscribersInfo]("/api/chat/subscribers", relayLookupTimeout)
		if !ok {
			continue
		}
		// A 2xx with no registered block is a real answer ("nobody is
		// registered"), not a failed lookup.
		reg := map[string]bool{}
		if subs.Registered != nil {
			for _, a := range subs.Registered.Agents {
				reg[a.ID] = true
			}
		}
		return reg, true
	}
	return nil, false
}

// enrollmentOf maps one agent's presence in a fetched registry snapshot onto
// the three-way enrollment answer. Pure; the robots-me7m distinction lives in
// ok: a failed fetch is enrollmentUnknown, never enrolledNo.
func enrollmentOf(reg map[string]bool, ok bool, agentID string) enrollment {
	if !ok {
		return enrollmentUnknown
	}
	if reg[agentID] {
		return enrolledYes
	}
	return enrolledNo
}

// CrewStateResult is the reconciled {state, source, detail} triple crew-state
// prints as "<state> · source: <src> · <detail>", plus the process exit code
// that condition maps to (see the Exit* constants above).
type CrewStateResult struct {
	State, Source, Detail string
	ExitCode              int
}

// statusRead is the on-disk half of the reconciliation, kept separate from
// the relay half so each of the three distinct failure modes the ticket
// calls out — nothing recorded, unreadable/unparseable, present-and-valid —
// gets its own message instead of one conflated "unknown".
type statusRead struct {
	status parsedStatus
	// kind is one of: "ok", "absent" (no file / empty file),
	// "unreadable" (file exists but I/O failed), "unparseable" (a last
	// line that isn't firstmate's grammar).
	kind   string
	detail string
}

func readStatusFor(statusFile string) statusRead {
	data, err := os.ReadFile(statusFile)
	if err != nil {
		if os.IsNotExist(err) {
			return statusRead{kind: "absent"}
		}
		return statusRead{kind: "unreadable", detail: fmt.Sprintf("status file unreadable: %v", err)}
	}
	lines := nonEmptyLines(string(data))
	if len(lines) == 0 {
		return statusRead{kind: "absent"}
	}
	last := lines[len(lines)-1]
	p, ok := parseStatusLine(last)
	if !ok {
		return statusRead{kind: "unparseable", detail: fmt.Sprintf("status line unparseable: %s", strings.TrimSpace(last))}
	}
	return statusRead{status: p, kind: "ok"}
}

// noteOf renders a parsed status line's note for the detail column.
func noteOf(p parsedStatus) string {
	if p.note == "" {
		return "(no detail)"
	}
	return p.note
}

// CrewStateForAgent reconciles agentID's last keyed status line against
// parlay's oracle (relay-registry presence). Precedence, per robots-me7m's
// fix direction:
//
//  1. The relay is asked first, but its answer only decides "gone" when it
//     actually answered. A failed lookup NEVER produces "not enrolled".
//  2. The on-disk status file is the durable record and is always consulted:
//     a stale-but-valid status beats "unknown" in every case, because a
//     supervisor acting on stale-working is safe while one acting on
//     false-dead is not.
//  3. "nothing recorded", "unreadable/unparseable", and "not registered with
//     the relay" are three distinct conditions with distinct details and
//     distinct exit codes — only the last means the agent is unreachable.
//
// Fidelity fix (ticket B5, pre-approved — see status_verb.go's
// statusFileForAgent doc): resolves agentID's status file directly instead
// of the caller's own env-derived one.
func CrewStateForAgent(agentID string) CrewStateResult {
	return crewStateForAgentEnrolled(agentID, agentEnrollment(agentID))
}

// crewStateForAgentEnrolled is CrewStateForAgent with the relay half already
// answered — the seam batch callers (sweep) use to share one registry fetch
// across a whole pass instead of re-asking the relay per agent (robots-8783).
func crewStateForAgentEnrolled(agentID string, enrolled enrollment) CrewStateResult {
	sr := readStatusFor(statusFileForAgent(agentID))

	// Source suffix records HOW much to trust the status line: plain
	// "status" when the relay confirmed enrollment, qualified when the relay
	// disagreed or couldn't be reached.
	source := "status"
	exit := config.ExitOK
	suffix := ""
	switch enrolled {
	case enrolledNo:
		source = "status-unenrolled"
		exit = ExitCrewNotEnrolled
		suffix = " (relay does not list this agent)"
	case enrollmentUnknown:
		source = "status-degraded"
		suffix = " (relay unreachable; status may be stale)"
	}

	switch sr.kind {
	case "ok":
		if !knownStatusVerbs[sr.status.verb] {
			return CrewStateResult{
				State:    "unknown",
				Source:   source,
				Detail:   fmt.Sprintf("unrecognized verb: %s", sr.status.verb),
				ExitCode: ExitCrewStatusUnreadable,
			}
		}
		// A valid status line always wins the state, even when the relay
		// says the agent is gone or could not be reached — never return
		// "unknown" over a usable record.
		return CrewStateResult{State: sr.status.verb, Source: source, Detail: noteOf(sr.status) + suffix, ExitCode: exit}

	case "unreadable", "unparseable":
		return CrewStateResult{State: "unknown", Source: source, Detail: sr.detail + suffix, ExitCode: ExitCrewStatusUnreadable}
	}

	// No status on disk: the relay's answer is all there is.
	switch enrolled {
	case enrolledNo:
		return CrewStateResult{State: "unknown", Source: "none", Detail: "agent not registered with relay", ExitCode: ExitCrewNotEnrolled}
	case enrollmentUnknown:
		return CrewStateResult{State: "unknown", Source: "none", Detail: "relay unreachable and no status recorded", ExitCode: ExitCrewRelayUnreachable}
	}
	return CrewStateResult{State: "unknown", Source: "none", Detail: "no status recorded", ExitCode: ExitCrewNoStatus}
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
	if res.ExitCode != config.ExitOK {
		httpc.Exit(res.ExitCode)
	}
}
