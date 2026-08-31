// Unit 5: supervise's event cursor (after_seq over the per-agent event log)
// behind PARLAY_CREW_READ_BEADS, and the two invariants the line-marker
// original declared but never tested (0 .supervise-marker files exist in
// production, so nothing had ever exercised them): enqueue-BEFORE-cursor-
// advance, and do-not-advance-on-relay-failure. Both are pinned here on the
// event path, and the enqueue ordering on the legacy path too.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/crewevents"
	"github.com/trillium/parlay/tools/cli/internal/identity"
)

// seqEnv wires a temp agent home with the read gate ON and a live relay
// capture, mirroring the legacy tests' env shape.
func seqEnv(t *testing.T, agentID string) (home string, bodies *[]map[string]any) {
	t.Helper()
	bodies = &[]map[string]any{}
	srv := newSuperviseServer(t, bodies)
	home = t.TempDir()
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_AGENT_ID", agentID)
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_UNATTENDED_FLAG", "")
	t.Setenv("PARLAY_CREW_READ_BEADS", "1")
	return home, bodies
}

func appendEvent(t *testing.T, agentID, verb, note string) crewevents.Event {
	t.Helper()
	ev, err := crewevents.Append(
		crewevents.File(filepath.Join(identity.AgentsRoot(), agentID)),
		crewevents.Event{At: "2026-08-31T00:00:00Z", Name: crewevents.EventCrewStatus, Agent: agentID, Verb: verb, Note: note},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestSuperviseSeqWakesOnTerminalEventAndSuppressesRewake(t *testing.T) {
	_, bodies := seqEnv(t, "agent-e1")
	appendEvent(t, "agent-e1", "working", "on it")
	done := appendEvent(t, "agent-e1", "done", "task complete")

	out := captureStdout(t, func() { Supervise([]string{"agent-e1"}) })
	if !strings.Contains(out, "supervisor woken") {
		t.Errorf("stdout = %q, want a wake line", out)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0]["text"].(string), "is done") {
		t.Fatalf("relay posts = %+v, want one 'is done' post", *bodies)
	}
	if got := readSeenSeq("agent-e1"); got != done.Seq {
		t.Errorf("cursor after wake = %d, want the surfaced event's seq %d", got, done.Seq)
	}

	captureStdout(t, func() { Supervise([]string{"agent-e1"}) })
	if len(*bodies) != 1 {
		t.Errorf("relay posts after second run = %d, want still 1 (seq cursor suppresses re-wake)", len(*bodies))
	}
}

// The scan surfaces the FIRST terminal event after the cursor, not the last —
// each captain-relevant transition gets its own wake, in order.
func TestSuperviseSeqSurfacesTerminalEventsInOrder(t *testing.T) {
	_, bodies := seqEnv(t, "agent-e2")
	appendEvent(t, "agent-e2", "done", "first stop")
	appendEvent(t, "agent-e2", "failed", "then this")

	captureStdout(t, func() { Supervise([]string{"agent-e2"}) })
	captureStdout(t, func() { Supervise([]string{"agent-e2"}) })
	if len(*bodies) != 2 {
		t.Fatalf("relay posts = %d, want 2 (one per terminal event)", len(*bodies))
	}
	if !strings.Contains((*bodies)[0]["text"].(string), "is done") || !strings.Contains((*bodies)[1]["text"].(string), "is failed") {
		t.Errorf("posts = %+v, want done then failed, in event order", *bodies)
	}
}

// Routine events are absorbed and the cursor does NOT advance past them —
// parity with the line scan, where the marker only moves when something is
// surfaced.
func TestSuperviseSeqAbsorbsRoutineWithoutAdvancing(t *testing.T) {
	_, bodies := seqEnv(t, "agent-e3")
	appendEvent(t, "agent-e3", "working", "still going")

	captureStdout(t, func() { Supervise([]string{"agent-e3"}) })
	if len(*bodies) != 0 {
		t.Errorf("relay posts = %d, want 0 (routine absorbed)", len(*bodies))
	}
	if _, err := os.Stat(seqMarkerFile("agent-e3")); !os.IsNotExist(err) {
		t.Errorf("seq cursor written on an absorbed pass — nothing was surfaced")
	}

	appendEvent(t, "agent-e3", "blocked", "waiting on ci")
	captureStdout(t, func() { Supervise([]string{"agent-e3"}) })
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0]["text"].(string), "is blocked") {
		t.Fatalf("posts = %+v, want the later terminal event to wake", *bodies)
	}
}

