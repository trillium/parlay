// Unit 7: status-migrate, tested against FIXTURE COPIES ONLY — every root
// here is a t.TempDir(); the live-root captain gate (robots-lor) is itself
// under test, never crossed. Covers: dry-run-by-default, replay-not-truncate
// (originals byte-identical, backup written), the ApplyStatus fold landing
// in the bead, cursor seeding at head (history must not re-fire through
// supervise), the two idempotence latches (existing events.jsonl, existing
// backup), unparseable lines kept in place, scoping, and the unit-6
// projection round-trip.
package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/crewevents"
	"github.com/trillium/parlay/tools/cli/internal/parlaybeads"
)

// migrateEnv wires a fixture agents root plus the store gate. The store is
// stubbed to the in-memory fake; the counter proves when it was (not) opened.
func migrateEnv(t *testing.T) (root string, fake *fakeCrewClient, opens *int) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("PARLAY_CREW_STORE", filepath.Join(t.TempDir(), "store"))
	t.Setenv("PARLAY_AGENT_HOME", root)
	t.Setenv("PARLAY_AGENT_ID", "")
	t.Setenv("PARLAY_STATUS_FILE", "")
	fake = newFakeCrewClient()
	opens = stubCrewOpen(t, fake, nil)
	return root, fake, opens
}

func mkMigrateAgent(t *testing.T, root, id, status string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readEvents(t *testing.T, root, id string) []crewevents.Event {
	t.Helper()
	evs, skipped, err := crewevents.ReadAfter(crewevents.File(filepath.Join(root, id)), 0)
	if err != nil || skipped != 0 {
		t.Fatalf("ReadAfter(%s): skipped=%d err=%v", id, skipped, err)
	}
	return evs
}

const migrateHistory = "working: starting out\nneeds-decision [key=api-shape]: REST or gRPC?\ndone: all green\n"

func TestStatusMigrateDryRunByDefaultChangesNothing(t *testing.T) {
	root, _, opens := migrateEnv(t)
	mkMigrateAgent(t, root, "m1", migrateHistory)
	mkMigrateAgent(t, root, "m2", "working: also here\n")

	out := captureStdout(t, func() { StatusMigrate([]string{"--agents-root", root}) })
	if !strings.Contains(out, "dry run — nothing changed") || !strings.Contains(out, "m1: 3 line(s) to replay") {
		t.Errorf("dry-run output = %q, want the plan and the dry-run banner", out)
	}
	for _, id := range []string{"m1", "m2"} {
		dir := filepath.Join(root, id)
		for _, f := range []string{"events.jsonl", migrateBackupName, ".supervise-seq"} {
			if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
				t.Errorf("%s/%s exists after a dry run", id, f)
			}
		}
	}
	if *opens != 0 {
		t.Errorf("store opened %d time(s) during a dry run", *opens)
	}
}

func TestStatusMigrateApplyReplaysBacksUpAndSeedsCursor(t *testing.T) {
	root, fake, _ := migrateEnv(t)
	mkMigrateAgent(t, root, "m1", migrateHistory)

	out := captureStdout(t, func() { StatusMigrate([]string{"--agents-root", root, "--apply"}) })
	if !strings.Contains(out, "m1: replayed 3 line(s)") || !strings.Contains(out, "migrated 1 agent(s)") {
		t.Errorf("apply output = %q", out)
	}

	// Replay, never truncate: the original is byte-identical, the backup too.
	orig, err := os.ReadFile(filepath.Join(root, "m1", "status"))
	if err != nil || string(orig) != migrateHistory {
		t.Errorf("original status file changed: %q err=%v", orig, err)
	}
	backup, err := os.ReadFile(filepath.Join(root, "m1", migrateBackupName))
	if err != nil || !bytes.Equal(backup, orig) {
		t.Errorf("backup = %q err=%v, want a byte-identical copy", backup, err)
	}

	evs := readEvents(t, root, "m1")
	if len(evs) != 3 {
		t.Fatalf("event log holds %d events, want 3", len(evs))
	}
	wantVerbs := []string{"working", "needs-decision", "done"}
	for i, ev := range evs {
		if ev.Verb != wantVerbs[i] || ev.Name != crewevents.EventCrewStatus || ev.Agent != "m1" {
			t.Errorf("event %d = %+v, want verb %s", i, ev, wantVerbs[i])
		}
	}
	if evs[1].Key != "api-shape" || evs[1].Note != "REST or gRPC?" {
		t.Errorf("keyed event = %+v, want key/note preserved", evs[1])
	}

	// Cursor seeded at head.
	cur, err := os.ReadFile(filepath.Join(root, "m1", ".supervise-seq"))
	if err != nil || strings.TrimSpace(string(cur)) != "3" {
		t.Errorf("cursor = %q err=%v, want 3", cur, err)
	}

	// The bead holds the FOLD of the whole history: last verb done (terminal
	// → closed), the keyed decision opened along the way and left open.
	bead := fake.soleCrewBead(t)
	if bead.Metadata[parlaybeads.KeyStatusVerb] != "done" {
		t.Errorf("bead status_verb = %q, want done", bead.Metadata[parlaybeads.KeyStatusVerb])
	}
	if bead.Metadata[parlaybeads.DecisionKeyPrefix+"api-shape"] != "open" {
		t.Errorf("decision.api-shape = %q, want open", bead.Metadata[parlaybeads.DecisionKeyPrefix+"api-shape"])
	}
}

