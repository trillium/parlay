// ApplyStatus against the in-memory fake: the unit-3 write fold — first
// write creates the crew bead, later writes merge onto it, terminal verbs
// close it, keyed writes drive the decision.* transitions — all per
// docs/crew-bead-schema.md.
package parlaybeads

import (
	"context"
	"encoding/json"
	"testing"

	beads "github.com/steveyegge/beads"
)

func applyWorking(t *testing.T, c Client, agent, note string) string {
	t.Helper()
	id, err := ApplyStatus(context.Background(), c, CrewStatus{
		AgentID: agent, Verb: VerbWorking, Note: note, At: "2026-08-31T01:02:03Z",
	}, nil)
	if err != nil {
		t.Fatalf("ApplyStatus: %v", err)
	}
	return id
}

// seedCrewBead plants an existing crew bead for agentID in the fake store.
func seedCrewBead(f *fakeStore, id, agentID string) {
	f.issues[id] = &beads.Issue{
		ID:       id,
		Status:   beads.StatusInProgress,
		Metadata: json.RawMessage(`{"agent_id":"` + agentID + `"}`),
	}
	f.labels[id] = []string{LabelCrew}
}

func TestApplyStatusFirstWriteCreatesTheCrewBead(t *testing.T) {
	c, f := fakeClient()
	id := applyWorking(t, c, "status-lift-2", "porting the writer")

	issue := f.issues[id]
	if issue == nil {
		t.Fatalf("no bead created under %q", id)
	}
	if string(issue.IssueType) != BeadTypeAgent || issue.Assignee != "status-lift-2" {
		t.Errorf("bead shape = type %q assignee %q, want agent/status-lift-2", issue.IssueType, issue.Assignee)
	}
	if got := f.labels[id]; len(got) != 1 || got[0] != LabelCrew {
		t.Errorf("labels = %v, want [%s]", got, LabelCrew)
	}
	var meta map[string]string
	if err := json.Unmarshal(issue.Metadata, &meta); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	for k, want := range map[string]string{
		KeyAgentID:    "status-lift-2",
		KeyStatusVerb: VerbWorking,
		KeyStatusNote: "porting the writer",
		KeyStatusAt:   "2026-08-31T01:02:03Z",
		KeyStatusKey:  "",
	} {
		if meta[k] != want {
			t.Errorf("metadata[%s] = %q, want %q", k, meta[k], want)
		}
	}
	if got := f.updates[id]["status"]; got != StatusInProgress {
		t.Errorf("working should project to %s, got %v", StatusInProgress, got)
	}
}

func TestApplyStatusSecondWriteMergesOntoTheSameBead(t *testing.T) {
	c, f := fakeClient()
	seedCrewBead(f, "crew-3", "agent-x")

	id, err := ApplyStatus(context.Background(), c, CrewStatus{
		AgentID: agentX, Verb: VerbPaused, Note: "lunch", At: "2026-08-31T02:00:00Z",
	}, nil)
	if err != nil {
		t.Fatalf("ApplyStatus: %v", err)
	}
	if id != "crew-3" {
		t.Errorf("wrote to %q, want the existing crew-3", id)
	}
	if len(f.issues) != 1 {
		t.Errorf("a second write minted a bead: %d issues", len(f.issues))
	}
	if got := string(f.merged["crew-3"][KeyStatusVerb]); got != `"paused"` {
		t.Errorf("merged status_verb = %s", got)
	}
	if got := f.updates["crew-3"]["status"]; got != StatusDeferred {
		t.Errorf("paused should project to %s, got %v", StatusDeferred, got)
	}
}

const agentX = "agent-x"

func TestApplyStatusTerminalVerbsCloseWithTheVerbAsReason(t *testing.T) {
	for verb, reason := range map[string]string{VerbDone: "done", VerbFailed: "failed"} {
		c, f := fakeClient()
		seedCrewBead(f, "crew-1", agentX)
		if _, err := ApplyStatus(context.Background(), c, CrewStatus{AgentID: agentX, Verb: verb}, nil); err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if f.closeReason["crew-1"] != reason {
			t.Errorf("%s: close reason = %q, want %q", verb, f.closeReason["crew-1"], reason)
		}
	}
}

// Claim's shape: the very first status an agent ever records can be terminal.
func TestApplyStatusTerminalFirstWriteCreatesThenCloses(t *testing.T) {
	c, f := fakeClient()
	id, err := ApplyStatus(context.Background(), c, CrewStatus{
		AgentID: agentX, Verb: VerbFailed, Note: "claim task-1: no work",
	}, nil)
	if err != nil {
		t.Fatalf("ApplyStatus: %v", err)
	}
	if f.closeReason[id] != "failed" {
		t.Errorf("close reason = %q, want failed", f.closeReason[id])
	}
}

