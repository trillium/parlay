// The unit-3 dual-write seam: gated off PARLAY_CREW_STORE, file-first
// ordering, loud failure for `parlay status` (Q5b), best-effort-with-report
// for claim's failure recorder, and the gc_session attachment pointer.
package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/crewevents"
	"github.com/trillium/parlay/tools/cli/internal/parlaybeads"
)

// fakeCrewClient is an in-memory parlaybeads.Client. The write-fold logic it
// receives is tested in parlaybeads/writer_test.go; here it exists to see
// WHAT the commands layer sent, and to fail on demand.
type fakeCrewClient struct {
	beads  map[string]*parlaybeads.Bead
	closed map[string]string
}

func newFakeCrewClient() *fakeCrewClient {
	return &fakeCrewClient{beads: map[string]*parlaybeads.Bead{}, closed: map[string]string{}}
}

func (f *fakeCrewClient) Create(_ context.Context, b parlaybeads.Bead) (string, error) {
	id := fmt.Sprintf("crew-%d", len(f.beads)+1)
	b.ID = id
	f.beads[id] = &b
	return id, nil
}

func (f *fakeCrewClient) Get(_ context.Context, id string) (parlaybeads.Bead, error) {
	b, ok := f.beads[id]
	if !ok {
		return parlaybeads.Bead{}, fmt.Errorf("%w: %s", parlaybeads.ErrNotFound, id)
	}
	return *b, nil
}

func (f *fakeCrewClient) MergeMetadata(_ context.Context, id string, meta map[string]string) error {
	b, ok := f.beads[id]
	if !ok {
		return fmt.Errorf("%w: %s", parlaybeads.ErrNotFound, id)
	}
	if b.Metadata == nil {
		b.Metadata = map[string]string{}
	}
	for k, v := range meta {
		b.Metadata[k] = v
	}
	return nil
}

func (f *fakeCrewClient) SetStatus(_ context.Context, id, status string) error {
	f.beads[id].Status = status
	return nil
}

func (f *fakeCrewClient) CloseBead(_ context.Context, id, reason string) error {
	f.closed[id] = reason
	f.beads[id].Status = parlaybeads.StatusClosed
	return nil
}

func (f *fakeCrewClient) ListByLabel(_ context.Context, label string) ([]parlaybeads.Bead, error) {
	var out []parlaybeads.Bead
	for _, b := range f.beads {
		for _, l := range b.Labels {
			if l == label {
				out = append(out, *b)
			}
		}
	}
	return out, nil
}

func (f *fakeCrewClient) Close() error { return nil }

// soleCrewBead asserts exactly one bead exists and returns it.
func (f *fakeCrewClient) soleCrewBead(t *testing.T) parlaybeads.Bead {
	t.Helper()
	if len(f.beads) != 1 {
		t.Fatalf("store holds %d beads, want exactly 1", len(f.beads))
	}
	for _, b := range f.beads {
		return *b
	}
	panic("unreachable")
}

// stubCrewOpen swaps the store-opening seam for the test's lifetime and
// counts calls.
func stubCrewOpen(t *testing.T, c parlaybeads.Client, err error) *int {
	t.Helper()
	calls := new(int)
	prev := crewStoreOpen
	crewStoreOpen = func(context.Context, string, string) (parlaybeads.Client, error) {
		*calls++
		return c, err
	}
	t.Cleanup(func() { crewStoreOpen = prev })
	return calls
}

// gatedAgent wires up a temp agent home with the crew store gate ON and
// returns (agent dir, status file path).
func gatedAgent(t *testing.T, agent string) (dir, statusFile string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_AGENT_ID", agent)
	t.Setenv("PARLAY_STATUS_FILE", "")
	t.Setenv("PARLAY_CREW_STORE", filepath.Join(t.TempDir(), "store"))
	dir = filepath.Join(home, agent)
	return dir, filepath.Join(dir, "status")
}

