// Unit 4: the frozen wire contract, pinned cell by cell, and the bead-backed
// status read behind PARLAY_CREW_READ_BEADS.
//
// reconcileCrewState is what `parlay sweep`/`stale`/firstmate consume
// (status-lift report §6.3); its four hold-guard-feeding exit codes and
// source suffixes each came from a real incident. The table test below is
// deliberately exhaustive — every (read kind × enrollment) cell, exact
// strings, exact codes — so a unit-4+ refactor that shifts one cell fails
// loudly instead of teaching sweep to collect a live agent.
package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/parlaybeads"
)

// The numeric values are the contract — supervisors switch on them.
func TestCrewExitCodesAreFrozen(t *testing.T) {
	if ExitCrewNoStatus != 3 || ExitCrewNotEnrolled != 4 || ExitCrewStatusUnreadable != 5 || ExitCrewRelayUnreachable != 6 {
		t.Fatalf("frozen exit codes moved: 3/4/5/6, got %d/%d/%d/%d",
			ExitCrewNoStatus, ExitCrewNotEnrolled, ExitCrewStatusUnreadable, ExitCrewRelayUnreachable)
	}
}

func TestRelayRetryConstantsAreFrozen(t *testing.T) {
	if relayLookupAttempts != 3 || relayLookupBackoff != 250*time.Millisecond || relayLookupTimeout != 3*time.Second {
		t.Fatalf("frozen relay retry constants moved: got attempts=%d backoff=%v timeout=%v",
			relayLookupAttempts, relayLookupBackoff, relayLookupTimeout)
	}
}

func TestReconcileCrewStateFrozenTable(t *testing.T) {
	okRead := statusRead{kind: "ok", status: parsedStatus{verb: "working", note: "porting"}}
	okNoNote := statusRead{kind: "ok", status: parsedStatus{verb: "done"}}
	badVerb := statusRead{kind: "ok", status: parsedStatus{verb: "sprinting", note: "??"}}
	unreadable := statusRead{kind: "unreadable", detail: "status file unreadable: permission denied"}
	unparseable := statusRead{kind: "unparseable", detail: "status line unparseable: ???"}
	absent := statusRead{kind: "absent"}

	cases := []struct {
		name     string
		sr       statusRead
		enrolled enrollment
		want     CrewStateResult
	}{
		// A valid status line always wins the state — in all three
		// enrollment columns, including "the relay says gone".
		{"ok/enrolled", okRead, enrolledYes,
			CrewStateResult{State: "working", Source: "status", Detail: "porting", ExitCode: 0}},
		{"ok/unenrolled", okRead, enrolledNo,
			CrewStateResult{State: "working", Source: "status-unenrolled", Detail: "porting (relay does not list this agent)", ExitCode: 4}},
		{"ok/relay-unknown", okRead, enrollmentUnknown,
			CrewStateResult{State: "working", Source: "status-degraded", Detail: "porting (relay unreachable; status may be stale)", ExitCode: 0}},
		{"ok-no-note/enrolled", okNoNote, enrolledYes,
			CrewStateResult{State: "done", Source: "status", Detail: "(no detail)", ExitCode: 0}},

		// An unrecognized verb is exit 5 in every column; the source still
		// records the relay's answer.
		{"badverb/enrolled", badVerb, enrolledYes,
			CrewStateResult{State: "unknown", Source: "status", Detail: "unrecognized verb: sprinting", ExitCode: 5}},
		{"badverb/unenrolled", badVerb, enrolledNo,
			CrewStateResult{State: "unknown", Source: "status-unenrolled", Detail: "unrecognized verb: sprinting", ExitCode: 5}},
		{"badverb/relay-unknown", badVerb, enrollmentUnknown,
			CrewStateResult{State: "unknown", Source: "status-degraded", Detail: "unrecognized verb: sprinting", ExitCode: 5}},

		{"unreadable/enrolled", unreadable, enrolledYes,
			CrewStateResult{State: "unknown", Source: "status", Detail: "status file unreadable: permission denied", ExitCode: 5}},
		{"unreadable/unenrolled", unreadable, enrolledNo,
			CrewStateResult{State: "unknown", Source: "status-unenrolled", Detail: "status file unreadable: permission denied (relay does not list this agent)", ExitCode: 5}},
		{"unparseable/relay-unknown", unparseable, enrollmentUnknown,
			CrewStateResult{State: "unknown", Source: "status-degraded", Detail: "status line unparseable: ??? (relay unreachable; status may be stale)", ExitCode: 5}},

		// Nothing on disk: the relay's answer is all there is, and the three
		// answers get three distinct codes — "no news" (3) vs "gone" (4) vs
		// "couldn't ask" (6, the only code meaning crew-state has no opinion).
		{"absent/enrolled", absent, enrolledYes,
			CrewStateResult{State: "unknown", Source: "none", Detail: "no status recorded", ExitCode: 3}},
		{"absent/unenrolled", absent, enrolledNo,
			CrewStateResult{State: "unknown", Source: "none", Detail: "agent not registered with relay", ExitCode: 4}},
		{"absent/relay-unknown", absent, enrollmentUnknown,
			CrewStateResult{State: "unknown", Source: "none", Detail: "relay unreachable and no status recorded", ExitCode: 6}},
	}
	for _, tc := range cases {
		if got := reconcileCrewState(tc.sr, tc.enrolled); got != tc.want {
			t.Errorf("%s:\n got  %+v\n want %+v", tc.name, got, tc.want)
		}
	}
}

