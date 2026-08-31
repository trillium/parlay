// Fold §3.6 + §6 Slice 3 — parlay supervise primitive. A
// wake-on-actionable-status loop that ports fm-watch.sh's
// absorb-when-provably-working logic. Terminal verbs (done, needs-decision,
// blocked, failed) wake immediately and escalate to the captain. Routine
// verbs are absorbed unless a new actionable (terminal) line appears.
//
// Unattended (headless) mode per §3.6.2: presence gate (env flag),
// enqueue-before-suppress durable queue (unattended_queue.go; batch window +
// max-defer daemon deferred to a separate pass, same as the TS original), an
// in-band captain-return sentinel.
//
// Ported from packages/cli/src/commands-supervise.ts.
package commands

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/crewevents"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
)

// terminalStatusVerbs are captain-relevant: they wake the supervisor
// immediately. Everything else is routine and absorbed unless a wedge is
// detected (not yet implemented — see the TS original's own TODO).
var terminalStatusVerbs = map[string]bool{"done": true, "needs-decision": true, "blocked": true, "failed": true}

// daemonMarker flags daemon-authored relay messages (fold §3.6.2, in-band
// captain-return sentinel) — an ASCII unit separator a human never types.
const daemonMarker = "\x1f"

// escalateBatchSecs / maxDeferSecs are unattended-mode window constants
// ported verbatim from the TS original, which declares but never uses them
// either — placeholders for the batch-window + max-defer daemon its header
// comment calls out as "deferred to a separate pass".
const (
	escalateBatchSecs = 90
	maxDeferSecs      = 300
)

func markerFile(agentID string) string {
	return filepath.Join(identity.AgentsRoot(), agentID, ".supervise-marker")
}

// seqMarkerFile is the unit-5 cursor over the per-agent event log
// (crewevents): the last event Seq already surfaced to the supervisor. It
// lives ALONGSIDE the legacy line-number marker, never in it — the two
// cursors index different sequences, and a migration (unit 7) seeds this one
// explicitly.
func seqMarkerFile(agentID string) string {
	return filepath.Join(identity.AgentsRoot(), agentID, ".supervise-seq")
}

