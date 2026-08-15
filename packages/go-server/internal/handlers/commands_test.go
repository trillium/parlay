package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/store"
)

// goldenPath is the single wire-shape fixture both renderers are tested
// against: this package proves the server emits it, and
// packages/client/src/live-commands.test.ts proves the panel renders it. A
// field renamed on one side fails on both, which is what makes "one registry,
// two renderers" checkable rather than merely asserted.
const goldenPath = "../../testdata/live-commands.golden.json"

// testClock is a manually advanced clock so reaping is tested without sleeps.
type testClock struct{ t time.Time }

func (c *testClock) Now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// withCommandClock swaps in a registry driven by a manual clock. Commands is
// an ordinary exported field holding no files, so this needs no seam beyond
// the constructor the store package already exports.
func withCommandClock(st *store.Store, staleAfter, retainDone time.Duration) *testClock {
	clk := &testClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	st.Commands = store.NewCommandRegistry(store.CommandRegistryConfig{
		Now:        clk.Now,
		StaleAfter: staleAfter,
		RetainDone: retainDone,
	})
	return clk
}

// postCommandJSON is postJSON plus the JSON content type the report routes
// require (see requireCommandReport). Deliberately its own helper rather than
// a change to the shared one: the content-type requirement is this feature's
// guard, so these tests should break if it stops being satisfied here.
func postCommandJSON(t *testing.T, h http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// readCommands calls the read endpoint and decodes it.
func readCommands(t *testing.T, st *store.Store, hub *Hub) commandsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/chat/commands", nil)
	rec := httptest.NewRecorder()
	handleCommands(st, hub)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /commands status = %d, want 200", rec.Code)
	}
	var got commandsResponse
	decodeBody(t, rec, &got)
	return got
}

func findRecord(list []store.CommandInvocation, id string) (store.CommandInvocation, bool) {
	for _, rec := range list {
		if rec.ID == id {
			return rec, true
		}
	}
	return store.CommandInvocation{}, false
}

// --- lifecycle: the three cases the brief requires ------------------------

func TestStartedCommandAppearsOnTheReadEndpoint(t *testing.T) {
	st := newTestStore(t)
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{
		"id": "c-1", "verb": "listen", "agent": "crew-1", "pid": 4242,
	})

	got := readCommands(t, st, nil)
	rec, ok := findRecord(got.Commands, "c-1")
	if !ok {
		t.Fatalf("started command missing from %+v", got.Commands)
	}
	if rec.State != store.CommandRunning || rec.Verb != "listen" || rec.Agent != "crew-1" {
		t.Errorf("record = %+v, want running listen for crew-1", rec)
	}
	if got.Running != 1 {
		t.Errorf("running = %d, want 1", got.Running)
	}
}

// One reply, one moment. `running` and `commands` are two halves of the same
// response, so a reader must never be able to see a count its own rows
// contradict — a header saying "2 running" above three running rows makes the
// whole view untrustworthy. Driven over a registry holding every state at
// once, since the count is only interesting when something is NOT running.
func TestRunningCountAlwaysMatchesTheListedRecords(t *testing.T) {
	st := newTestStore(t)
	clk := withCommandClock(st, 90*time.Second, time.Hour)

	for _, id := range []string{"run-1", "run-2", "done-1", "fail-1", "gone-1"} {
		postCommandJSON(t, handleCommandStart(st, nil), map[string]any{"id": id, "verb": "send"})
	}
	postCommandJSON(t, handleCommandEnd(st, nil), map[string]any{
		"id": "done-1", "state": "finished", "exitCode": 0, "outcome": "ok",
	})
	postCommandJSON(t, handleCommandEnd(st, nil), map[string]any{
		"id": "fail-1", "state": "failed", "exitCode": 3, "outcome": "error",
	})

	// gone-1 stops reporting; the two live ones keep heartbeating across the
	// staleness line, so the next read expires exactly one record.
	clk.advance(95 * time.Second)
	for _, id := range []string{"run-1", "run-2"} {
		postCommandJSON(t, handleCommandHeartbeat(st), map[string]any{"id": id})
	}

	got := readCommands(t, st, nil)

	states := map[string]int{}
	for _, rec := range got.Commands {
		states[rec.State]++
	}
	for _, want := range []string{store.CommandRunning, store.CommandFinished, store.CommandFailed, store.CommandExpired} {
		if states[want] == 0 {
			t.Fatalf("no %s record in %+v — the invariant would be vacuous", want, got.Commands)
		}
	}
	if got.Running != states[store.CommandRunning] {
		t.Errorf("running = %d, but the emitted commands hold %d running records: %+v",
			got.Running, states[store.CommandRunning], got.Commands)
	}
}

