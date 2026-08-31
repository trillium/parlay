package parlaybeads

import (
	"reflect"
	"testing"
)

// The 7-verb writer vocabulary must match commands/status_verb.go's
// statusVerbs exactly, in order. Spelled out literally here (not imported)
// so a drift in either place fails a test instead of silently forking the
// vocabulary.
func TestWriterVerbsMatchStatusVerbVocabulary(t *testing.T) {
	want := []string{"working", "needs-decision", "blocked", "paused", "done", "failed", "resolved"}
	if got := WriterVerbs(); !reflect.DeepEqual(got, want) {
		t.Errorf("WriterVerbs() = %v, want %v (commands/status_verb.go statusVerbs)", got, want)
	}
}

func TestVerbSpecTable(t *testing.T) {
	cases := []struct {
		verb, status, reason string
		terminal             bool
	}{
		{"working", StatusInProgress, "", false},
		{"needs-decision", StatusBlocked, "", false},
		{"blocked", StatusBlocked, "", false},
		{"paused", StatusDeferred, "", false},
		{"done", StatusClosed, "done", true},
		{"failed", StatusClosed, "failed", true},
		{"resolved", StatusInProgress, "", false},
	}
	for _, tc := range cases {
		status, reason, terminal, ok := VerbSpec(tc.verb)
		if !ok {
			t.Errorf("VerbSpec(%q): not found", tc.verb)
			continue
		}
		if status != tc.status || reason != tc.reason || terminal != tc.terminal {
			t.Errorf("VerbSpec(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.verb, status, reason, terminal, tc.status, tc.reason, tc.terminal)
		}
	}
	// A terminal row must carry a close reason and vice versa.
	for _, s := range verbSpecs {
		if s.Terminal != (s.CloseReason != "") {
			t.Errorf("verb %q: Terminal=%v but CloseReason=%q", s.Verb, s.Terminal, s.CloseReason)
		}
	}
}

// captain-held is reader-plane vocabulary: never stored, so deliberately
// absent from the mapping. This test is the tripwire against someone
// "helpfully" adding it.
func TestCaptainHeldIsUnmapped(t *testing.T) {
	if _, _, _, ok := VerbSpec(VerbCaptainHeld); ok {
		t.Error("captain-held has a verbSpec row; it is reader-plane vocabulary and must never be stored")
	}
}

// Mirrors gascity's TestInfoCodecFieldsDisjoint in spirit: every codec entry
// must fold a distinct key, or a later entry silently shadows an earlier one.
func TestCrewKeyCodecKeysDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range crewKeyCodec {
		if seen[spec.key] {
			t.Errorf("duplicate codec key %q", spec.key)
		}
		seen[spec.key] = true
	}
}

func TestCrewStatusFoldAndRoundTrip(t *testing.T) {
	meta := map[string]string{
		"agent_id":          "status-lift-1",
		"status_verb":       "needs-decision",
		"status_key":        "topology",
		"status_note":       "pick one",
		"status_at":         "2026-08-30T12:00:00Z",
		"decision.topology": "open",
		"decision.older":    "resolved",
		"foreign_key":       "ignored",
	}
	c := CrewStatusFromMetadata(meta)
	if c.AgentID != "status-lift-1" || c.Verb != "needs-decision" || c.Key != "topology" ||
		c.Note != "pick one" || c.At != "2026-08-30T12:00:00Z" {
		t.Errorf("fold = %+v", c)
	}
	wantDecisions := map[string]string{"topology": "open", "older": "resolved"}
	if !reflect.DeepEqual(c.Decisions, wantDecisions) {
		t.Errorf("Decisions = %v, want %v", c.Decisions, wantDecisions)
	}

	// Write-side inverse carries exactly the four status_* keys.
	got := c.StatusMetadata()
	want := map[string]string{
		"status_verb": "needs-decision",
		"status_key":  "topology",
		"status_note": "pick one",
		"status_at":   "2026-08-30T12:00:00Z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StatusMetadata() = %v, want %v", got, want)
	}
}

func TestCrewStatusFromMetadataEmpty(t *testing.T) {
	c := CrewStatusFromMetadata(nil)
	if c.Verb != "" || c.Decisions != nil {
		t.Errorf("fold of nil = %+v, want zero value", c)
	}
	// A bare "decision." key (empty slug) is not a decision.
	c = CrewStatusFromMetadata(map[string]string{"decision.": "open"})
	if c.Decisions != nil {
		t.Errorf("empty-slug decision folded: %v", c.Decisions)
	}
}

// Pins the rendered line to the byte shape of commands/status_verb.go's
// buildStatusLine — firstmate's grammar "<verb> [key=<slug>]: <note>\n".
// Expected strings are literals on purpose: if either renderer changes shape,
// this fails instead of both drifting together.
func TestRenderStatusLineByteShape(t *testing.T) {
	cases := []struct {
		c    CrewStatus
		want string
	}{
		{CrewStatus{Verb: "working", Note: "porting unit 2"}, "working: porting unit 2\n"},
		{CrewStatus{Verb: "needs-decision", Key: "topology", Note: "pick one"}, "needs-decision [key=topology]: pick one\n"},
		{CrewStatus{Verb: "resolved", Key: "topology"}, "resolved [key=topology]:\n"},
		{CrewStatus{Verb: "done"}, "done:\n"},
	}
	for _, tc := range cases {
		if got := tc.c.RenderStatusLine(); got != tc.want {
			t.Errorf("RenderStatusLine(%+v) = %q, want %q", tc.c, got, tc.want)
		}
	}
}
