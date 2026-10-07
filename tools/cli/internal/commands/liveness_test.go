package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/liveness"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
)

// livenessRun is the shared verb capture (explain_test.go's verbRun), so this
// file cannot drift from explain's tests on pipe ownership.
func livenessRun(t *testing.T, argv []string) (stdout, stderr string, code int, exited bool) {
	t.Helper()
	return verbRun(t, Liveness, argv)
}

// fakeListeners drives the process-table probe. A unit test cannot arm a real
// listener, and the probe's own failure mode (known=false) is a case these
// tests must be able to produce deliberately.
func fakeListeners(t *testing.T, known bool, ids ...string) {
	t.Helper()
	orig := liveListeners
	liveListeners = func() (map[string]bool, bool) {
		m := map[string]bool{}
		for _, id := range ids {
			m[id] = true
		}
		return m, known
	}
	t.Cleanup(func() { liveListeners = orig })
}

// subAgent is one agent in the fixture server's snapshot.
type subAgent struct {
	id   string
	name string
	// lastSeen "" = no presence row at all; "none" = a row with no lastSeen
	// (the server's own "never observed" shape); anything else = the stamp.
	lastSeen string
}

func livenessSubs(agents ...subAgent) map[string]any {
	list := make([]map[string]any, 0, len(agents))
	presence := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		list = append(list, map[string]any{"id": a.id, "name": a.name, "color": "#abc"})
		switch a.lastSeen {
		case "":
		case "none":
			presence = append(presence, map[string]any{"channel": a.id})
		default:
			presence = append(presence, map[string]any{"channel": a.id, "lastSeen": a.lastSeen})
		}
	}
	return map[string]any{
		"registered": map[string]any{"count": len(list), "agents": list},
		"presence":   presence,
	}
}

// writeAgentStatus writes one agent's status file and backdates its mtime, which is
// the only clock the fleet view has for "when did the agent last say this".
func writeAgentStatus(t *testing.T, home, agent, line string, age time.Duration) {
	t.Helper()
	dir := filepath.Join(home, agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "status")
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		when := time.Now().Add(-age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
}

// writeLedger puts a real delivery trail on disk, which is what lets the
// fleet view answer with the relay dead.
func writeLedger(t *testing.T, runtime string, entries ...relayctl.DeliveryEntry) {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteString("\n")
	}
	if err := os.WriteFile(relayctl.LedgerPath(), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// rowFor returns one agent's table line. Notes lines are indented, so a match
// here is always the table row.
func rowFor(t *testing.T, out, agent string) string {
	t.Helper()
	prefix := format.PadEnd(agent, livenessAgentCol)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no table row for %q\n---\n%s", agent, out)
	return ""
}

func ago(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }

// sourceLine is one line of the sources block, built from the same padding the
// renderer uses, so a width change breaks these assertions loudly instead of
// leaving them vacuously true.
func sourceLine(name, state string) string { return format.PadEnd(name, 22) + " " + state }

// noteLine is the prefix of one notes-section line, built from the renderer's
// own padding for the same reason sourceLine is.
func noteLine(agent, aspect string) string {
	return "  " + format.PadEnd(agent, livenessAgentCol) + " " + format.PadEnd(aspect, 10)
}

// TestLivenessFleetTable is the whole point of the verb: four agents, four
// different reasons to look at them, one table — including the false-alarm
// case (crew-4) where the channel stamp has expired but the agent is working.
func TestLivenessFleetTable(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(
			subAgent{id: "crew-1", name: "Crew One", lastSeen: ago(30 * time.Second)},
			subAgent{id: "crew-2", name: "Crew Two", lastSeen: "none"},
			subAgent{id: "crew-3", name: "Crew Three"},
			subAgent{id: "crew-4", name: "Crew Four", lastSeen: ago(3 * time.Hour)},
		),
	})
	fakeListeners(t, true, "crew-1", "crew-2", "crew-4")
	f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1", "crew-2", "crew-4"}, nil)
	writeAgentStatus(t, f.home, "crew-1", "working [key=t]: building the parser", 2*time.Minute)
	writeAgentStatus(t, f.home, "crew-3", "blocked: waiting on the captain", 3*time.Hour)
	writeAgentStatus(t, f.home, "crew-4", `working [key=p]: running the suite`, 20*time.Second)
	writeLedger(t, f.runtime,
		relayctl.DeliveryEntry{Ts: ago(4 * time.Hour), Event: "spooled", Agent: "crew-3", Msg: "m-1", Role: "user"},
	)

	out, _, code, exited := livenessRun(t, nil)
	if exited {
		t.Fatalf("Liveness exited with code %d; want none", code)
	}
	wantLine(t, out,
		"parlay liveness — 4 agent(s) in the fleet · silence window 10m",
		"server          "+f.server,
		"AGENT",
		"STATE",
		"HEARTBEAT",
		"SILENT",
		"LAST OBSERVED ACTIVITY",
		"1 of 4 silent beyond 10m · 2 with no heartbeat record (never observed or no presence row)",
	)
	// crew-1: healthy, and the newest record is the channel stamp, not the
	// status file two minutes behind it.
	row1 := rowFor(t, out, "crew-1")
	wantLine(t, row1, "live", "fresh (30s ago)", "no", "channel activity 30s ago")
	// crew-2: a presence row with NO stamp. That is absence, never staleness.
	row2 := rowFor(t, out, "crew-2")
	wantLine(t, row2, "live", "never observed", "unknown", "no dated record")
	notWantLine(t, row2, "expired", "0s")
	// crew-3: registered, nothing listening, no presence row, and the last
	// thing anyone saw was a relay hand-over four hours ago.
	row3 := rowFor(t, out, "crew-3")
	wantLine(t, row3, "ghost", "no row", "3h00m", `status "blocked" 3h00m ago`)
	// crew-4: the channel stamp expired three hours ago, but the agent wrote a
	// status line 20 seconds ago. An expired heartbeat is NOT a silent agent.
	row4 := rowFor(t, out, "crew-4")
	wantLine(t, row4, "live", "expired (3h00m ago)", "no", `status "working" 20s ago`)

	// The notes section explains the rows that need it, and only those.
	notesAt := strings.Index(out, "notes")
	if notesAt < 0 {
		t.Fatalf("no notes section\n---\n%s", out)
	}
	notes := out[notesAt:]
	wantLine(t, notes,
		noteLine("crew-2", "heartbeat"),
		noteLine("crew-3", "state"),
		noteLine("crew-3", "heartbeat"),
		"never observed",
		"absent, not expired",
		"spooled for a reader that is gone",
	)
	notWantLine(t, notes, noteLine("crew-1", "heartbeat"), noteLine("crew-1", "state"), noteLine("crew-4", "silence"))

	// Every source answered here, and each is named with what it answered.
	wantLine(t, out,
		sourceLine("registry + presence", "read"),
		sourceLine("process table", "read"),
		sourceLine("relay", "read"),
		"up — polling "+f.server,
		sourceLine("delivery ledger", "read"),
	)
}