func TestFinishedCommandLeavesTheRunningSet(t *testing.T) {
	st := newTestStore(t)
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{"id": "c-1", "verb": "send"})
	postCommandJSON(t, handleCommandEnd(st, nil), map[string]any{
		"id": "c-1", "state": "finished", "exitCode": 0, "outcome": "ok",
	})

	got := readCommands(t, st, nil)
	if got.Running != 0 {
		t.Errorf("running = %d, want 0 after the command finished", got.Running)
	}
	rec, ok := findRecord(got.Commands, "c-1")
	if !ok {
		t.Fatal("finished command should still be listed during its retention window")
	}
	if rec.State != store.CommandFinished || rec.Outcome != "ok" {
		t.Errorf("record = %+v, want finished/ok", rec)
	}
}

func TestCommandThatDiesWithoutReportingIsReaped(t *testing.T) {
	st := newTestStore(t)
	clk := withCommandClock(st, 30*time.Second, 10*time.Second)

	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{"id": "c-1", "verb": "monitor"})
	if got := readCommands(t, st, nil); got.Running != 1 {
		t.Fatalf("running = %d before the process dies, want 1", got.Running)
	}

	// The process is killed: no end report, no further heartbeats, ever.
	clk.advance(31 * time.Second)

	got := readCommands(t, st, nil)
	if got.Running != 0 {
		t.Errorf("running = %d, want 0 — an abandoned record must not stay 'running' forever", got.Running)
	}
	rec, ok := findRecord(got.Commands, "c-1")
	if !ok {
		t.Fatal("reaped command should be listed as expired during retention")
	}
	if rec.State != store.CommandExpired || rec.Outcome != "no-heartbeat" {
		t.Errorf("record = %+v, want expired/no-heartbeat", rec)
	}

	// ...and then it ages out of the list entirely.
	clk.advance(11 * time.Second)
	if got := readCommands(t, st, nil); len(got.Commands) != 0 {
		t.Errorf("commands = %+v, want empty after retention lapsed", got.Commands)
	}
}

// --- SSE: the panel's live updates ----------------------------------------

func TestCommandStartBroadcastsCommandUpdate(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	sub, cancel := hub.subscribe()
	defer cancel()

	postCommandJSON(t, handleCommandStart(st, hub), map[string]any{"id": "c-1", "verb": "claim"})

	ev := awaitEvent(t, sub, eventCommandUpdate, time.Second)
	rec, ok := ev.data.(store.CommandInvocation)
	if !ok || rec.ID != "c-1" || rec.State != store.CommandRunning {
		t.Errorf("event data = %#v, want a running CommandInvocation for c-1", ev.data)
	}
}

func TestCommandEndBroadcastsCommandUpdate(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	postCommandJSON(t, handleCommandStart(st, hub), map[string]any{"id": "c-1", "verb": "claim"})

	sub, cancel := hub.subscribe()
	defer cancel()
	postCommandJSON(t, handleCommandEnd(st, hub), map[string]any{"id": "c-1", "state": "failed", "outcome": "error"})

	ev := awaitEvent(t, sub, eventCommandUpdate, time.Second)
	rec, _ := ev.data.(store.CommandInvocation)
	if rec.State != store.CommandFailed {
		t.Errorf("event data = %#v, want state failed", ev.data)
	}
}

