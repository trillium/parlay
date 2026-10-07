// Tests for the on-disk agent-registry fallback shared by `parlay liveness`
// and `parlay explain`: the server did not answer, so its roster is read off
// disk (registry_file.go). Each test degrades exactly one thing, and the
// assertions are about WORDING as much as content — the fallback is only
// honest if it says it was used and says what it cannot answer.
package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/agentregistry"
)

// writeRoster puts a real agents.json in this test's private state home.
func writeRoster(t *testing.T, entries ...map[string]any) string {
	t.Helper()
	if entries == nil {
		entries = []map[string]any{}
	}
	body, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(os.Getenv("PARLAY_STATE_HOME"), agentregistry.FileName)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// notThisHost makes the locality gate answer "another machine" without a
// resolver, so the case "the CLI targets a host that is not this one" is
// testable deterministically.
func notThisHost(t *testing.T) {
	t.Helper()
	orig := servesThisHost
	servesThisHost = func(string) bool { return false }
	t.Cleanup(func() { servesThisHost = orig })
}

// TestLivenessFallsBackToTheDiskRosterWhenTheServerIsDown is the fleet-wide
// payoff: with the server dead the table used to be a column of "unknown".
// The roster on disk restores STATE (the process table is a LOCAL measurement)
// and SILENCE (the status files and the relay trail are local too) while
// leaving HEARTBEAT unknown — presence is never written to disk.
func TestLivenessFallsBackToTheDiskRosterWhenTheServerIsDown(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	path := writeRoster(t,
		map[string]any{"id": "crew-1", "name": "Crew One", "color": "#abc"},
		map[string]any{"id": "crew-2", "name": "Crew Two"},
	)
	fakeListeners(t, true, "crew-1")
	writeAgentStatus(t, f.home, "crew-1", "working: building the parser", 0)

	out, _, code, exited := livenessRun(t, nil)
	if exited {
		t.Fatalf("exited %d; the roster on disk answered, so this is not an empty fleet", code)
	}
	wantLine(t, out,
		sourceLine("registry + presence", "unreachable"),
		sourceLine("registry (disk)", "read"),
		"read "+path+" because the server did not answer",
		"registration below comes from DISK",
		"presence is never written to disk, so every heartbeat stays unknown",
	)
	// crew-1: listed on disk, a listener is running here, and its own status
	// file is fresh — live, measurably not silent.
	wantLine(t, rowFor(t, out, "crew-1"), "live", "unknown", "no")
	// crew-2: listed on disk with nothing listening. That is a ghost, and it is
	// a conclusion the LOCAL process table supports without the server.
	wantLine(t, rowFor(t, out, "crew-2"), "ghost", "unknown", "unknown")

	notes := out[strings.Index(out, "\nnotes\n"):]
	wantLine(t, notes,
		"on-disk registry",
		"presence is kept in memory by the server and is never written to disk",
	)
}

// TestLivenessDiskRosterAbsentIsNotAnEmptyFleet: no roster file is one more
// way for a source to be absent, and it must not read as "nobody is enrolled".
func TestLivenessDiskRosterAbsentIsNotAnEmptyFleet(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	fakeListeners(t, true)

	out, stderr, code, exited := livenessRun(t, nil)
	if !exited || code != 1 {
		t.Fatalf("exit = (%d, %v); want (1, true) — nothing at all was observable", code, exited)
	}
	wantLine(t, out,
		sourceLine("registry (disk)", "absent"),
		"no roster file at "+filepath.Join(os.Getenv("PARLAY_STATE_HOME"), agentregistry.FileName),
		"Not the same as 'no agent is registered'",
		"no agents to report",
	)
	wantLine(t, stderr, "nothing was observable")
}

// TestLivenessDiskRosterUnreadableIsUnknownNotEmpty: a roster that exists and
// cannot be parsed is the state that most tempts a reader into reporting an
// empty fleet. It stays unknown, and no agent is called offline off it.
func TestLivenessDiskRosterUnreadableIsUnknownNotEmpty(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	p := filepath.Join(os.Getenv("PARLAY_STATE_HOME"), agentregistry.FileName)
	if err := os.WriteFile(p, []byte("{ this is not a roster"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeListeners(t, true)
	writeAgentStatus(t, f.home, "crew-1", "working: alive", 0)

	out, _, code, exited := livenessRun(t, nil)
	if exited {
		t.Fatalf("exited %d; this host still has a record of its own", code)
	}
	wantLine(t, out,
		sourceLine("registry (disk)", "unreadable"),
		"what it holds is unknown, not empty",
	)
	row := rowFor(t, out, "crew-1")
	wantLine(t, row, "unknown", "unknown")
	// The row's own columns must not carry a verdict the failed read cannot
	// support (the note's "not the same as offline" wording is the opposite
	// claim, so this asserts on the row).
	notWantLine(t, row, "offline", "ghost")
}

// TestLivenessDoesNotBorrowAnotherHostsRoster: agents.json belongs to a HOST.
// A CLI pointed at another machine must not assemble a fleet out of this
// machine's file — and must say why it did not.
func TestLivenessDoesNotBorrowAnotherHostsRoster(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	notThisHost(t)
	writeRoster(t, map[string]any{"id": "crew-9", "name": "Someone Else's Agent"})
	fakeListeners(t, true)
	writeAgentStatus(t, f.home, "crew-2", "working: alive", 0)

	out, _, code, exited := livenessRun(t, nil)
	if exited {
		t.Fatalf("exited %d; this host's own records answered", code)
	}
	wantLine(t, out,
		sourceLine("registry (disk)", "not-this-host"),
		"is not that server's roster",
	)
	if strings.Contains(out, "crew-9") {
		t.Errorf("another host's roster leaked into this fleet:\n%s", out)
	}
	wantLine(t, rowFor(t, out, "crew-2"), "unknown")
}

// TestExplainRegistrationFromDiskWhenTheServerIsDown: one agent's story keeps
// working, and the substitution is named on the line. Registration may be
// answered from a file; the CHANNEL line may not — presence has no on-disk
// record at all, and conflating the two would invent a heartbeat.
func TestExplainRegistrationFromDiskWhenTheServerIsDown(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	path := writeRoster(t, map[string]any{"id": "crew-1", "name": "Crew One", "color": "#abc"})
	f.statusFile(t, "working: alive\n")

	out, _, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("exited %d; the roster on disk answered", code)
	}
	wantLine(t, out,
		"registration    listed in the roster the server last persisted to disk ("+path+") — name Crew One, color #abc",
		"whether it is registered RIGHT NOW is unknown; that file holds no heartbeat either",
		"channel         unknown — the server did not answer "+f.server,
	)
	notWantLine(t, out, "registration    registered")
	// The reconciled crew state keeps its frozen oracle: the LIVE registry, not
	// last night's file.
	wantLine(t, out, "crew state      working · source: status-degraded")
}

// TestExplainRegistrationFromDiskNotListedStaysUnsettled: the disk roster can
// say "this id is not in it", which is not the same claim as "not enrolled" —
// the state directory the server runs with is a separate configuration point.
func TestExplainRegistrationFromDiskNotListedStaysUnsettled(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	path := writeRoster(t, map[string]any{"id": "crew-2", "name": "Crew Two"})

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out,
		"registration    not in the roster the server last persisted to disk ("+path+")",
		"not a live answer",
	)
	notWantLine(t, out, "NOT in the registry")
}

// TestExplainRegistrationDiskRosterAbsent: absence of the fallback is named,
// and it is explicitly not the confident negative.
func TestExplainRegistrationDiskRosterAbsent(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	f.statusFile(t, "working: alive\n")

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out,
		"registration    unknown — the server did not answer "+f.server,
		"no roster file at "+filepath.Join(os.Getenv("PARLAY_STATE_HOME"), agentregistry.FileName),
		"that absence is not a 'not registered'",
	)
}