func TestStatusVerbDualWritesWhenGated(t *testing.T) {
	dir, statusFile := gatedAgent(t, "dw-agent")
	fake := newFakeCrewClient()
	stubCrewOpen(t, fake, nil)

	out := captureStdout(t, func() { StatusVerb([]string{"working", "porting", "the", "writer"}) })
	if !strings.Contains(out, "status working") {
		t.Errorf("unexpected output: %q", out)
	}

	// 1. The file projection landed in the exact legacy byte shape.
	data, err := os.ReadFile(statusFile)
	if err != nil || string(data) != "working: porting the writer\n" {
		t.Errorf("status file = %q (err %v), want the legacy line", data, err)
	}
	// 2. The event landed with seq 1 and the typed name.
	evs, skipped, err := crewevents.ReadAfter(crewevents.File(dir), 0)
	if err != nil || skipped != 0 || len(evs) != 1 {
		t.Fatalf("event log = (%d evs, %d skipped, %v), want exactly 1", len(evs), skipped, err)
	}
	ev := evs[0]
	if ev.Seq != 1 || ev.Name != crewevents.EventCrewStatus || ev.Agent != "dw-agent" || ev.Verb != "working" || ev.Note != "porting the writer" || ev.At == "" {
		t.Errorf("event = %+v", ev)
	}
	// 3. The bead write landed per the unit-2 schema.
	b := fake.soleCrewBead(t)
	if b.Metadata[parlaybeads.KeyStatusVerb] != "working" || b.Metadata[parlaybeads.KeyAgentID] != "dw-agent" || b.Status != parlaybeads.StatusInProgress {
		t.Errorf("bead = %+v", b)
	}
	if b.Metadata[parlaybeads.KeyStatusAt] != ev.At {
		t.Errorf("bead at %q != event at %q — one write, one timestamp", b.Metadata[parlaybeads.KeyStatusAt], ev.At)
	}
}

func TestStatusVerbGateOffIsByteIdenticalLegacy(t *testing.T) {
	dir, statusFile := gatedAgent(t, "legacy-agent")
	t.Setenv("PARLAY_CREW_STORE", "")
	calls := stubCrewOpen(t, newFakeCrewClient(), nil)

	captureStdout(t, func() { StatusVerb([]string{"working", "as", "before"}) })

	if data, err := os.ReadFile(statusFile); err != nil || string(data) != "working: as before\n" {
		t.Errorf("status file = %q (err %v)", data, err)
	}
	if _, err := os.Stat(crewevents.File(dir)); !os.IsNotExist(err) {
		t.Error("gate off, yet an event log appeared")
	}
	if *calls != 0 {
		t.Errorf("gate off, yet the store was opened %d times", *calls)
	}
}

// Q5b with the file's reliability preserved: the store failing is a loud
// EXIT_RUNTIME death — but only AFTER the operative file line landed, and the
// message says so.
func TestStatusVerbStoreFailureDiesLoudAfterFileLanded(t *testing.T) {
	_, statusFile := gatedAgent(t, "unlucky")
	stubCrewOpen(t, nil, errors.New("dolt is on fire"))

	var code int
	var exited bool
	captureStderr(t, func() {
		captureStdout(t, func() {
			code, exited = withExitTrap(t, func() { StatusVerb([]string{"working", "x"}) })
		})
	})
	if !exited || code != config.ExitRuntime {
		t.Fatalf("exit = (%d, %v), want a loud ExitRuntime death", code, exited)
	}
	if data, err := os.ReadFile(statusFile); err != nil || string(data) != "working: x\n" {
		t.Errorf("the operative status line must land BEFORE the store is tried; file = %q (err %v)", data, err)
	}
}

func TestStatusVerbEventAppendFailureDiesLoudAfterFileLanded(t *testing.T) {
	// File sink redirected somewhere writable; the agent home is a regular
	// FILE so the event log's MkdirAll fails — file lands, event append dies.
	sink := filepath.Join(t.TempDir(), "fm-injected.status")
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARLAY_AGENT_HOME", blocker)
	t.Setenv("PARLAY_AGENT_ID", "dw-agent")
	t.Setenv("PARLAY_STATUS_FILE", sink)
	t.Setenv("PARLAY_CREW_STORE", filepath.Join(t.TempDir(), "store"))
	calls := stubCrewOpen(t, newFakeCrewClient(), nil)

	var code int
	var exited bool
	captureStderr(t, func() {
		captureStdout(t, func() {
			code, exited = withExitTrap(t, func() { StatusVerb([]string{"blocked", "y"}) })
		})
	})
	if !exited || code != config.ExitRuntime {
		t.Fatalf("exit = (%d, %v), want ExitRuntime — an event that cannot land is never a silent drop", code, exited)
	}
	if data, err := os.ReadFile(sink); err != nil || string(data) != "blocked: y\n" {
		t.Errorf("file-first ordering violated: sink = %q (err %v)", data, err)
	}
	if *calls != 0 {
		t.Error("bead write attempted after the event append failed — event-before-bead ordering violated")
	}
}