func TestSweepBroadcastsExpiryThenDrop(t *testing.T) {
	st := newTestStore(t)
	clk := withCommandClock(st, 30*time.Second, 10*time.Second)
	hub := newHub(newBroker())
	postCommandJSON(t, handleCommandStart(st, hub), map[string]any{"id": "c-1", "verb": "monitor"})

	sub, cancel := hub.subscribe()
	defer cancel()

	clk.advance(31 * time.Second)
	sweepCommands(st, hub)
	if ev := awaitEvent(t, sub, eventCommandUpdate, time.Second); ev.data.(store.CommandInvocation).State != store.CommandExpired {
		t.Errorf("first sweep event = %#v, want state expired", ev.data)
	}

	clk.advance(11 * time.Second)
	sweepCommands(st, hub)
	ev := awaitEvent(t, sub, eventCommandUpdate, time.Second)
	rec := ev.data.(store.CommandInvocation)
	if rec.State != commandStateDropped || rec.ID != "c-1" {
		t.Errorf("second sweep event = %#v, want a dropped notice for c-1", ev.data)
	}
}

// withCommandCap installs a registry whose record cap is small enough that a
// short burst overflows it, so the eviction path is reachable through the real
// handlers without posting hundreds of records.
func withCommandCap(st *store.Store, max int) *testClock {
	clk := &testClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	st.Commands = store.NewCommandRegistry(store.CommandRegistryConfig{
		Now:        clk.Now,
		MaxRecords: max,
	})
	return clk
}

// Eviction is the OTHER way a record leaves the registry, and a client has to
// hear about it for the same reason it hears about a sweep's drops: on an
// append-only stream, an id nobody is told to forget is shown forever.
func TestEvictionBroadcastsADropForEveryRecordItSheds(t *testing.T) {
	st := newTestStore(t)
	clk := withCommandCap(st, 2)
	hub := newHub(newBroker())
	sub, cancel := hub.subscribe()
	defer cancel()

	ids := []string{"c-1", "c-2", "c-3", "c-4"}
	for _, id := range ids {
		clk.advance(time.Second)
		postCommandJSON(t, handleCommandStart(st, hub), map[string]any{"id": id, "verb": "send"})
		postCommandJSON(t, handleCommandEnd(st, hub), map[string]any{
			"id": id, "state": "finished", "outcome": "ok",
		})
	}

	announced := map[string]bool{}
drain:
	for {
		select {
		case ev := <-sub:
			rec, ok := ev.data.(store.CommandInvocation)
			if ok && ev.name == eventCommandUpdate && rec.State == commandStateDropped {
				announced[rec.ID] = true
			}
		default:
			break drain
		}
	}

	held := readCommands(t, st, hub).Commands
	if len(held) > 2 {
		t.Fatalf("registry grew past the cap: %+v", held)
	}
	for _, id := range ids {
		_, stillHeld := findRecord(held, id)
		if !stillHeld && !announced[id] {
			t.Errorf("record %q was evicted with no dropped broadcast; announced = %v", id, announced)
		}
		if stillHeld && announced[id] {
			t.Errorf("record %q was announced dropped but is still in the registry", id)
		}
	}
}

func TestEventsConnectBurstIncludesCommands(t *testing.T) {
	st := newTestStore(t)
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{"id": "c-1", "verb": "listen"})

	rec, stop := runEvents(t, st, newHub(newBroker()))
	time.Sleep(50 * time.Millisecond) // let the initial burst land before we stop the stream
	stop()

	body := rec.Body.String()
	if !strings.Contains(body, "event: commands") {
		t.Errorf("connect burst missing the commands event; got:\n%s", body)
	}
	if !strings.Contains(body, `"verb":"listen"`) {
		t.Errorf("commands event missing the started command; got:\n%s", body)
	}
}

