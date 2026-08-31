// Unit 6: byte-for-byte golden coverage of the status-file projection.
// The contract has three failure modes and each gets its own pin:
//  1. the two renderers (buildStatusLine / RenderStatusLine) drifting apart
//     → corpus test, whole-file bytes through both paths;
//  2. the real writer's file and its own event log disagreeing
//     → end-to-end dual-write test through the actual StatusVerb command;
//  3. BOTH renderers drifting together (a sync test can't see that)
//     → checked-in golden fixture with the literal expected bytes.
package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/crewevents"
)

// projectionCorpus covers every line shape the grammar produces: all seven
// verbs, keyed and keyless, with and without a note, plus spacing and
// unicode a shell parser could trip on.
var projectionCorpus = []struct{ verb, key, note string }{
	{"working", "", "starting the port"},
	{"needs-decision", "api-shape", "REST or gRPC?"},
	{"blocked", "ci-quota", "waiting on a runner"},
	{"paused", "", ""},
	{"resolved", "api-shape", "went with REST"},
	{"failed", "", "gave up: exit 1"},
	{"done", "", "all green — ünïcode & [brackets] survive"},
	{"working", "a.b-c_9", ""},
}

func corpusEvents() []crewevents.Event {
	evs := make([]crewevents.Event, len(projectionCorpus))
	for i, c := range projectionCorpus {
		evs[i] = crewevents.Event{
			Seq: uint64(i + 1), At: "2026-08-31T00:00:00Z",
			Name: crewevents.EventCrewStatus, Agent: "px", Verb: c.verb, Key: c.key, Note: c.note,
		}
	}
	return evs
}

func TestProjectionMatchesBuildStatusLineByteForByte(t *testing.T) {
	var legacy bytes.Buffer
	for _, c := range projectionCorpus {
		legacy.WriteString(buildStatusLine(c.verb, c.key, c.note))
	}
	projected := projectStatusFile(corpusEvents())
	if !bytes.Equal(projected, legacy.Bytes()) {
		t.Errorf("projection diverged from buildStatusLine:\nprojected: %q\nlegacy:    %q", projected, legacy.Bytes())
	}
}

// The strongest form of the identity: the REAL dual-writing command produces
// both artifacts, and the file it wrote must equal the projection of the
// event log it wrote — for every line shape in the corpus.
func TestDualWriteStatusFileEqualsEventProjection(t *testing.T) {
	dir, statusFile := gatedAgent(t, "proj-agent")
	stubCrewOpen(t, newFakeCrewClient(), nil)

	for _, c := range projectionCorpus {
		argv := []string{c.verb}
		if c.key != "" {
			argv = append(argv, "--key", c.key)
		}
		if c.note != "" {
			argv = append(argv, c.note)
		}
		captureStdout(t, func() { StatusVerb(argv) })
	}

	fileBytes, err := os.ReadFile(statusFile)
	if err != nil {
		t.Fatal(err)
	}
	evs, skipped, err := crewevents.ReadAfter(crewevents.File(dir), 0)
	if err != nil || skipped != 0 {
		t.Fatalf("ReadAfter: evs=%d skipped=%d err=%v", len(evs), skipped, err)
	}
	if len(evs) != len(projectionCorpus) {
		t.Fatalf("event log holds %d events, want %d", len(evs), len(projectionCorpus))
	}
	if projected := projectStatusFile(evs); !bytes.Equal(projected, fileBytes) {
		t.Errorf("the written file and the projection of its own event log diverged:\nfile:      %q\nprojected: %q", fileBytes, projected)
	}
}

// The golden fixture holds the literal bytes ~30 firstmate scripts would
// parse. Regenerating it to make this pass IS a breaking change to the
// compatibility contract — do that only with the wire-consumer's sign-off.
func TestProjectionGoldenFixture(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "status-projection.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if got := projectStatusFile(corpusEvents()); !bytes.Equal(got, want) {
		t.Errorf("projection diverged from the golden fixture:\ngot:  %q\nwant: %q", got, want)
	}
}

// Foreign event names in the log (other seams share the parlay.* registry)
// must not leak lines into the file view.
func TestProjectionSkipsForeignEventNames(t *testing.T) {
	evs := []crewevents.Event{
		{Seq: 1, Name: crewevents.EventCrewStatus, Verb: "working", Note: "real"},
		{Seq: 2, Name: "parlay.crew.somethingelse", Verb: "done", Note: "not a status"},
		{Seq: 3, Name: crewevents.EventCrewStatus, Verb: "done", Note: "also real"},
	}
	want := buildStatusLine("working", "", "real") + buildStatusLine("done", "", "also real")
	if got := string(projectStatusFile(evs)); got != want {
		t.Errorf("got %q, want %q (foreign names skipped)", got, want)
	}
}