// --- the bead-backed read (PARLAY_CREW_READ_BEADS) --------------------------

// stubCrewOpenRead swaps the reader-side store seam and counts calls.
func stubCrewOpenRead(t *testing.T, c parlaybeads.Client, err error) *int {
	t.Helper()
	calls := new(int)
	prev := crewStoreOpenRead
	crewStoreOpenRead = func(context.Context, string) (parlaybeads.Client, error) {
		*calls++
		return c, err
	}
	t.Cleanup(func() { crewStoreOpenRead = prev })
	return calls
}

// readGatedAgent wires a temp agent home with BOTH gates on and a status
// file already holding fileLine (so tests can prove which source won).
func readGatedAgent(t *testing.T, agent, fileLine string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_CREW_STORE", filepath.Join(t.TempDir(), "store"))
	t.Setenv("PARLAY_CREW_READ_BEADS", "1")
	if fileLine != "" {
		dir := filepath.Join(home, agent)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(fileLine), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// seedFakeCrewBead plants a crew bead for agent in the fake client.
func seedFakeCrewBead(t *testing.T, f *fakeCrewClient, agent string, meta map[string]string) {
	t.Helper()
	m := map[string]string{parlaybeads.KeyAgentID: agent}
	for k, v := range meta {
		m[k] = v
	}
	if _, err := f.Create(context.Background(), parlaybeads.Bead{
		Assignee: agent, Labels: []string{parlaybeads.LabelCrew}, Metadata: m,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBeadBackedReadWinsOverTheFile(t *testing.T) {
	readGatedAgent(t, "dw-agent", "working: from the file\n")
	fake := newFakeCrewClient()
	seedFakeCrewBead(t, fake, "dw-agent", map[string]string{
		parlaybeads.KeyStatusVerb: "blocked",
		parlaybeads.KeyStatusKey:  "api-shape",
		parlaybeads.KeyStatusNote: "from the bead",
	})
	stubCrewOpenRead(t, fake, nil)

	got := crewStateForAgentEnrolled("dw-agent", enrolledYes)
	want := CrewStateResult{State: "blocked", Source: "status", Detail: "from the bead", ExitCode: 0}
	if got != want {
		t.Errorf("got %+v, want the bead's answer %+v", got, want)
	}
}

// The frozen contract survives the source swap: the same wire shape comes
// out whether the status was read from the file or the bead.
func TestBeadBackedReadKeepsTheFrozenWireShape(t *testing.T) {
	readGatedAgent(t, "dw-agent", "")
	fake := newFakeCrewClient()
	seedFakeCrewBead(t, fake, "dw-agent", map[string]string{parlaybeads.KeyStatusVerb: "working", parlaybeads.KeyStatusNote: "n"})
	stubCrewOpenRead(t, fake, nil)

	if got := crewStateForAgentEnrolled("dw-agent", enrolledNo); got.Source != "status-unenrolled" || got.ExitCode != 4 || got.State != "working" {
		t.Errorf("unenrolled column shifted under the bead read: %+v", got)
	}
	if got := crewStateForAgentEnrolled("dw-agent", enrollmentUnknown); got.Source != "status-degraded" || got.ExitCode != 0 {
		t.Errorf("degraded column shifted under the bead read: %+v", got)
	}
}

func TestBeadBackedUnrecognizedVerbIsExitFive(t *testing.T) {
	readGatedAgent(t, "dw-agent", "")
	fake := newFakeCrewClient()
	seedFakeCrewBead(t, fake, "dw-agent", map[string]string{parlaybeads.KeyStatusVerb: "sprinting"})
	stubCrewOpenRead(t, fake, nil)

	got := crewStateForAgentEnrolled("dw-agent", enrolledYes)
	if got.State != "unknown" || got.ExitCode != ExitCrewStatusUnreadable || got.Detail != "unrecognized verb: sprinting" {
		t.Errorf("got %+v, want the exit-5 unrecognized-verb cell", got)
	}
}

// A store failure is never a state verdict: fall back to the file (which
// dual-write keeps truthful) and say so on stderr. Never manufacture
// unknown/dead out of an unreachable store.
func TestStoreFailureFallsBackToFileWithANote(t *testing.T) {
	readGatedAgent(t, "dw-agent", "working: still here\n")
	stubCrewOpenRead(t, nil, errors.New("dolt is on fire"))

	var got CrewStateResult
	errOut := captureStderr(t, func() {
		got = crewStateForAgentEnrolled("dw-agent", enrolledYes)
	})
	want := CrewStateResult{State: "working", Source: "status", Detail: "still here", ExitCode: 0}
	if got != want {
		t.Errorf("got %+v, want the file's answer %+v", got, want)
	}
	if !strings.Contains(errOut, "falling back to the status file") {
		t.Errorf("a store failure must be noted, not absorbed; stderr = %q", errOut)
	}
}

// No crew bead yet is the expected rollout shape — quiet fallback.
func TestNoBeadFallsBackToFileQuietly(t *testing.T) {
	readGatedAgent(t, "dw-agent", "paused: lunch\n")
	stubCrewOpenRead(t, newFakeCrewClient(), nil)

	var got CrewStateResult
	errOut := captureStderr(t, func() {
		got = crewStateForAgentEnrolled("dw-agent", enrolledYes)
	})
	if got.State != "paused" || got.ExitCode != 0 {
		t.Errorf("got %+v, want the file's paused", got)
	}
	if errOut != "" {
		t.Errorf("an absent bead is expected during rollout, not noteworthy; stderr = %q", errOut)
	}
}

// An attach-only bead (gc_session pointer, no status write yet) has no
// opinion either.
func TestAttachOnlyBeadFallsBackToFile(t *testing.T) {
	readGatedAgent(t, "dw-agent", "working: from the file\n")
	fake := newFakeCrewClient()
	seedFakeCrewBead(t, fake, "dw-agent", map[string]string{parlaybeads.KeyGCSession: "gc-0042"})
	stubCrewOpenRead(t, fake, nil)

	if got := crewStateForAgentEnrolled("dw-agent", enrolledYes); got.State != "working" || got.Detail != "from the file" {
		t.Errorf("got %+v, want the file's answer over an attach-only bead", got)
	}
}

func TestReadGateOffNeverOpensTheStore(t *testing.T) {
	readGatedAgent(t, "dw-agent", "working: legacy\n")
	t.Setenv("PARLAY_CREW_READ_BEADS", "")
	calls := stubCrewOpenRead(t, newFakeCrewClient(), nil)

	if got := crewStateForAgentEnrolled("dw-agent", enrolledYes); got.State != "working" {
		t.Errorf("got %+v", got)
	}
	if *calls != 0 {
		t.Errorf("read gate off, yet the store was opened %d times", *calls)
	}
}

// Read gate without a store dir is a misconfiguration: noted, then the file
// answers.
func TestReadGateWithoutStoreDirNotesAndFallsBack(t *testing.T) {
	readGatedAgent(t, "dw-agent", "working: still legacy\n")
	t.Setenv("PARLAY_CREW_STORE", "")
	calls := stubCrewOpenRead(t, newFakeCrewClient(), nil)

	var got CrewStateResult
	errOut := captureStderr(t, func() {
		got = crewStateForAgentEnrolled("dw-agent", enrolledYes)
	})
	if got.State != "working" {
		t.Errorf("got %+v", got)
	}
	if !strings.Contains(errOut, "PARLAY_CREW_READ_BEADS is set but PARLAY_CREW_STORE is not") {
		t.Errorf("misconfiguration must be named; stderr = %q", errOut)
	}
	if *calls != 0 {
		t.Errorf("store opened with no dir configured (%d times)", *calls)
	}
}

// Both sources empty under the gate: the absent column of the frozen table,
// unchanged.
func TestBeadGateWithNothingAnywhereIsStillTheAbsentCell(t *testing.T) {
	readGatedAgent(t, "dw-agent", "")
	stubCrewOpenRead(t, newFakeCrewClient(), nil)

	got := crewStateForAgentEnrolled("dw-agent", enrolledYes)
	want := CrewStateResult{State: "unknown", Source: "none", Detail: "no status recorded", ExitCode: ExitCrewNoStatus}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