// FROZEN INVARIANT (do-not-advance-on-failure, supervise.go's robots-gxlb
// block): a failed relay post leaves the seq cursor un-advanced so the event
// is not lost, and the very same event re-fires once the relay is back.
func TestSuperviseSeqFailedPostDoesNotAdvanceCursor(t *testing.T) {
	_, bodies := seqEnv(t, "agent-e4")
	appendEvent(t, "agent-e4", "blocked", "waiting on ci")

	t.Setenv("PARLAY_SERVER", deadServerURL(t))
	code, exited := withExitTrap(t, func() {
		captureStdout(t, func() { Supervise([]string{"agent-e4"}) })
	})
	if !exited || code != 1 {
		t.Errorf("exit with a dead relay = (%d, %v), want (1, true)", code, exited)
	}
	if _, err := os.Stat(seqMarkerFile("agent-e4")); !os.IsNotExist(err) {
		t.Errorf("seq cursor advanced after a failed post — the event would be lost")
	}

	srv := newSuperviseServer(t, bodies)
	t.Setenv("PARLAY_SERVER", srv.URL)
	out := captureStdout(t, func() { Supervise([]string{"agent-e4"}) })
	if !strings.Contains(out, "supervisor woken") || len(*bodies) != 1 {
		t.Fatalf("after recovery: out=%q posts=%d, want the missed event to re-fire once", out, len(*bodies))
	}
}