// TestSSEBurstAndReadEndpointCarryByteIdenticalCommands is the "one registry,
// two renderers" claim reduced to something checkable. The CLI reads
// GET /api/chat/commands; the panel reads the `commands` SSE frame. This
// asserts the two payloads are byte-for-byte the same array — not merely
// similar, and not two independently-built views that happen to agree today.
// The clock is frozen so a computed DurationMs cannot drift between the two
// reads and mask a real divergence.
func TestSSEBurstAndReadEndpointCarryByteIdenticalCommands(t *testing.T) {
	st := newTestStore(t)
	clk := withCommandClock(st, store.DefaultCommandStaleAfter, store.DefaultCommandRetainDone)

	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{
		"id": "c-1", "verb": "listen", "agent": "crew-1", "flags": []string{"--agent"}, "pid": 4242,
	})
	clk.advance(3 * time.Second)
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{"id": "c-2", "verb": "merge-gate"})
	postCommandJSON(t, handleCommandEnd(st, nil), map[string]any{
		"id": "c-2", "state": "failed", "exitCode": 3, "outcome": "error",
	})

	rec, stop := runEvents(t, st, newHub(newBroker()))
	time.Sleep(50 * time.Millisecond) // let the initial burst land before we stop the stream
	stop()

	sse := sseFrameData(t, rec.Body.String(), "commands")
	fromRead, err := json.Marshal(readCommands(t, st, nil).Commands)
	if err != nil {
		t.Fatalf("marshal read-endpoint commands: %v", err)
	}
	if sse != string(fromRead) {
		t.Errorf("the two renderers are not reading one payload.\n SSE: %s\nread: %s", sse, fromRead)
	}
	if !strings.Contains(sse, `"verb":"listen"`) {
		t.Errorf("the shared payload does not actually contain the commands: %s", sse)
	}
}

// sseFrameData returns the data line of the first frame with the given event
// name. Enough of an SSE parser for a test; the panel uses the browser's.
func sseFrameData(t *testing.T, body, event string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "event: "+event {
			continue
		}
		for _, next := range lines[i+1:] {
			if data, ok := strings.CutPrefix(next, "data: "); ok {
				return strings.TrimRight(data, "\r")
			}
		}
	}
	t.Fatalf("no %q frame in stream:\n%s", event, body)
	return ""
}

// --- reporting protocol ----------------------------------------------------

func TestHeartbeatKeepsALongRunningCommandOutOfTheReaper(t *testing.T) {
	st := newTestStore(t)
	clk := withCommandClock(st, 30*time.Second, 10*time.Second)
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{"id": "c-1", "verb": "listen"})

	for i := 0; i < 5; i++ {
		clk.advance(20 * time.Second)
		rec := postCommandJSON(t, handleCommandHeartbeat(st), map[string]any{"id": "c-1"})
		var got commandReportResponse
		decodeBody(t, rec, &got)
		if !got.OK {
			t.Fatalf("heartbeat %d = %+v, want ok", i, got)
		}
	}
	if got := readCommands(t, st, nil); got.Running != 1 {
		t.Errorf("running = %d after 100s of heartbeats, want 1", got.Running)
	}
}