// PARLAY_STATUS_FILE with no PARLAY_AGENT_ID is a configuration shape, not a
// failure: the write succeeds, with a stderr note that the dual-write was
// structurally skipped.
func TestStatusVerbNoIdentitySkipsDualWriteWithNote(t *testing.T) {
	sink := filepath.Join(t.TempDir(), "fm-injected.status")
	t.Setenv("PARLAY_STATUS_FILE", sink)
	t.Setenv("PARLAY_AGENT_ID", "")
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	t.Setenv("PARLAY_CREW_STORE", filepath.Join(t.TempDir(), "store"))
	calls := stubCrewOpen(t, newFakeCrewClient(), nil)

	var errOut string
	errOut = captureStderr(t, func() {
		captureStdout(t, func() { StatusVerb([]string{"working", "z"}) })
	})
	if !strings.Contains(errOut, "dual-write skipped") {
		t.Errorf("want a stderr note about the structural skip, got: %q", errOut)
	}
	if data, err := os.ReadFile(sink); err != nil || string(data) != "working: z\n" {
		t.Errorf("sink = %q (err %v)", data, err)
	}
	if *calls != 0 {
		t.Error("store opened despite having no agent identity to key by")
	}
}

func TestStatusVerbAttachesGCSessionPointerWhenStamped(t *testing.T) {
	dir, _ := gatedAgent(t, "gc-born")
	fake := newFakeCrewClient()
	stubCrewOpen(t, fake, nil)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := "---\ngc_session: gc-0042\n---\n# Identity\n"
	if err := os.WriteFile(filepath.Join(dir, "identity.md"), []byte(stamp), 0o644); err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() { StatusVerb([]string{"working", "spawned by gc"}) })

	b := fake.soleCrewBead(t)
	if b.Metadata[parlaybeads.KeyGCSession] != "gc-0042" {
		t.Errorf("gc_session = %q, want the attachment pointer to the spawn seam's record", b.Metadata[parlaybeads.KeyGCSession])
	}
}

func TestClaimRecordFailureDualWrites(t *testing.T) {
	dir, statusFile := gatedAgent(t, "no-work")
	fake := newFakeCrewClient()
	stubCrewOpen(t, fake, nil)

	if !claimRecordFailure("no-work", "task-9", "the ticket does not resolve") {
		t.Fatal("claimRecordFailure = false, want the file write to land")
	}
	if data, _ := os.ReadFile(statusFile); string(data) != "failed: claim task-9: the ticket does not resolve\n" {
		t.Errorf("status file = %q", data)
	}
	evs, _, err := crewevents.ReadAfter(crewevents.File(dir), 0)
	if err != nil || len(evs) != 1 || evs[0].Verb != "failed" || evs[0].Note != "claim task-9: the ticket does not resolve" {
		t.Errorf("event log = %+v (err %v)", evs, err)
	}
	b := fake.soleCrewBead(t)
	if fake.closed[b.ID] != "failed" {
		t.Errorf("crew bead close reason = %q, want failed", fake.closed[b.ID])
	}
}

// Claim's contract is best-effort — it must not withhold the exit procedure —
// but §7.1 forbids a SILENT drop: a pipeline failure is reported to stderr
// while the file-landed result stands.
func TestClaimRecordFailureStoreFailureIsReportedNotFatal(t *testing.T) {
	_, statusFile := gatedAgent(t, "no-work")
	stubCrewOpen(t, nil, errors.New("dolt is on fire"))

	var recorded bool
	errOut := captureStderr(t, func() {
		recorded = claimRecordFailure("no-work", "task-9", "reason")
	})
	if !recorded {
		t.Error("recorded = false — the file landed; the store failing must not erase that")
	}
	if data, _ := os.ReadFile(statusFile); !strings.Contains(string(data), "failed: claim task-9") {
		t.Errorf("status file = %q", data)
	}
	if !strings.Contains(errOut, "dual-write did not land") {
		t.Errorf("want a stderr report of the dropped dual-write, got: %q", errOut)
	}
}