// Unit 6 ∘ unit 7: projecting the replayed event log reproduces the original
// file byte for byte (for canonical-grammar files, which is what the live
// writer has always produced).
func TestStatusMigrateProjectionRoundTrip(t *testing.T) {
	root, _, _ := migrateEnv(t)
	mkMigrateAgent(t, root, "m1", migrateHistory)
	captureStdout(t, func() { StatusMigrate([]string{"--agents-root", root, "--apply"}) })

	if projected := projectStatusFile(readEvents(t, root, "m1")); string(projected) != migrateHistory {
		t.Errorf("projection of the migrated log = %q, want the original file %q", projected, migrateHistory)
	}
}

func TestStatusMigrateRefusesAgentWhoseLogAlreadyExists(t *testing.T) {
	root, _, _ := migrateEnv(t)
	mkMigrateAgent(t, root, "started", "working: x\n")
	if _, err := crewevents.Append(crewevents.File(filepath.Join(root, "started")), crewevents.Event{Name: crewevents.EventCrewStatus, Verb: "working"}); err != nil {
		t.Fatal(err)
	}
	mkMigrateAgent(t, root, "clean", "done: y\n")

	out := captureStdout(t, func() { StatusMigrate([]string{"--agents-root", root, "--apply"}) })
	if !strings.Contains(out, "started: SKIP") || !strings.Contains(out, "refusing to replay") {
		t.Errorf("output = %q, want a SKIP for the already-dual-writing agent", out)
	}
	if evs := readEvents(t, root, "started"); len(evs) != 1 {
		t.Errorf("started's log grew to %d events — the skip must not replay", len(evs))
	}
	if evs := readEvents(t, root, "clean"); len(evs) != 1 {
		t.Errorf("clean's log holds %d events, want 1 (other agents still migrate)", len(evs))
	}
}

func TestStatusMigrateSecondApplyIsRefusedByTheBackupLatch(t *testing.T) {
	root, _, _ := migrateEnv(t)
	mkMigrateAgent(t, root, "m1", migrateHistory)
	captureStdout(t, func() { StatusMigrate([]string{"--agents-root", root, "--apply"}) })

	out := captureStdout(t, func() { StatusMigrate([]string{"--agents-root", root, "--apply"}) })
	if !strings.Contains(out, "SKIP") || !strings.Contains(out, "nothing to do") {
		t.Errorf("second apply output = %q, want a SKIP + nothing-to-do", out)
	}
	if evs := readEvents(t, root, "m1"); len(evs) != 3 {
		t.Errorf("event log holds %d events after a re-run, want still 3", len(evs))
	}
}

func TestStatusMigrateKeepsUnparseableLinesInPlace(t *testing.T) {
	root, _, _ := migrateEnv(t)
	content := "working: fine\n<<< not a status line >>>\ndone: fine too\n"
	mkMigrateAgent(t, root, "m1", content)

	out := captureStdout(t, func() { StatusMigrate([]string{"--agents-root", root, "--apply"}) })
	if !strings.Contains(out, "1 unparseable kept in place") {
		t.Errorf("output = %q, want the unparseable count reported", out)
	}
	if evs := readEvents(t, root, "m1"); len(evs) != 2 {
		t.Errorf("event log holds %d events, want 2 (garbage never replayed)", len(evs))
	}
	got, _ := os.ReadFile(filepath.Join(root, "m1", "status"))
	if string(got) != content {
		t.Errorf("status file changed: %q — unparseable lines must be KEPT, in place", got)
	}
}

