package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// explainRun captures the three things an operator sees — stdout, stderr, and
// the exit code — in one call.
//
// It does NOT build on captureStdout: httpc.Die panics through a test double,
// and a panic unwinding through captureStdout's body skips its final read and
// leaks its reader goroutine. Owning both pipes here keeps an exiting test
// from leaving a goroutine behind (CI runs with the race detector).
func explainRun(t *testing.T, argv []string) (stdout, stderr string, code int, exited bool) {
	t.Helper()
	return verbRun(t, Explain, argv)
}

// verbRun is the same capture for any verb — one implementation, so a second
// verb's tests cannot drift from the first's on the pipe ownership that keeps
// -race clean.
func verbRun(t *testing.T, fn func([]string), argv []string) (stdout, stderr string, code int, exited bool) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	origExit := httpc.Exit
	httpc.Exit = testsupport.RecordingExit()

	outCh := make(chan string, 1)
	errCh := make(chan string, 1)
	go func() { var b bytes.Buffer; io.Copy(&b, outR); outCh <- b.String() }()
	go func() { var b bytes.Buffer; io.Copy(&b, errR); errCh <- b.String() }()

	code, exited = testsupport.Capture(func() { fn(argv) })

	os.Stdout, os.Stderr = origOut, origErr
	httpc.Exit = origExit
	outW.Close()
	errW.Close()
	return <-outCh, <-errCh, code, exited
}

// noSleep makes the relay-lookup retries instant, the same seam crew_state
// tests use. Without it every "server unreachable" case costs ~10s of real
// sleeping (3 attempts × 3s timeout + backoff).
func noSleep(t *testing.T) {
	t.Helper()
	orig := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { sleep = orig })
}

// explainFixture is an isolated agent home + a short relay runtime dir + a
// fake chat server. Short matters for the runtime dir: a unix socket path has
// a ~104-byte limit and t.TempDir() alone is already ~60 bytes on macOS.
type explainFixture struct {
	agent   string
	home    string
	runtime string
	server  string

	// relayMu guards relayMethods: the control-socket handlers run on the
	// server's goroutines, so a bare append here would be a data race under
	// -race even though the requests have all completed by assertion time.
	relayMu      sync.Mutex
	relayMethods []string
}

func newExplainFixture(t *testing.T, agent string) *explainFixture {
	t.Helper()
	testsupport.TempStateHome(t)
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", home)

	runtime, err := os.MkdirTemp("", "pex")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(runtime) })
	t.Setenv(relayctl.RuntimeEnv, runtime)
	t.Setenv(relayctl.SockEnv, "")

	return &explainFixture{agent: agent, home: home, runtime: runtime}
}

// server starts a fake chat server from the given route values and points the
// CLI at it. Call with no routes for an "up but useless" server; use
// deadServer for an unreachable one.
func (f *explainFixture) serverWith(t *testing.T, routes map[string]any) {
	t.Helper()
	f.server = testsupport.JSONServer(t, routes).URL
	t.Setenv("PARLAY_SERVER", f.server)
}

// deadServer points the CLI at a port nothing is listening on.
func (f *explainFixture) deadServer(t *testing.T) {
	t.Helper()
	f.server = "http://127.0.0.1:1"
	t.Setenv("PARLAY_SERVER", f.server)
}