func TestHeartbeatForForgottenIDAsksTheReporterToStartAgain(t *testing.T) {
	st := newTestStore(t) // fresh registry: this is what a server restart looks like
	rec := postCommandJSON(t, handleCommandHeartbeat(st), map[string]any{"id": "c-1"})

	var got commandReportResponse
	decodeBody(t, rec, &got)
	if got.OK || !got.Unknown {
		t.Errorf("response = %+v, want ok=false unknown=true", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — an unknown id is a fact, not an error", rec.Code)
	}
}

func TestEndWithoutAStartStillRecordsTheInvocation(t *testing.T) {
	st := newTestStore(t)
	postCommandJSON(t, handleCommandEnd(st, nil), map[string]any{"id": "c-1", "state": "finished", "outcome": "ok"})

	rec, ok := findRecord(readCommands(t, st, nil).Commands, "c-1")
	if !ok || rec.State != store.CommandFinished {
		t.Errorf("record = %+v ok=%v, want a finished record — start/end must be order-independent", rec, ok)
	}
}

func TestCommandReportsRequireAnID(t *testing.T) {
	st := newTestStore(t)
	for name, h := range map[string]http.HandlerFunc{
		"start":     handleCommandStart(st, nil),
		"heartbeat": handleCommandHeartbeat(st),
		"end":       handleCommandEnd(st, nil),
	} {
		rec := postCommandJSON(t, h, map[string]any{})
		var got map[string]any
		decodeBody(t, rec, &got)
		if rec.Code != http.StatusOK || got["error"] == nil {
			t.Errorf("%s: status=%d body=%v, want 200 with an error field", name, rec.Code, got)
		}
	}
}

func TestCommandEndpointsRejectTheWrongMethod(t *testing.T) {
	st := newTestStore(t)
	cases := []struct {
		name   string
		method string
		h      http.HandlerFunc
	}{
		{"commands", http.MethodPost, handleCommands(st, nil)},
		{"start", http.MethodGet, handleCommandStart(st, nil)},
		{"heartbeat", http.MethodGet, handleCommandHeartbeat(st)},
		{"end", http.MethodGet, handleCommandEnd(st, nil)},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/", nil)
		rec := httptest.NewRecorder()
		c.h(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s via %s: status = %d, want 405", c.name, c.method, rec.Code)
		}
	}
}

// TestReportRoutesRefuseACrossOriginSimpleRequest pins the CSRF-shaped guard
// on the three mutating routes. A browser can only send text/plain,
// form-urlencoded, or multipart without a preflight, and this server answers
// no preflight — so requiring application/json is what keeps a hostile page
// from writing into the registry. The read endpoint is deliberately not
// gated: it stays world-readable like /api/chat/agents.
func TestReportRoutesRefuseACrossOriginSimpleRequest(t *testing.T) {
	st := newTestStore(t)
	routes := map[string]http.HandlerFunc{
		"start":     handleCommandStart(st, nil),
		"heartbeat": handleCommandHeartbeat(st),
		"end":       handleCommandEnd(st, nil),
	}
	simpleTypes := []string{"", "text/plain", "text/plain;charset=UTF-8", "application/x-www-form-urlencoded", "multipart/form-data"}

	for name, h := range routes {
		for _, ct := range simpleTypes {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":"forged","verb":"send"}`))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s with Content-Type %q: status = %d, want 415", name, ct, rec.Code)
			}
		}
	}

	if got := readCommands(t, st, nil); len(got.Commands) != 0 {
		t.Errorf("a refused request still wrote to the registry: %+v", got.Commands)
	}
}

func TestReportRoutesAcceptAParameterizedJSONContentType(t *testing.T) {
	st := newTestStore(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":"c-1","verb":"send"}`))
	req.Header.Set("Content-Type", "application/JSON; charset=utf-8")
	rec := httptest.NewRecorder()
	handleCommandStart(st, nil)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — charset must not disqualify a JSON body", rec.Code)
	}
	if _, ok := findRecord(readCommands(t, st, nil).Commands, "c-1"); !ok {
		t.Error("the accepted report did not reach the registry")
	}
}

// TestNoFlagValueEverReachesTheWire is the redaction guarantee, checked at
// the HTTP boundary rather than only in the store: a reporter that hands the
// server a flag with its value attached must not be able to publish that
// value to every connected panel. The endpoint is unauthenticated, so the
// hostile tokens below are exactly what a caller may send — the server's own
// shape check, not the CLI's, is what has to reject them, and reject is the
// word: a trimmed token would still carry the head of the secret.
func TestNoFlagValueEverReachesTheWire(t *testing.T) {
	const secret = "sk-live-abcdef"
	st := newTestStore(t)
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{
		"id":   "c-1",
		"verb": "send",
		"flags": []string{
			"--token=s3cr3t-value",
			"--message", "the whole body of a private message",
			"/home/someone/private/path",
			"-- heads up: the key is " + secret,
			"--flag with space",
			"-5",
			"-",
			"--",
			"--" + strings.Repeat("z", 200),
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/chat/commands", nil)
	rec := httptest.NewRecorder()
	handleCommands(st, nil)(rec, req)

	body := rec.Body.String()
	for _, leak := range []string{"s3cr3t", "whole body", "private/path", secret, "sk-live", "sk-", "heads", "zzz"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaked %q; got:\n%s", leak, body)
		}
	}
	got, _ := findRecord(readCommands(t, st, nil).Commands, "c-1")
	if !reflect.DeepEqual(got.Flags, []string{"--token", "--message"}) {
		t.Errorf("flags = %#v, want only the flag NAMES", got.Flags)
	}
}

// --- the shared wire shape -------------------------------------------------

// jsonKeys collects every object key in v, prefixed by its path, so two
// payloads can be compared on structure while ignoring volatile values
// (timestamps, ids, durations).
func jsonKeys(prefix string, v any, into map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			into[prefix+k] = true
			jsonKeys(prefix+k+".", sub, into)
		}
	case []any:
		for _, sub := range t {
			jsonKeys(prefix, sub, into)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func loadGolden(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(goldenPath))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	return b
}

// TestGoldenFixtureIsExactlyTheServerWireShape round-trips the fixture
// through this server's own response types. It fails if the fixture carries a
// field the server cannot produce, or omits one the server always emits — so
// the fixture can never drift into being a shape only the client believes in.
func TestGoldenFixtureIsExactlyTheServerWireShape(t *testing.T) {
	golden := loadGolden(t)

	dec := json.NewDecoder(bytes.NewReader(golden))
	dec.DisallowUnknownFields()
	var typed commandsResponse
	if err := dec.Decode(&typed); err != nil {
		t.Fatalf("golden does not fit commandsResponse: %v", err)
	}

	reencoded, err := json.Marshal(typed)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}

	var want, got any
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reencoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round-trip changed the payload:\n golden = %s\n server = %s", golden, reencoded)
	}
}