// FROZEN INVARIANT (enqueue-before-cursor-advance): in unattended mode the
// durable queue write happens while the cursor still holds its OLD value, so
// a crash between the two duplicates the digest entry instead of losing it.
// Pinned by observing the cursor at the moment the enqueue seam fires.
func TestSuperviseSeqUnattendedEnqueuesBeforeAdvancing(t *testing.T) {
	_, bodies := seqEnv(t, "agent-e5")
	flagFile := filepath.Join(t.TempDir(), "away")
	if err := os.WriteFile(flagFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARLAY_UNATTENDED_FLAG", flagFile)
	ev := appendEvent(t, "agent-e5", "needs-decision", "pick a path")

	cursorAtEnqueue := uint64(999)
	prev := superviseEnqueue
	superviseEnqueue = func(agentID, verb, note string) {
		cursorAtEnqueue = readSeenSeq(agentID)
		prev(agentID, verb, note)
	}
	t.Cleanup(func() { superviseEnqueue = prev })

	captureStdout(t, func() { Supervise([]string{"agent-e5"}) })
	if len(*bodies) != 0 {
		t.Errorf("relay posts = %d, want 0 in unattended mode", len(*bodies))
	}
	if cursorAtEnqueue != 0 {
		t.Errorf("cursor already at %d when the enqueue fired — must still be 0 (enqueue BEFORE advance)", cursorAtEnqueue)
	}
	if got := readSeenSeq("agent-e5"); got != ev.Seq {
		t.Errorf("cursor after queued pass = %d, want %d", got, ev.Seq)
	}
	if q := ReadUnattendedQueue("agent-e5"); len(q) != 1 || q[0].Verb != "needs-decision" {
		t.Errorf("queue = %+v, want the one needs-decision entry", q)
	}
}

// The same ordering invariant on the LEGACY line-marker path (gate off) —
// the brief's "0 .supervise-marker files exist in production; the invariants
// are untested" gap.
func TestSuperviseLegacyUnattendedEnqueuesBeforeMarkerAdvance(t *testing.T) {
	home, _ := seqEnv(t, "agent-e6")
	t.Setenv("PARLAY_CREW_READ_BEADS", "")
	flagFile := filepath.Join(t.TempDir(), "away")
	if err := os.WriteFile(flagFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARLAY_UNATTENDED_FLAG", flagFile)
	writeStatus(t, home, "agent-e6", "failed: gave up\n")

	markerExistedAtEnqueue := true
	prev := superviseEnqueue
	superviseEnqueue = func(agentID, verb, note string) {
		_, err := os.Stat(markerFile(agentID))
		markerExistedAtEnqueue = !os.IsNotExist(err)
		prev(agentID, verb, note)
	}
	t.Cleanup(func() { superviseEnqueue = prev })

	captureStdout(t, func() { Supervise([]string{"agent-e6"}) })
	if markerExistedAtEnqueue {
		t.Errorf("line marker already advanced when the enqueue fired — must be enqueue BEFORE advance")
	}
	if q := ReadUnattendedQueue("agent-e6"); len(q) != 1 || q[0].Verb != "failed" {
		t.Errorf("queue = %+v, want the one failed entry", q)
	}
	if _, err := os.Stat(markerFile("agent-e6")); err != nil {
		t.Errorf("line marker not advanced after a successful queued pass: %v", err)
	}
}

// Gate on but no event log yet — the expected rollout shape (dual-write has
// never run for this agent): fall back to the legacy line scan, quietly.
func TestSuperviseGateOnWithoutEventLogFallsBackToStatusFile(t *testing.T) {
	home, bodies := seqEnv(t, "agent-e7")
	writeStatus(t, home, "agent-e7", "done: finished\n")

	out := captureStdout(t, func() { Supervise([]string{"agent-e7"}) })
	if !strings.Contains(out, "supervisor woken") || len(*bodies) != 1 {
		t.Fatalf("out=%q posts=%d, want a legacy-path wake", out, len(*bodies))
	}
	if _, err := os.Stat(markerFile("agent-e7")); err != nil {
		t.Errorf("legacy marker not advanced on the fallback path: %v", err)
	}
	if _, err := os.Stat(seqMarkerFile("agent-e7")); !os.IsNotExist(err) {
		t.Errorf("seq cursor written on the legacy fallback path")
	}
}

// Once the event log EXISTS, its answer is final — a "nothing new" from the
// events must not fall through to the line scan, whose cursor never advances
// after cutover and would re-surface every old line forever.
func TestSuperviseEventAnswerIsFinalNoLegacyRescan(t *testing.T) {
	home, bodies := seqEnv(t, "agent-e8")
	appendEvent(t, "agent-e8", "working", "on it")
	writeStatus(t, home, "agent-e8", "done: stale line from before cutover\n")

	captureStdout(t, func() { Supervise([]string{"agent-e8"}) })
	if len(*bodies) != 0 {
		t.Errorf("relay posts = %d, want 0 — the event log answered 'nothing new'; the line scan must not run", len(*bodies))
	}
}

// Gate off: the event log is invisible, byte-identical legacy behavior.
func TestSuperviseGateOffIgnoresEventLog(t *testing.T) {
	home, bodies := seqEnv(t, "agent-e9")
	t.Setenv("PARLAY_CREW_READ_BEADS", "")
	appendEvent(t, "agent-e9", "done", "only in the event log")
	writeStatus(t, home, "agent-e9", "working: on it\n")

	captureStdout(t, func() { Supervise([]string{"agent-e9"}) })
	if len(*bodies) != 0 {
		t.Errorf("relay posts = %d, want 0 (gate off: only the status file speaks, and it says working)", len(*bodies))
	}
	if _, err := os.Stat(seqMarkerFile("agent-e9")); !os.IsNotExist(err) {
		t.Errorf("seq cursor written with the gate off")
	}
}

// A lost/garbage cursor reads as 0 — the safe direction (re-surface, never
// drop).
func TestSuperviseSeqGarbageCursorReadsAsZero(t *testing.T) {
	seqEnv(t, "agent-ea")
	if err := os.MkdirAll(filepath.Join(identity.AgentsRoot(), "agent-ea"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seqMarkerFile("agent-ea"), []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readSeenSeq("agent-ea"); got != 0 {
		t.Errorf("readSeenSeq(garbage) = %d, want 0", got)
	}
}