// TestExplainRegistrationDiskRosterUnreadable: the file is there and what it
// holds is unknown — never "empty".
func TestExplainRegistrationDiskRosterUnreadable(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	p := filepath.Join(os.Getenv("PARLAY_STATE_HOME"), agentregistry.FileName)
	if err := os.WriteFile(p, []byte("not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.statusFile(t, "working: alive\n")

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out,
		"registration    unknown — the server did not answer "+f.server+" and its roster file at "+p,
		"exists but could not be read",
		"what it holds is unknown, not empty",
	)
}

// TestExplainDoesNotReadAnotherHostsRoster: the file is only this server's when
// this machine is the server's machine, and the line says which it was.
func TestExplainDoesNotReadAnotherHostsRoster(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	notThisHost(t)
	writeRoster(t, map[string]any{"id": "crew-1", "name": "Stale Name From Another Machine"})
	f.statusFile(t, "working: alive\n")

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out,
		"registration    unknown — the server did not answer "+f.server,
		"was NOT consulted: the target is another machine",
	)
	notWantLine(t, out, "Stale Name From Another Machine")
}

// TestExplainLiveAnswerBeatsTheDiskRoster: the live registry is the oracle. A
// stale file must never override — or even appear beside — the server's own
// answer, exactly as the delivery trail behaves when the relay is up.
func TestExplainLiveAnswerBeatsTheDiskRoster(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	writeRoster(t, map[string]any{"id": "crew-1", "name": "Stale Name"})
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": subscribersBody("crew-2", "Crew Two", "#abc", ""),
	})

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out, "registration    NOT in the registry — the server answered and does not list it")
	notWantLine(t, out, "last persisted to disk", "registry (disk)")
}

// TestExplainDiskRosterIsAnObservableSource: the exit contract is about whether
// ANY source answered. A roster read off disk counts — with its caveat — so a
// half-broken fleet does not exit 1 on a run that plainly printed something.
func TestExplainDiskRosterIsAnObservableSource(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	writeRoster(t, map[string]any{"id": "crew-1", "name": "Crew One"})

	out, stderr, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("exited %d (%s); the roster on disk was read, so something was observable", code, stderr)
	}
	wantLine(t, out, "listed in the roster the server last persisted to disk")
}

// TestRegistryFallbackIsReadOnly: both verbs open the roster read-only, and the
// file is byte-identical afterwards. An observability surface that can rewrite
// the roster it reports on is not a diagnostic.
func TestRegistryFallbackIsReadOnly(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	path := writeRoster(t, map[string]any{"id": "crew-1", "name": "Crew One"})
	fakeListeners(t, true, "crew-1")
	f.statusFile(t, "working: alive\n")

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Both verbs, over the same file: neither may write to it.
	_, _, _, _ = explainRun(t, []string{"crew-1"})
	_, _, _, _ = livenessRun(t, nil)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the roster changed across two read-only runs:\nbefore: %s\nafter:  %s", before, after)
	}
}