func (f *explainFixture) statusFile(t *testing.T, line string) {
	t.Helper()
	dir := filepath.Join(f.home, f.agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *explainFixture) sessionStart(t *testing.T, at time.Time) {
	t.Helper()
	dir := filepath.Join(f.home, f.agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := strconv.FormatInt(at.Unix(), 10)
	if err := os.WriteFile(filepath.Join(dir, "session-start"), []byte(stamp), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ageStatusFile backdates the status file's mtime, which is the only clock
// explain has for "when did the agent last say this".
func (f *explainFixture) ageStatusFile(t *testing.T, age time.Duration) {
	t.Helper()
	p := filepath.Join(f.home, f.agent, "status")
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
}

func (f *explainFixture) spool(t *testing.T, lines ...string) {
	t.Helper()
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(relayctl.SpoolPath(f.agent), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// relay serves the relay's read-only control socket in this runtime dir.
// delivery == nil means /delivery 404s, which is exactly what a relay built
// before the ledger existed answers.
func (f *explainFixture) relay(t *testing.T, health relayctl.Health, agents []string, delivery *relayctl.Delivery) {
	t.Helper()
	// note records the method of every control-socket request: the relay also
	// serves POST /register and POST /unregister, and explain must be provably
	// unable to reach them.
	note := func(r *http.Request) {
		f.relayMu.Lock()
		defer f.relayMu.Unlock()
		f.relayMethods = append(f.relayMethods, r.Method)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		writeRelayJSON(t, w, health)
	})
	mux.HandleFunc("/agents", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		writeRelayJSON(t, w, relayctl.Agents{Agents: agents, Server: health.Server, Runtime: f.runtime})
	})
	mux.HandleFunc("/delivery", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		if delivery == nil {
			http.NotFound(w, r)
			return
		}
		writeRelayJSON(t, w, *delivery)
	})
	ln, err := net.Listen("unix", filepath.Join(f.runtime, "relay.sock"))
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

// relaySawOnlyGets reports whether every control-socket request explain made
// was a GET.
func (f *explainFixture) relaySawOnlyGets() bool {
	f.relayMu.Lock()
	defer f.relayMu.Unlock()
	for _, m := range f.relayMethods {
		if m != http.MethodGet {
			return false
		}
	}
	return true
}

func writeRelayJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

// subscribersBody is the /api/chat/subscribers payload for one registered
// agent with an optional presence row ("", "none", or an RFC3339 stamp).
func subscribersBody(agentID, name, color, lastSeen string) map[string]any {
	subs := map[string]any{
		"registered": map[string]any{
			"count": 1,
			"agents": []map[string]any{
				{"id": agentID, "name": name, "color": color},
			},
		},
	}
	switch lastSeen {
	case "none":
		subs["presence"] = []map[string]any{{"channel": agentID}}
	case "":
		// no presence block at all
	default:
		subs["presence"] = []map[string]any{{"channel": agentID, "lastSeen": lastSeen}}
	}
	return subs
}

func commandsBody(list ...wire.CommandInvocation) map[string]any {
	if list == nil {
		list = []wire.CommandInvocation{}
	}
	return map[string]any{"ok": true, "now": time.Now().UTC().Format(time.RFC3339), "running": 0, "staleAfterMs": 90000, "commands": list}
}

func wantLine(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func notWantLine(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(out, u) {
			t.Errorf("output must NOT contain %q\n---\n%s", u, out)
		}
	}
}

// TestExplainHealthyStory is the full picture: every source answers and the
// whole story is on one screen, including a relay `spooled` entry rendered in
// the ledger's own vocabulary (the append, never "delivered") and a
// spool-failed entry promoted into the last-error line.
func TestExplainHealthyStory(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	seen := time.Now().Add(-12 * time.Minute).UTC().Truncate(time.Second)
	cmdStart := time.Now().Add(-12 * time.Minute).UTC().Format(time.RFC3339)
	cmdEnd := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", seen.Format(time.RFC3339)),
		"/api/chat/commands": commandsBody(
			wire.CommandInvocation{ID: "c1", Verb: "listen", Agent: "crew-1", State: "running", StartedAt: cmdStart, DurationMs: 720000},
			wire.CommandInvocation{ID: "c2", Verb: "status", Agent: "crew-1", State: "exited", StartedAt: cmdStart, EndedAt: cmdEnd, ExitCode: intPtr(0), Outcome: "ok", DurationMs: 40},
			wire.CommandInvocation{ID: "c3", Verb: "send", Agent: "crew-1", State: "failed", StartedAt: cmdStart, EndedAt: cmdEnd, ExitCode: intPtr(1), Outcome: "error", DurationMs: 1200},
			wire.CommandInvocation{ID: "c4", Verb: "spawn", Agent: "other", State: "running", StartedAt: cmdStart},
		),
	})
	spoolLines := 1
	f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1"}, &relayctl.Delivery{
		OK: true, Enabled: true, Exists: true, Ledger: relayctl.LedgerPath(), Count: 3,
		Entries: []relayctl.DeliveryEntry{
			{Ts: cmdStart, Event: "spooled", Agent: "crew-1", Msg: "m-1", Role: "user"},
			{Ts: cmdEnd, Event: "spool-failed", Agent: "crew-1", Msg: "m-3"},
			{Ts: cmdEnd, Event: "delivery-ended", Agent: "crew-1", Reason: "channel-gone", SpoolLines: &spoolLines},
		},
	})
	f.spool(t, "CHAT_MSG|m-1|user|hello", "CHAT_MSG|m-2|agent|working")
	f.statusFile(t, "working [key=t]: building the parser\n")
	f.sessionStart(t, time.Now().Add(-3*time.Hour))

	out, _, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("Explain exited with code %d; want none", code)
	}
	wantLine(t, out,
		"explain crew-1",
		"registration    registered — name Crew One, color #abc",
		"channel         last observed 12m ago",
		"crew state      working · source: status · building the parser",
		"status file     working [key=t]: building the parser",
		"pane age        started ",
		"relay           up — polling "+f.server,
		"relay enroll    polling this agent",
		"2 line(s) queued",
		"unconfirmed-consumed",
		"; resume cursor m-2",
		"delivery        3 of the last 20 ledger event(s), oldest first:",
		"spooled msg m-1 role=user",
		"SPOOL FAILED for msg m-3 — it did not reach the agent",
		"delivery ended — reason=channel-gone spoolLines=1",
		"commands        3 record(s) for this agent, newest first:",
		"failed   send             ended 2m ago · took 1.2s · exit 1 error",
		"last error      `parlay send` ended failed exit 1 outcome error",
	)
	// The agent's own commands only: the other agent's record must not appear,
	// and a healthy story must not carry the mismatch warning.
	notWantLine(t, out, "parlay spawn", "WARNING", "unknown —")
	if !f.relaySawOnlyGets() {
		t.Errorf("explain made a non-GET request to the relay: %v", f.relayMethods)
	}
}