// TestReadEndpointMatchesGoldenShape drives the real handler with records
// exercising every field and compares its key set to the fixture's. This is
// the server half of the "both surfaces read the same state" claim; the
// client half asserts the panel renders that same fixture.
func TestReadEndpointMatchesGoldenShape(t *testing.T) {
	st := newTestStore(t)
	clk := withCommandClock(st, 90*time.Second, 60*time.Second)

	// One record per state the fixture shows, laid out on the fake clock so
	// the oldest one crosses the staleness line and the read below reaps it.
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{
		"id": "c-1", "verb": "robots-watch", "agent": "crew-3",
	})
	clk.advance(91 * time.Second) // c-1 is now past staleAfter and never heartbeated
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{
		"id": "c-2", "verb": "send", "agent": "crew-1", "pid": 4300,
	})
	postCommandJSON(t, handleCommandEnd(st, nil), map[string]any{
		"id": "c-2", "state": "finished", "exitCode": 0, "outcome": "ok",
	})
	clk.advance(time.Second)
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{
		"id": "c-3", "verb": "merge-gate", "agent": "crew-2", "flags": []string{"--json"}, "pid": 4310,
	})
	postCommandJSON(t, handleCommandEnd(st, nil), map[string]any{
		"id": "c-3", "state": "failed", "exitCode": 3, "outcome": "error",
	})
	clk.advance(time.Second)
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{"id": "c-4", "verb": "history"})
	clk.advance(2 * time.Second)
	postCommandJSON(t, handleCommandStart(st, nil), map[string]any{
		"id": "c-5", "verb": "listen", "agent": "crew-1",
		"flags": []string{"--agent", "--caps"}, "pid": 4242,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/chat/commands", nil)
	rec := httptest.NewRecorder()
	handleCommands(st, nil)(rec, req)

	var live, golden any
	if err := json.Unmarshal(rec.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(loadGolden(t), &golden); err != nil {
		t.Fatal(err)
	}

	liveKeys, goldenKeys := map[string]bool{}, map[string]bool{}
	jsonKeys("", live, liveKeys)
	jsonKeys("", golden, goldenKeys)
	if !reflect.DeepEqual(sortedKeys(liveKeys), sortedKeys(goldenKeys)) {
		t.Errorf("live response keys != golden keys\n live   = %v\n golden = %v",
			sortedKeys(liveKeys), sortedKeys(goldenKeys))
	}

	// The fixture's own summary numbers must be reproducible too, not just
	// its field names: two of those five records are still running.
	var typed commandsResponse
	decodeBody(t, rec, &typed)
	if typed.Running != 2 || len(typed.Commands) != 5 || typed.StaleAfterMs != 90000 {
		t.Errorf("summary = running:%d commands:%d staleAfterMs:%d, want 2/5/90000",
			typed.Running, len(typed.Commands), typed.StaleAfterMs)
	}
	if typed.Commands[0].ID != "c-5" {
		t.Errorf("commands[0] = %q, want the newest (c-5) first, matching the fixture's order", typed.Commands[0].ID)
	}
}
