// System-level coverage for the robots-watch poll daemon (robots-p0e0).
//
//  1. TestWatchedStoresIntendedTable — pins the INTENDED store registry the
//     daemon must watch, including `inbox` with created+closed. Today the
//     table lacks inbox, so this test is RED with ROBOTSWATCH_GAP_PROBE=1
//     (the listed gap: inbox is a separate dolt database and its beads never
//     appear in `robots list --all`). Gated so default/CI runs stay green — a
//     tests-only task must not edit the production watch table.
//  2. TestScratchStoreClosedBeadFiresNotifyPath — mutates a scratch bd store,
//     drives one real poll pass per transition, and asserts the notify path
//     fires (via a recording `parlay` stub) for a `notify:<channel>`-labeled
//     close.
//  3. TestRobotWatchDaemonAliveCursorFresh — spawns a real `robots-watch`
//     daemon (the compiled binary, re-exec'd as a test subprocess) and
//     asserts it stays alive and keeps its cursor state fresh: the cursor
//     file keeps advancing and its age stays within a bounded window. No
//     wall-clock sleeps; the bound is a generous safety net around the steady
//     writes a live daemon maintains.
package robotswatch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ── Shared scaffolding ────────────────────────────────────────────────────────

// mustExec runs a command, failing the test on any error. Returns combined
// output.
func mustExec(t *testing.T, name string, args []string, dir string, env []string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
	return string(out)
}

// storeEnv returns an environment that pins bd to the given store.
func storeEnv(beadsDir, name string) []string {
	env := os.Environ()
	env = append(env, "BD_NAME="+name, "BEADS_DIR="+beadsDir)
	return env
}

// bdOnPath reports whether the real beads CLI is available on PATH. The
// integration body shells out to the real `bd` binary; hosts that only link
// the beads Go library (e.g. CI) skip the test rather than fail it — same
// convention as parlaybeads' opt-in TestRealStoreRoundTrip.
func bdOnPath() bool {
	_, err := exec.LookPath("bd")
	return err == nil
}

// initScratchStore creates a fresh bd disk store at dir with the given issue
// prefix and returns the path to its .beads dir.
func initScratchStore(t *testing.T, dir, prefix string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustExec(t, "bd", []string{"init", "-p", prefix}, dir, os.Environ())
	return filepath.Join(dir, ".beads")
}

// writeStoreStub installs an executable `name` into binDir that forwards every
// call to the real `bd` pinned to the given scratch store.
func writeStoreStub(t *testing.T, binDir, name, beadsDir string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nexport BEADS_DIR=%q\nexport BD_NAME=%q\nexec bd \"$@\"\n", beadsDir, name)
	if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeParlayStub installs a recording `parlay` into binDir: every invocation
// is appended to $PARLAY_RECORD, exiting 0.
func writeParlayStub(t *testing.T, binDir string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PARLAY_RECORD\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "parlay"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// listBeads shells `bd list --all --json --limit 0` against a scratch store.
func listBeads(t *testing.T, beadsDir, name, dir string) []Bead {
	t.Helper()
	out := mustExec(t, "bd", []string{"list", "--all", "--json", "--limit", "0"}, dir, storeEnv(beadsDir, name))
	var beads []Bead
	if err := json.Unmarshal([]byte(out), &beads); err != nil {
		t.Fatalf("parse %s list --json: %v\n%s", name, err, out)
	}
	return beads
}

// beadByTitle returns the id of the first listed bead whose title matches.
func beadByTitle(t *testing.T, beadsDir, name, dir, title string) string {
	t.Helper()
	for _, b := range listBeads(t, beadsDir, name, dir) {
		if b.Title == title {
			return b.ID
		}
	}
	t.Fatalf("bead %q not found in scratch store %s", title, name)
	return ""
}

// prependPath puts binDir at the front of PATH for this test.
func prependPath(t *testing.T, binDir string) {
	t.Helper()
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// waitFor polls cond until true or the timeout elapses. The timeout is a
// safety net, not an assertion window — the cond itself is the test.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// execEnv rebuilds the environment from os.Environ with the given overrides
// applied exactly once each (deduplicating any inherited duplicates).
func execEnv(overrides ...string) []string {
	seen := map[string]bool{}
	env := []string{}
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		k := kv[:i]
		if seen[k] {
			continue
		}
		seen[k] = true
		env = append(env, kv)
	}
	for _, kv := range overrides {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		seen[kv[:i]] = true // override wins by being the only occurrence
		env = append(env, kv)
	}
	return env
}

// sameKinds compares two kind lists as sets.
func sameKinds(a, b []EventKind) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[EventKind]bool{}
	for _, k := range a {
		m[k] = true
	}
	for _, k := range b {
		if !m[k] {
			return false
		}
	}
	return true
}

// ── Test 1: the intended watch table (red with ROBOTSWATCH_GAP_PROBE=1) ──────