func intPtr(n int) *int { return &n }

// TestExplainWarningWhenRelayPollsAnotherServer pins the registered-but-deaf
// tell: the relay answering is not enough, it has to be answering for THIS
// server.
func TestExplainWarningWhenRelayPollsAnotherServer(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", time.Now().UTC().Format(time.RFC3339)),
		"/api/chat/commands":    commandsBody(),
	})
	f.relay(t, relayctl.Health{OK: true, Server: "http://somewhere-else:4242", Runtime: f.runtime}, []string{"crew-1"}, nil)

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out, "WARNING: this relay polls http://somewhere-else:4242, NOT the server this CLI targets ("+f.server+")")
}

// TestExplainRelayDownStillCoversTheServerHalf is degraded mode 1: the relay
// is not running, and every relay-derived answer says unknown while the
// server-side and local answers still print.
func TestExplainRelayDownStillCoversTheServerHalf(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", time.Now().UTC().Format(time.RFC3339)),
		"/api/chat/commands":    commandsBody(wire.CommandInvocation{ID: "c1", Verb: "status", Agent: "crew-1", State: "exited", StartedAt: time.Now().UTC().Format(time.RFC3339), ExitCode: intPtr(0), Outcome: "ok"}),
	})
	f.statusFile(t, "working: alive\n")
	f.spool(t, "CHAT_MSG|m-1|user|queued while nobody was listening")
	// No relay started: no socket in this runtime dir.

	out, _, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("exited %d; a half-broken fleet is exactly when this command must work", code)
	}
	wantLine(t, out,
		"relay           no answer at "+relayctl.SockPath(),
		"relay enroll    unknown — the relay did not answer GET /agents",
		"delivery        unknown — the relay did not answer",
		"registration    registered — name Crew One",
		"crew state      working · source: status · alive",
		"1 line(s) queued",
		"; resume cursor m-1",
		"no relay is answering, so these lines have no writer",
	)
}