// TestLivenessSilentFilter keeps --silent to the two ways a record can say
// "not answering": silence past the window, or no heartbeat record at all.
func TestLivenessSilentFilter(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(
			subAgent{id: "crew-1", lastSeen: ago(30 * time.Second)},
			subAgent{id: "crew-2", lastSeen: "none"},
			subAgent{id: "crew-3", lastSeen: ago(3 * time.Hour)},
		),
	})
	fakeListeners(t, true, "crew-1", "crew-2", "crew-3")

	out, _, code, exited := livenessRun(t, []string{"--silent"})
	if exited {
		t.Fatalf("Liveness exited with code %d; want none", code)
	}
	rowFor(t, out, "crew-2")
	rowFor(t, out, "crew-3")
	if strings.Contains(out, format.PadEnd("crew-1", livenessAgentCol)) {
		t.Errorf("--silent must hide a fresh agent\n---\n%s", out)
	}
	// The window is a flag, so the same records answer a tighter question.
	tight, _, _, _ := livenessRun(t, []string{"--silent", "--silent-for", "10s"})
	rowFor(t, tight, "crew-1")
}

// TestLivenessAbsentHeartbeatIsNotExpired is the distinction the objective
// names: a row that exists with no stamp must not read as a stale heartbeat,
// and must not produce a computed age.
func TestLivenessAbsentHeartbeatIsNotExpired(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(
			subAgent{id: "crew-1", lastSeen: "none"},
			subAgent{id: "crew-2"},
		),
	})
	fakeListeners(t, true, "crew-1", "crew-2")

	out, _, _, _ := livenessRun(t, nil)
	row1 := rowFor(t, out, "crew-1")
	wantLine(t, row1, "never observed")
	notWantLine(t, row1, "expired", "0s", "1s")
	row2 := rowFor(t, out, "crew-2")
	wantLine(t, row2, "no row")
	notWantLine(t, row2, "expired", "0s")
	wantLine(t, out, "2 with no heartbeat record (never observed or no presence row)")
}