func TestWatchedStoresIntendedTable(t *testing.T) {
	if os.Getenv("ROBOTSWATCH_GAP_PROBE") == "" {
		t.Skip("gap probe (robots-p0e0): run with ROBOTSWATCH_GAP_PROBE=1 to pin the intended watch table and prove the missing inbox row")
	}

	intended := []struct {
		store string
		kinds []EventKind
	}{
		{store: "robots", kinds: []EventKind{EventCreated, EventClosed}},
		{store: "questions", kinds: []EventKind{EventClosed}},
		{store: "task", kinds: []EventKind{EventClosed}},
		// inbox is a SEPARATE dolt database; without a watch row its beads
		// never appear in any polled store's list, so inbox opens/closes are
		// invisible and inbox close-notifications can never fire.
		{store: "inbox", kinds: []EventKind{EventCreated, EventClosed}},
	}

	byStore := map[string]*watch{}
	for i := range watches {
		w := watches[i]
		byStore[w.Store] = &w
	}

	for _, want := range intended {
		w, ok := byStore[want.store]
		if !ok {
			t.Errorf("GAP: watch table missing store %q (intended kinds %v) — %s beads are never polled\ncurrent table: %+v",
				want.store, want.kinds, want.store, watches)
			continue
		}
		if !sameKinds(w.Kinds, want.kinds) {
			t.Errorf("store %q watches kinds %v, intended %v\ncurrent table: %+v", want.store, w.Kinds, want.kinds, watches)
		}
	}
	if t.Failed() {
		t.Fatal("intended watch table not satisfied (GAP proven: see missing-store errors above)")
	}
}

// ── Test 2: scratch store close fires the notify path ─────────────────────────

func TestScratchStoreClosedBeadFiresNotifyPath(t *testing.T) {
	if !bdOnPath() {
		t.Skip("bd CLI not on PATH — integration drives the real beads binary through the poll loop")
	}

	stateHome := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", stateHome)

	binDir := t.TempDir()
	prependPath(t, binDir)

	record := filepath.Join(stateHome, "parlay-record.log")
	t.Setenv("PARLAY_RECORD", record)
	writeParlayStub(t, binDir)

	scratch := t.TempDir()
	beadsDir := initScratchStore(t, scratch, "task")

	// The poll daemon lists whatever `task` resolves to on PATH; point it at
	// the scratch store so no real store is touched.
	writeStoreStub(t, binDir, "task", beadsDir)

	// Watch ONLY the scratch task store for this test (created + closed).
	origWatches := watches
	watches = []watch{{Store: "task", Kinds: []EventKind{EventCreated, EventClosed}}}
	t.Cleanup(func() { watches = origWatches })

	title := "close-notify integration"
	mustExec(t, "bd", []string{"create", title}, scratch, storeEnv(beadsDir, "task"))
	id := beadByTitle(t, beadsDir, "task", scratch, title)

	// Pass A: first sighting seeds the cursor and fires nothing.
	runPollOnce(false)
	if data, err := os.ReadFile(record); err == nil && len(data) != 0 {
		t.Fatalf("seed pass must not notify; record=%q", data)
	}

	// Mutate the scratch store: subscribe a channel, then close the bead.
	mustExec(t, "bd", []string{"label", "add", id, "notify:mayor"}, scratch, storeEnv(beadsDir, "task"))
	mustExec(t, "bd", []string{"close", id}, scratch, storeEnv(beadsDir, "task"))

	// Pass B: the close must route to the notify handler and fire a send.
	runPollOnce(false)

	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("notify path never fired: no parlay send recorded: %v", err)
	}
	line := strings.TrimSpace(string(data))
	for _, want := range []string{"--mayor", id, "closed"} {
		if !strings.Contains(line, want) {
			t.Fatalf("notify send missing %q: got %q", want, line)
		}
	}
}

// ── Test 3: live daemon liveness + bounded cursor age ─────────────────────────

func TestRobotWatchDaemonAliveCursorFresh(t *testing.T) {
	if v := os.Getenv("GO_ROBOTSWATCH_DAEMON"); v == "1" {
		// Child branch: become the daemon against a scratch, isolated state
		// home and never return. The parent asserts on the state it keeps.
		CmdRobotsWatch([]string{"--interval", "0.1"})
		return // unreachable while the daemon loops
	}

	stateHome := t.TempDir()
	fakeHome := t.TempDir()

	// Isolate every store the daemon polls: shadowed CLIs fail fast so no
	// real bead store is ever listed (and no handler can ever fire).
	binDir := t.TempDir()
	for _, name := range []string{"robots", "questions", "task"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	env := execEnv(
		"HOME="+fakeHome,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PARLAY_STATE_HOME="+stateHome,
		"PARLAY_MECHANIC_DISPATCH=off",
		"GO_ROBOTSWATCH_DAEMON=1",
	)

	cmd := exec.Command(os.Args[0], "-test.run=^TestRobotWatchDaemonAliveCursorFresh$")
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	cursor := filepath.Join(stateHome, "robots-watch", "cursor.json")

	// The daemon must materialize its cursor: first write lands within the
	// bound, proving it survives startup.
	var firstMtime time.Time
	waitFor(t, 15*time.Second, func() bool {
		st, err := os.Stat(cursor)
		if err != nil || st.Size() == 0 {
			return false
		}
		firstMtime = st.ModTime()
		return true
	})

	// The cursor must keep ADVANCING while the daemon lives (a second write
	// past the first), and its age must stay bounded — freshly rewritten, not
	// a one-shot artifact. The 5s window is a safety bound around a ~100ms
	// write cycle, so it cannot flake; the advancing mtime is the assertion.
	waitFor(t, 15*time.Second, func() bool {
		st, err := os.Stat(cursor)
		if err != nil {
			return false
		}
		return st.ModTime().After(firstMtime) && time.Since(st.ModTime()) < 5*time.Second
	})

	// The daemon process must still be alive (not a zombie writing nothing).
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("daemon process not alive: %v", err)
	}
	st, err := os.Stat(cursor)
	if err != nil {
		t.Fatal(err)
	}
	age := time.Since(st.ModTime())
	if age > 10*time.Second {
		t.Fatalf("cursor age %s exceeds bound", age)
	}
	t.Logf("daemon alive; cursor rewritten; last write age %s (< 5s cycle)", age)
}