// TestExplainServerUnreachableNamesEveryUnknown is degraded mode 2. The point
// of the assertions about wording is that a failed read must not be rendered
// as a confident negative — "not registered either" would be exactly the
// collapse of unknown into a fact this command exists to prevent.
func TestExplainServerUnreachableNamesEveryUnknown(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	f.relay(t, relayctl.Health{OK: true, Server: "http://macbook:31337", Runtime: f.runtime}, nil, nil)

	out, _, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("exited %d; the relay answered, so this is not a nothing-observable run", code)
	}
	wantLine(t, out,
		"registration    unknown — the server did not answer "+f.server,
		"channel         unknown — the server did not answer "+f.server,
		"commands        unknown — the server did not answer /api/chat/commands",
		"relay enroll    NOT polled by this relay — whether the server registry lists it is unknown",
	)
	notWantLine(t, out, "not registered with the server either", "not registered either")
}

// TestExplainLastSeenMissingVsStale is the liveness distinction the objective
// calls out: a never-observed channel and an expired one are different facts
// and must not collapse into one line.
func TestExplainLastSeenMissingVsStale(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		noSleep(t)
		f := newExplainFixture(t, "crew-1")
		old := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
		f.serverWith(t, map[string]any{
			"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", old.Format(time.RFC3339)),
			"/api/chat/commands":    commandsBody(),
		})
		out, _, _, _ := explainRun(t, []string{"crew-1"})
		wantLine(t, out, "channel         last observed 2.0h ago ("+old.Format(time.RFC3339)+")")
	})
	t.Run("never observed", func(t *testing.T) {
		noSleep(t)
		f := newExplainFixture(t, "crew-1")
		f.serverWith(t, map[string]any{
			"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", "none"),
			"/api/chat/commands":    commandsBody(),
		})
		out, _, _, _ := explainRun(t, []string{"crew-1"})
		wantLine(t, out, "lastSeen absent — the server has never observed activity on this channel")
	})
	t.Run("no row at all", func(t *testing.T) {
		noSleep(t)
		f := newExplainFixture(t, "crew-1")
		f.serverWith(t, map[string]any{
			"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", ""),
			"/api/chat/commands":    commandsBody(),
		})
		out, _, _, _ := explainRun(t, []string{"crew-1"})
		wantLine(t, out, "no presence row in the server's snapshot")
	})
}

// TestExplainLedgerStates covers the three ways a ledger can answer without
// having entries, each of which must print as itself rather than as an empty
// list.
func TestExplainLedgerStates(t *testing.T) {
	cases := []struct {
		name     string
		delivery relayctl.Delivery
		want     string
	}{
		{
			name:     "recording switched off",
			delivery: relayctl.Delivery{OK: true, Enabled: false, Exists: true, Ledger: "/rt/delivery.log"},
			want:     "recording is OFF in the running relay",
		},
		{
			name:     "ledger never written",
			delivery: relayctl.Delivery{OK: true, Enabled: true, Exists: false, Ledger: "/rt/delivery.log"},
			want:     "no ledger at /rt/delivery.log",
		},
		{
			name:     "ledger present but quiet for this agent",
			delivery: relayctl.Delivery{OK: true, Enabled: true, Exists: true, Ledger: "/rt/delivery.log"},
			want:     "ledger present (/rt/delivery.log), no events for this agent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			noSleep(t)
			f := newExplainFixture(t, "crew-1")
			f.serverWith(t, map[string]any{
				"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", time.Now().UTC().Format(time.RFC3339)),
				"/api/chat/commands":    commandsBody(),
			})
			d := tc.delivery
			f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1"}, &d)
			out, _, _, _ := explainRun(t, []string{"crew-1"})
			wantLine(t, out, tc.want)
		})
	}
}

// TestExplainRotatedLedgerSaysHistoryIsLossy: a trail that starts at a rotated
// marker must say so, because it is otherwise indistinguishable from a quiet
// fleet.
func TestExplainRotatedLedgerSaysHistoryIsLossy(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", time.Now().UTC().Format(time.RFC3339)),
		"/api/chat/commands":    commandsBody(),
	})
	f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1"}, &relayctl.Delivery{
		OK: true, Enabled: true, Exists: true, Ledger: "/rt/delivery.log", Count: 1,
		Entries: []relayctl.DeliveryEntry{{Ts: time.Now().UTC().Format(time.RFC3339), Event: "rotated", Reason: "size-cap"}},
	})
	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out, "ledger rotated (size-cap) — history before this line lives in delivery.log.1")
}