// TestLivenessRelayDownReadsTheTrail: the relay is not running, so its live
// state is unknown — but its delivery trail is a FILE, so the last activity is
// still answerable, and the source line says which half was read.
func TestLivenessRelayDownReadsTheTrail(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(subAgent{id: "crew-1"}),
	})
	fakeListeners(t, true, "crew-1")
	writeLedger(t, f.runtime,
		relayctl.DeliveryEntry{Ts: ago(90 * time.Minute), Event: "spool-failed", Agent: "crew-1", Msg: "m-9"},
	)

	out, _, code, exited := livenessRun(t, nil)
	if exited {
		t.Fatalf("Liveness exited with code %d; want none", code)
	}
	wantLine(t, out,
		sourceLine("relay", "unreachable"),
		"the relay is not running",
		sourceLine("delivery ledger", "read"),
		"relay spool-failed 1h30m ago",
	)
	rowFor(t, out, "crew-1")
}

// TestLivenessServerDownKeepsLocalActivity is the half-broken-fleet case: the
// server is gone, so registration is UNKNOWN (never "offline"), yet the local
// status file still dates the agent and the exit code stays 0.
func TestLivenessServerDownKeepsLocalActivity(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	fakeListeners(t, true)
	writeAgentStatus(t, f.home, "crew-1", "working: still going", 5*time.Minute)

	out, stderr, code, exited := livenessRun(t, nil)
	if exited {
		t.Fatalf("Liveness exited with code %d; want none — a local record answered", code)
	}
	row := rowFor(t, out, "crew-1")
	wantLine(t, row, "unknown", "unknown", "5m", `status "working" 5m ago`)
	notWantLine(t, row, "offline")
	wantLine(t, out,
		sourceLine("registry + presence", "unreachable"),
		"registration and channel activity are UNKNOWN, not absent",
		"the server did not answer, so registration is unknown — this is not the same as offline",
		"the server did not answer, so channel activity is unknown (not absent, and not fresh)",
	)
	notWantLine(t, stderr, "nothing was observable")
}

// TestLivenessNothingObservable: no server, no relay, no agent home. The verb
// must refuse to report an empty healthy fleet.
func TestLivenessNothingObservable(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	fakeListeners(t, true)

	out, stderr, code, exited := livenessRun(t, nil)
	if !exited || code != ExitLivenessNothing {
		t.Fatalf("exit = (%d, %v), want (%d, true)", code, exited, ExitLivenessNothing)
	}
	wantLine(t, out, "no agents to report")
	wantLine(t, stderr, "parlay liveness: nothing was observable")
}

// TestLivenessProcessTableUnreadable: a failed probe must not become "ghost".
func TestLivenessProcessTableUnreadable(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(subAgent{id: "crew-1", lastSeen: ago(2 * time.Minute)}),
	})
	fakeListeners(t, false)

	out, _, code, exited := livenessRun(t, nil)
	if exited {
		t.Fatalf("Liveness exited with code %d; want none", code)
	}
	row := rowFor(t, out, "crew-1")
	wantLine(t, row, "live")
	notWantLine(t, row, "ghost")
	wantLine(t, out,
		sourceLine("process table", "unreadable"),
		"a listener cannot be confirmed OR ruled out",
	)
}

// TestLivenessRelayBoundElsewhere is the registered-but-deaf tell: the relay
// answers, but polls a different chat server, so its trail describes someone
// else's traffic.
func TestLivenessRelayBoundElsewhere(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(subAgent{id: "crew-1"}),
	})
	fakeListeners(t, true, "crew-1")
	f.relay(t, relayctl.Health{OK: true, Server: "http://127.0.0.1:1", Runtime: f.runtime}, []string{"crew-1"}, nil)

	out, _, _, _ := livenessRun(t, nil)
	wantLine(t, out, "WARNING this relay polls http://127.0.0.1:1, NOT the server this CLI targets ("+f.server+")")
}