func TestApplyStatusKeyedDecisionTransitions(t *testing.T) {
	cases := []struct {
		verb, want string
	}{
		{VerbNeedsDecision, `"open"`},
		{VerbBlocked, `"open"`}, // blocked is an opener too — firstmate's fold treats it like needs-decision
		{VerbResolved, `"resolved"`},
	}
	for _, tc := range cases {
		c, f := fakeClient()
		seedCrewBead(f, "crew-1", agentX)
		if _, err := ApplyStatus(context.Background(), c, CrewStatus{AgentID: agentX, Verb: tc.verb, Key: "api-shape"}, nil); err != nil {
			t.Fatalf("%s: %v", tc.verb, err)
		}
		if got := string(f.merged["crew-1"][DecisionKeyPrefix+"api-shape"]); got != tc.want {
			t.Errorf("%s [key=api-shape]: decision.api-shape = %s, want %s", tc.verb, got, tc.want)
		}
	}
}

func TestApplyStatusKeylessWritesTouchNoDecisions(t *testing.T) {
	c, f := fakeClient()
	seedCrewBead(f, "crew-1", agentX)
	if _, err := ApplyStatus(context.Background(), c, CrewStatus{AgentID: agentX, Verb: VerbNeedsDecision, Note: "which way"}, nil); err != nil {
		t.Fatal(err)
	}
	for k := range f.merged["crew-1"] {
		if len(k) > len(DecisionKeyPrefix) && k[:len(DecisionKeyPrefix)] == DecisionKeyPrefix {
			t.Errorf("keyless needs-decision merged %s — decisions are keyed-only", k)
		}
	}
}

func TestApplyStatusRejectsNonWriterVerbs(t *testing.T) {
	c, _ := fakeClient()
	for _, verb := range []string{VerbCaptainHeld, "sprinting", ""} {
		if _, err := ApplyStatus(context.Background(), c, CrewStatus{AgentID: agentX, Verb: verb}, nil); err == nil {
			t.Errorf("verb %q: want an error, wrote instead", verb)
		}
	}
}

// Any verb may follow any verb, exactly like the status file: a non-terminal
// write on a closed bead sets its status back rather than erroring.
func TestApplyStatusNonTerminalAfterClosedReopens(t *testing.T) {
	c, f := fakeClient()
	seedCrewBead(f, "crew-1", agentX)
	f.issues["crew-1"].Status = beads.StatusClosed

	if _, err := ApplyStatus(context.Background(), c, CrewStatus{AgentID: agentX, Verb: VerbWorking}, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.updates["crew-1"]["status"]; got != StatusInProgress {
		t.Errorf("status after working-on-closed = %v, want %s", got, StatusInProgress)
	}
}

func TestApplyStatusTerminalOnAlreadyClosedDoesNotReclose(t *testing.T) {
	c, f := fakeClient()
	seedCrewBead(f, "crew-1", agentX)
	f.issues["crew-1"].Status = beads.StatusClosed

	if _, err := ApplyStatus(context.Background(), c, CrewStatus{AgentID: agentX, Verb: VerbDone}, nil); err != nil {
		t.Fatal(err)
	}
	if _, reclosed := f.closeReason["crew-1"]; reclosed {
		t.Error("done on an already-closed bead re-closed it; the metadata merge alone was owed")
	}
	if got := string(f.merged["crew-1"][KeyStatusVerb]); got != `"done"` {
		t.Errorf("metadata still owed on a closed bead: status_verb = %s", got)
	}
}

func TestApplyStatusExtraMetaRidesAlong(t *testing.T) {
	c, f := fakeClient()
	seedCrewBead(f, "crew-1", agentX)
	extra := map[string]string{KeyGCSession: "gc-0042"}
	if _, err := ApplyStatus(context.Background(), c, CrewStatus{AgentID: agentX, Verb: VerbWorking}, extra); err != nil {
		t.Fatal(err)
	}
	if got := string(f.merged["crew-1"][KeyGCSession]); got != `"gc-0042"` {
		t.Errorf("gc_session = %s, want the attachment pointer merged", got)
	}
}

// A past create race left two beads claiming one agent: the pick must be
// deterministic (numeric id order — crew-2 before crew-10) so all future
// writers converge on one bead instead of wedging on ambiguity.
func TestFindCrewBeadConvergesOnLowestNumericID(t *testing.T) {
	c, f := fakeClient()
	seedCrewBead(f, "crew-10", agentX)
	seedCrewBead(f, "crew-2", agentX)
	seedCrewBead(f, "crew-5", "someone-else")

	bead, found, err := FindCrewBead(context.Background(), c, agentX)
	if err != nil || !found {
		t.Fatalf("FindCrewBead: found=%v err=%v", found, err)
	}
	if bead.ID != "crew-2" {
		t.Errorf("picked %s, want crew-2 (numeric order, not lexical)", bead.ID)
	}
}

func TestFindCrewBeadAbsentIsNotAnError(t *testing.T) {
	c, _ := fakeClient()
	_, found, err := FindCrewBead(context.Background(), c, "never-wrote")
	if err != nil || found {
		t.Errorf("FindCrewBead on absent = (found %v, err %v), want (false, nil)", found, err)
	}
}