// readSeenSeq reads the event cursor: the highest Seq already surfaced.
// Missing/empty/garbage reads as 0 — ReadAfter's exclusive cursor semantics
// make 0 mean "from the beginning", which is also the safe direction (a lost
// cursor re-surfaces events rather than dropping them).
func readSeenSeq(agentID string) uint64 {
	data, err := os.ReadFile(seqMarkerFile(agentID))
	if err != nil {
		return 0
	}
	lines := nonEmptyLines(string(data))
	if len(lines) == 0 {
		return 0
	}
	n, err := strconv.ParseUint(strings.TrimSpace(lines[len(lines)-1]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// writeSeenSeq appends a new cursor entry (append-not-truncate, matching the
// legacy marker's audit-trail shape). Best-effort like writeSeenMarker: a
// failed advance re-surfaces the event next call, which is the safe failure.
func writeSeenSeq(agentID string, seq uint64) {
	dir := filepath.Join(identity.AgentsRoot(), agentID)
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(seqMarkerFile(agentID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%d\n", seq)
}

type seenMarker struct {
	lastLine int
	lastHash string
}

// readSeenMarker reads the suppression marker recording which status lines
// have already been surfaced to the supervisor.
func readSeenMarker(agentID string) seenMarker {
	data, err := os.ReadFile(markerFile(agentID))
	if err != nil {
		return seenMarker{lastLine: -1}
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return seenMarker{lastLine: -1}
	}
	lines := strings.Split(content, "\n")
	last := lines[len(lines)-1]
	parts := strings.SplitN(last, "|", 2)
	lineNum, err := strconv.Atoi(parts[0])
	if err != nil {
		lineNum = -1
	}
	hash := ""
	if len(parts) > 1 {
		hash = parts[1]
	}
	return seenMarker{lastLine: lineNum, lastHash: hash}
}

// writeSeenMarker appends a new suppression-marker entry.
func writeSeenMarker(agentID string, lineNum int, hash string) {
	dir := filepath.Join(identity.AgentsRoot(), agentID)
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(markerFile(agentID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%d|%s\n", lineNum, hash)
}

// hashLine computes a short, portable dedup hash of a status line: the
// first 8 chars of its base64 encoding.
func hashLine(line string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(line))
	if len(enc) > 8 {
		return enc[:8]
	}
	return enc
}

// readAllStatusLines reads every non-blank line from statusFile.
func readAllStatusLines(statusFile string) []string {
	data, err := os.ReadFile(statusFile)
	if err != nil {
		return nil
	}
	return nonEmptyLines(string(data))
}

type actionableStatus struct {
	line      string
	lineIndex int
	parsed    parsedStatus
}

// findNewActionable scans statusFile for the first terminal-verb line after
// the seen marker. Routine lines are skipped (absorbed); a routine line
// whose hash matches the last-seen one is explicitly recognized as
// unchanged, but either way nothing short-circuits the scan for a later
// terminal line — matching the TS original's loop exactly.
func findNewActionable(agentID, statusFile string) (actionableStatus, bool) {
	allLines := readAllStatusLines(statusFile)
	if len(allLines) == 0 {
		return actionableStatus{}, false
	}

	seen := readSeenMarker(agentID)

	for i := seen.lastLine + 1; i < len(allLines); i++ {
		line := allLines[i]
		parsed, ok := parseStatusLine(line)
		if !ok {
			continue
		}

		if terminalStatusVerbs[parsed.verb] {
			return actionableStatus{line: line, lineIndex: i, parsed: parsed}, true
		}

		// Routine verb: same content as last time → absorbed, otherwise
		// still absorbed for now (full wedge detection is a refinement the
		// TS original defers).
	}

	return actionableStatus{}, false
}

// findNewActionableEvent is the unit-5 event-cursor scan: the first
// terminal-verb parlay.crew.status event with Seq > the seen cursor. Routine
// verbs are absorbed exactly as in the line scan. The third return
// ("answered") is the fallback seam: false means the event log could not
// speak for this agent — absent (the expected rollout shape: no dual-write
// has run here yet) or unreadable (noted on stderr) — and the caller must
// fall back to the legacy line scan. It is NOT an error verdict.
func findNewActionableEvent(agentID string) (actionableStatus, uint64, bool, bool) {
	evFile := crewevents.File(filepath.Join(identity.AgentsRoot(), agentID))
	if _, err := os.Stat(evFile); err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "supervise %s: note — event log unreadable, falling back to the status file: %v\n", agentID, err)
		}
		return actionableStatus{}, 0, false, false
	}
	evs, skipped, err := crewevents.ReadAfter(evFile, readSeenSeq(agentID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervise %s: note — event log unreadable, falling back to the status file: %v\n", agentID, err)
		return actionableStatus{}, 0, false, false
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "supervise %s: note — %d unparseable event line(s) skipped\n", agentID, skipped)
	}
	for _, ev := range evs {
		if ev.Name != crewevents.EventCrewStatus {
			continue
		}
		if terminalStatusVerbs[ev.Verb] {
			return actionableStatus{
				line:   ev.Verb + ": " + ev.Note,
				parsed: parsedStatus{verb: ev.Verb, key: ev.Key, note: ev.Note},
			}, ev.Seq, true, true
		}
		// Routine verb: absorbed, scan continues for a later terminal event.
	}
	return actionableStatus{}, 0, false, true
}

// superviseFindActionable resolves the scan per the unit-4/5 read gate
// (PARLAY_CREW_READ_BEADS=1): event cursor when the gate is on and the
// per-agent event log exists, legacy line cursor otherwise. Returns the
// actionable status plus the ADVANCE closure for whichever cursor found it —
// the caller decides WHEN to advance (only after the event is safely
// delivered or queued), this decides WHICH cursor moves. When the event log
// answered "nothing new", that answer is final: no legacy re-scan, because
// the line cursor never advances once reads cut over and would re-surface
// every old line.
func superviseFindActionable(agentID, statusFile string) (actionableStatus, func(), bool) {
	if crewReadBeads() {
		if act, seq, found, answered := findNewActionableEvent(agentID); answered {
			return act, func() { writeSeenSeq(agentID, seq) }, found
		}
	}
	act, ok := findNewActionable(agentID, statusFile)
	return act, func() { writeSeenMarker(agentID, act.lineIndex, hashLine(act.line)) }, ok
}

// superviseEnqueue is the unattended-queue seam, a var so tests can pin the
// enqueue-BEFORE-cursor-advance ordering (crash safety: a crash between the
// two duplicates the digest entry rather than losing it).
var superviseEnqueue = EnqueueUnattended

// postToRelay posts a message to agentID's channel. Non-fatal: failures are
// warned to stderr, not fatal — supervise must keep running when the relay
// is down.
func postToRelay(agentID, text string) bool {
	ok, reason := httpc.TryPostJSON("/api/chat/message", map[string]any{
		"channel": agentID,
		"role":    "agent",
		"text":    text,
	}, httpc.DefaultTimeout)
	if !ok {
		fmt.Fprintf(os.Stderr, "warn: failed to post to relay — %s\n", reason)
	}
	return ok
}

// isUnattended reports whether PARLAY_UNATTENDED_FLAG names a file that
// exists — the away-mode presence gate (fold §3.6.2), mirroring the
// $PARLAY_STATUS_FILE indirection.
func isUnattended() bool {
	flag := os.Getenv("PARLAY_UNATTENDED_FLAG")
	if flag == "" {
		return false
	}
	_, err := os.Stat(flag)
	return err == nil
}

// Supervise ports cmdSupervise.
//
// Fidelity fix (follow-up to ticket B5 — extends the same fix already
// applied to crew-state, see status_verb.go's statusFileForAgent doc): the
// TS original resolved its status file via the CALLING process's own
// PARLAY_AGENT_ID/PARLAY_STATUS_FILE rather than from the agentID argument,
// so `parlay supervise <id>` never actually watched <id>'s status file when
// invoked by something other than the target agent itself. Fixed here:
// always resolve directly from agentID via statusFileForAgent, matching
// everything else keyed by agentID (the marker file, the unattended queue).
func Supervise(argv []string) {
	if helpWanted("supervise", argv) {
		return
	}
	r := args.Parse("supervise", argv, []string{"--drain"}, nil)

	agentID := ""
	if len(r.Positionals) > 0 {
		agentID = strings.TrimSpace(r.Positionals[0])
	}
	if agentID == "" {
		httpc.Die("parlay supervise: agent id required", config.ExitUsage)
		return
	}

	statusFile := statusFileForAgent(agentID)

	if r.Bool("--drain") {
		buffered := DrainUnattendedQueue(agentID)
		if len(buffered) == 0 {
			fmt.Printf("supervise %s --drain: no buffered events\n", agentID)
			return
		}
		parts := make([]string, len(buffered))
		for i, e := range buffered {
			if e.Detail != "" {
				parts[i] = fmt.Sprintf("%s: %s", e.Verb, e.Detail)
			} else {
				parts[i] = e.Verb
			}
		}
		message := fmt.Sprintf("%screw: %s away-mode digest — %s", daemonMarker, agentID, strings.Join(parts, "; "))
		// Exit non-zero, not 0-with-an-error-line: the queue is retained and
		// nothing was delivered, and a caller that can only see the exit code
		// must be able to tell that apart from a real drain (robots-gxlb).
		if !postToRelay(agentID, message) {
			httpc.Die(
				fmt.Sprintf("error: failed to deliver %d buffered event(s) for %s; queue retained", len(buffered), agentID),
				config.ExitRuntime,
			)
			return
		}
		ClearUnattendedQueue(agentID)
		fmt.Printf("drained %d buffered event(s) for %s\n", len(buffered), agentID)
		return
	}

	act, advance, ok := superviseFindActionable(agentID, statusFile)
	if !ok {
		fmt.Fprintf(os.Stderr, "supervise %s: no new actionable status (routine verbs absorbed)\n", agentID)
		return
	}

	detail := ""
	if act.parsed.note != "" {
		detail = " — " + act.parsed.note
	}

	if isUnattended() {
		// Enqueue BEFORE advancing the cursor — crash safety (see
		// EnqueueUnattended's doc). This ordering is frozen across both
		// cursors (line marker and event seq).
		superviseEnqueue(agentID, act.parsed.verb, act.parsed.note)
		fmt.Fprintf(os.Stderr, "supervise %s: unattended mode, queued %s%s\n", agentID, act.parsed.verb, detail)
		advance()
		return
	}

	message := fmt.Sprintf("%screw: %s is %s%s", daemonMarker, agentID, act.parsed.verb, detail)

	// A failed post must not print the success-shaped "supervisor woken:"
	// line, and must not exit 0. The cursor is deliberately left un-advanced
	// so the event is not lost — which means this exact line re-fires on the
	// next call, so a caller reading stdout would have looped forever on a
	// down relay while the exit code said everything was fine (robots-gxlb).
	// Frozen across both cursors: do-not-advance-on-failure.
	if !postToRelay(agentID, message) {
		httpc.Die(
			fmt.Sprintf("supervise %s: supervisor NOT woken (%s%s) — relay post failed; marker not advanced, retry when the relay is back", agentID, act.parsed.verb, detail),
			config.ExitRuntime,
		)
		return
	}

	fmt.Printf("supervisor woken: %s %s%s\n", agentID, act.parsed.verb, detail)

	// Mark the event seen only after a successful post, to prevent event
	// loss on relay failure.
	advance()
}