// TestLivenessJSONEnvelope pins the machine-readable form: the closed
// vocabularies travel verbatim and every unknown carries its note.
func TestLivenessJSONEnvelope(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(
			subAgent{id: "crew-1", lastSeen: ago(3 * time.Hour)},
			subAgent{id: "crew-2", lastSeen: "none"},
		),
	})
	// No listener for either agent: the JSON must carry the ghost state and the
	// absence vocabulary, not just the healthy shapes.
	fakeListeners(t, true)
	f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1"}, nil)

	out, _, _, _ := livenessRun(t, []string{"--json"})
	var got struct {
		Server    string `json:"server"`
		SilentFor string `json:"silent_for"`
		Answered  bool   `json:"answered"`
		Agents    []struct {
			Agent         string  `json:"agent"`
			State         string  `json:"state"`
			StateNote     string  `json:"state_note"`
			Heartbeat     string  `json:"heartbeat"`
			HeartbeatAt   string  `json:"heartbeat_at"`
			Silence       string  `json:"silence"`
			SilentForSecs float64 `json:"silent_for_seconds"`
			SilentSince   string  `json:"silent_since"`
			LastSource    string  `json:"last_source"`
			RelayKnown    bool    `json:"relay_known"`
			RelayEnrolled bool    `json:"relay_enrolled"`
		} `json:"agents"`
		Sources []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json is not valid JSON: %v\n---\n%s", err, out)
	}
	if got.Server != f.server || got.SilentFor != "10m" || !got.Answered {
		t.Errorf("envelope header = %+v", got)
	}
	if len(got.Agents) != 2 || len(got.Sources) == 0 {
		t.Fatalf("agents=%d sources=%d, want 2 and at least one", len(got.Agents), len(got.Sources))
	}
	a := got.Agents[0]
	if a.Agent != "crew-1" || a.State != liveness.StateGhost {
		t.Errorf("crew-1 row = %+v, want ghost (no listener was faked for it)", a)
	}
	if !strings.Contains(a.StateNote, "nothing listening") {
		t.Errorf("crew-1 state note = %q, want the ghost explanation", a.StateNote)
	}
	if a.Heartbeat != liveness.HeartbeatExpired || a.Silence != liveness.SilenceExpired {
		t.Errorf("crew-1 heartbeat/silence = %q/%q", a.Heartbeat, a.Silence)
	}
	if a.HeartbeatAt == "" || a.SilentSince == "" || a.SilentForSecs < 10700 || a.SilentForSecs > 10900 {
		t.Errorf("crew-1 stamps = %+v", a)
	}
	if a.LastSource != liveness.SourceChannel {
		t.Errorf("crew-1 last source = %q, want %q", a.LastSource, liveness.SourceChannel)
	}
	if !a.RelayKnown || !a.RelayEnrolled {
		t.Errorf("relay answer missing: known=%v enrolled=%v", a.RelayKnown, a.RelayEnrolled)
	}
	// crew-2's absence must be a vocabulary word, not an empty string.
	if got.Agents[1].Heartbeat != liveness.HeartbeatNeverObserved || got.Agents[1].Silence != liveness.SilenceUnknown {
		t.Errorf("crew-2 row = %+v", got.Agents[1])
	}
}

// TestLivenessIsReadOnly: the verb may only GET, and must leave every record
// it read byte-identical. Observability that can mutate what it observes is
// not a diagnostic.
func TestLivenessIsReadOnly(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(subAgent{id: "crew-1", lastSeen: ago(time.Minute)}),
	})
	fakeListeners(t, true, "crew-1")
	writeAgentStatus(t, f.home, "crew-1", "working: building", 0)
	f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1"}, nil)
	writeLedger(t, f.runtime, relayctl.DeliveryEntry{Ts: ago(time.Minute), Event: "spooled", Agent: "crew-1", Msg: "m-1"})

	before := map[string]string{}
	for _, p := range []string{relayctl.LedgerPath(), statusFileForAgent("crew-1")} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		before[p] = string(b)
	}
	if _, _, code, exited := livenessRun(t, nil); exited {
		t.Fatalf("Liveness exited with code %d; want none", code)
	}
	if !f.relaySawOnlyGets() {
		t.Errorf("liveness made a non-GET request to the relay: %v", f.relayMethods)
	}
	for p, want := range before {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want {
			t.Errorf("%s was modified by a read-only verb", p)
		}
	}
}

func TestLivenessUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"two positionals", []string{"crew-1", "crew-2"}},
		{"conflicting ids", []string{"crew-1", "--agent", "crew-2"}},
		{"bogus window", []string{"--silent-for", "soon"}},
		{"zero window", []string{"--silent-for", "0s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code, exited := livenessRun(t, tc.argv)
			if !exited || code != 2 {
				t.Fatalf("exit = (%d, %v), want (2, true)", code, exited)
			}
			if !strings.Contains(stderr, "parlay liveness") {
				t.Errorf("stderr = %q", stderr)
			}
		})
	}
}

// TestLivenessSingleAgentFilter: a bare id is a filter, and an id nothing
// knows about is still reported as unknown rather than dropped.
func TestLivenessSingleAgentFilter(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(subAgent{id: "crew-1", lastSeen: ago(time.Minute)}),
	})
	fakeListeners(t, true, "crew-1")

	out, _, code, exited := livenessRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("Liveness exited with code %d; want none", code)
	}
	rowFor(t, out, "crew-1")
	if strings.Contains(out, "crew-2") {
		t.Errorf("filter leaked another agent\n---\n%s", out)
	}

	ghostID, _, code, exited := livenessRun(t, []string{"nobody-here"})
	if exited {
		t.Fatalf("an unknown id must still be reported, exit was %d", code)
	}
	row := rowFor(t, ghostID, "nobody-here")
	wantLine(t, row, "offline", "no row")
}