// TestExplainDroppedCommandReadsAsWedged is degraded mode 3: an agent whose
// command stopped heartbeating. The registry marks it `dropped`, and both the
// command row and the last-error line must carry that.
func TestExplainDroppedCommandReadsAsWedged(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339)),
		"/api/chat/commands": commandsBody(wire.CommandInvocation{
			ID: "c1", Verb: "spawn", Agent: "crew-1", State: "dropped",
			StartedAt: time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339),
			Outcome:   "no-heartbeat", DurationMs: 300000,
		}),
	})
	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out,
		"dropped  spawn            started 30m ago",
		"(heartbeats stopped without an end report)",
		"last error      `parlay spawn` ended dropped outcome no-heartbeat",
	)
}

// TestExplainLastErrorPicksTheNewestSource: the status file said `failed` an
// hour ago and a command failed a minute ago; the newer evidence wins.
func TestExplainLastErrorPicksTheNewestSource(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", time.Now().UTC().Format(time.RFC3339)),
		"/api/chat/commands": commandsBody(wire.CommandInvocation{
			ID: "c1", Verb: "send", Agent: "crew-1", State: "exited",
			StartedAt:  time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339),
			EndedAt:    time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339),
			ExitCode:   intPtr(2),
			Outcome:    "usage",
			DurationMs: 30,
		}),
	})
	f.statusFile(t, "failed: could not reach the registry\n")
	f.ageStatusFile(t, time.Hour)

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out, "last error      `parlay send` ended exited exit 2 outcome usage")
	notWantLine(t, out, "its last status was `failed`")
}

// TestExplainLastErrorSaysWhenItLookedAndFoundNothing pins the "none" that
// came from looking. The same word must never be printed when no source
// answered — that case is asserted in the nothing-observable test below.
func TestExplainLastErrorSaysWhenItLookedAndFoundNothing(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", time.Now().UTC().Format(time.RFC3339)),
		"/api/chat/commands":    commandsBody(),
	})
	f.statusFile(t, "working: fine\n")

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out, "last error      none observed (looked at: server registry, command history)")
}

// TestExplainNothingObservableExitsOne is the only non-zero exit: no source
// answered AND no local record exists, so there is nothing to report and
// "unknown" would understate it.
func TestExplainNothingObservableExitsOne(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	// No relay, no spool, no status, no session-start.

	_, stderr, code, exited := explainRun(t, []string{"crew-1"})
	if !exited || code != ExitExplainNothing {
		t.Fatalf("exit = (%d, %v), want (%d, true)", code, exited, ExitExplainNothing)
	}
	wantLine(t, stderr, "nothing was observable about crew-1")
}

// TestExplainUsageAndHelp pins the arg contract: exactly one positional, and
// --help short-circuits before any read.
func TestExplainUsageAndHelp(t *testing.T) {
	t.Run("no id", func(t *testing.T) {
		_, _, code, exited := explainRun(t, nil)
		if !exited || code != 2 {
			t.Fatalf("exit = (%d, %v), want (2, true)", code, exited)
		}
	})
	t.Run("two ids", func(t *testing.T) {
		_, _, code, exited := explainRun(t, []string{"a", "b"})
		if !exited || code != 2 {
			t.Fatalf("exit = (%d, %v), want (2, true)", code, exited)
		}
	})
	t.Run("unknown flag is a hard exit, never ignored", func(t *testing.T) {
		_, _, code, exited := explainRun(t, []string{"--nope", "crew-1"})
		if !exited || code != 2 {
			t.Fatalf("exit = (%d, %v), want (2, true)", code, exited)
		}
	})
	t.Run("help needs no server", func(t *testing.T) {
		noSleep(t)
		f := newExplainFixture(t, "crew-1")
		f.deadServer(t)
		out, _, _, exited := explainRun(t, []string{"--help"})
		if exited {
			t.Fatal("--help exited")
		}
		wantLine(t, out, "parlay explain — ONE agent's whole story")
	})
}