// The captain gate (robots-lor): the canonical live agents root is refused
// without --live, before anything is touched.
func TestStatusMigrateRefusesTheLiveRootWithoutLiveFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	liveRoot := filepath.Join(home, ".parlay", "agents")
	t.Setenv("PARLAY_CREW_STORE", filepath.Join(t.TempDir(), "store"))
	stubCrewOpen(t, newFakeCrewClient(), nil)
	mkMigrateAgent(t, liveRoot, "m1", "done: x\n")

	var out string
	code, exited := withExitTrap(t, func() {
		out = captureStdout(t, func() { StatusMigrate([]string{"--agents-root", liveRoot, "--apply"}) })
	})
	if !exited || code != 2 {
		t.Errorf("exit = (%d, %v), want (2, true)", code, exited)
	}
	if strings.Contains(out, "replayed") {
		t.Errorf("output = %q — nothing may run against the live root without --live", out)
	}
	if _, err := os.Stat(crewevents.File(filepath.Join(liveRoot, "m1"))); !os.IsNotExist(err) {
		t.Errorf("event log created under the refused live root")
	}

	// With --live the same invocation proceeds (here HOME is a fixture; on
	// the captain's box passing --live is the captain-gated act itself).
	captureStdout(t, func() { StatusMigrate([]string{"--agents-root", liveRoot, "--apply", "--live"}) })
	if evs := readEvents(t, liveRoot, "m1"); len(evs) != 1 {
		t.Errorf("with --live: %d events, want 1", len(evs))
	}
}

func TestStatusMigrateRequiresRootAndStore(t *testing.T) {
	t.Setenv("PARLAY_CREW_STORE", filepath.Join(t.TempDir(), "store"))
	code, exited := withExitTrap(t, func() { captureStdout(t, func() { StatusMigrate(nil) }) })
	if !exited || code != 2 {
		t.Errorf("no --agents-root: exit = (%d, %v), want (2, true)", code, exited)
	}

	t.Setenv("PARLAY_CREW_STORE", "")
	code, exited = withExitTrap(t, func() {
		captureStdout(t, func() { StatusMigrate([]string{"--agents-root", t.TempDir()}) })
	})
	if !exited || code != 2 {
		t.Errorf("no PARLAY_CREW_STORE: exit = (%d, %v), want (2, true)", code, exited)
	}
}

func TestStatusMigrateAgentFlagLimitsScope(t *testing.T) {
	root, _, _ := migrateEnv(t)
	mkMigrateAgent(t, root, "in-scope", "done: x\n")
	mkMigrateAgent(t, root, "out-of-scope", "done: y\n")

	captureStdout(t, func() { StatusMigrate([]string{"--agents-root", root, "--agent", "in-scope", "--apply"}) })
	if evs := readEvents(t, root, "in-scope"); len(evs) != 1 {
		t.Errorf("in-scope: %d events, want 1", len(evs))
	}
	if evs := readEvents(t, root, "out-of-scope"); len(evs) != 0 {
		t.Errorf("out-of-scope: %d events, want 0", len(evs))
	}
}

// The point of seeding the cursor at head: a post-migration supervise pass
// (readers cut over) treats the whole replayed history as already-seen — and
// still wakes on the NEXT live event.
func TestStatusMigrateHistoryDoesNotRefireThroughSupervise(t *testing.T) {
	root, _, _ := migrateEnv(t)
	mkMigrateAgent(t, root, "m1", migrateHistory) // ends in a terminal "done"
	captureStdout(t, func() { StatusMigrate([]string{"--agents-root", root, "--apply"}) })

	var bodies []map[string]any
	srv := newSuperviseServer(t, &bodies)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "m1")
	t.Setenv("PARLAY_UNATTENDED_FLAG", "")
	t.Setenv("PARLAY_CREW_READ_BEADS", "1")

	captureStdout(t, func() { Supervise([]string{"m1"}) })
	if len(bodies) != 0 {
		t.Fatalf("relay posts = %d, want 0 — migrated history must not re-fire", len(bodies))
	}

	if _, err := crewevents.Append(crewevents.File(filepath.Join(root, "m1")), crewevents.Event{Name: crewevents.EventCrewStatus, Agent: "m1", Verb: "failed", Note: "new, after migration"}); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() { Supervise([]string{"m1"}) })
	if len(bodies) != 1 || !strings.Contains(bodies[0]["text"].(string), "is failed") {
		t.Errorf("posts = %+v, want exactly the one NEW post-migration event", bodies)
	}
}
