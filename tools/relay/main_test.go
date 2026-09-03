package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUpstream is a stand-in for the Pulse chat server's long-poll endpoint.
// Per channel it serves a fixed, ordered script of messages: each poll with a
// matching after=<lastId> hands back the next message; once the script is
// exhausted it returns {"timeout":true} so the relay's loop idles instead of
// spinning. This lets a test drive the relay's real register/pollLoop code
// against a deterministic message stream.
type fakeUpstream struct {
	mu sync.Mutex
	// scripts[channel] is the ordered messages that channel will emit.
	scripts map[string][]upstreamMessage
	// served[channel] counts how many messages that channel has handed out.
	served map[string]int
}

func newFakeUpstream() *fakeUpstream {
	return &fakeUpstream{
		scripts: make(map[string][]upstreamMessage),
		served:  make(map[string]int),
	}
}

func (f *fakeUpstream) setScript(channel string, msgs []upstreamMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[channel] = msgs
	f.served[channel] = 0
}

// handler implements GET /api/chat/poll?after=<id>&channel=<agent>. It returns
// the next scripted message for the channel (advancing only when after= matches
// the previously served id, mirroring the real server's at-most-one-per-call
// contract), or {"timeout":true} when the script is drained.
func (f *fakeUpstream) handler(w http.ResponseWriter, req *http.Request) {
	channel := req.URL.Query().Get("channel")
	after := req.URL.Query().Get("after")

	f.mu.Lock()
	script := f.scripts[channel]
	idx := f.served[channel]

	// The relay only advances after= once it has spooled a message, so a repeat
	// poll with the same after= must not double-serve. Gate delivery on after=
	// equalling the last id we handed out (or "" at the start).
	var expectedAfter string
	if idx > 0 {
		expectedAfter = script[idx-1].ID
	}
	if idx >= len(script) || after != expectedAfter {
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"timeout": true})
		return
	}
	msg := script[idx]
	f.served[channel] = idx + 1
	f.mu.Unlock()

	writeJSON(w, http.StatusOK, msg)
}

// readSpoolIDs returns the ids in order from a spool file's CHAT_MSG lines.
func readSpoolIDs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read spool %s: %v", path, err)
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "CHAT_MSG|") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) >= 2 && parts[1] != "" {
			ids = append(ids, parts[1])
		}
	}
	return ids
}

// waitForSpoolCount polls a spool until it holds want ids or the deadline hits.
func waitForSpoolCount(t *testing.T, path string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ids := readSpoolIDs(t, path)
		if len(ids) >= want {
			return ids
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d ids in %s (have %d: %v)", want, path, len(ids), ids)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func newTestRelay(t *testing.T, server string) *relay {
	t.Helper()
	dir := t.TempDir()
	return &relay{
		server:     strings.TrimRight(server, "/"),
		runtimeDir: dir,
		client:     &http.Client{},
		loops:      make(map[string]*agentLoop),
	}
}

// TestChannelIsolation is the first riskiest-behavior test: with two agents
// registered against a shared relay, each agent's distinct messages must land
// ONLY in that agent's spool — a message for agent A must never appear in
// agent B's spool.
func TestChannelIsolation(t *testing.T) {
	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()

	// Distinct message streams per channel. The ids are deliberately disjoint so
	// any cross-channel leak is unambiguous.
	up.setScript("agent-a", []upstreamMessage{
		{ID: "a1", Role: "user", Text: "for A one"},
		{ID: "a2", Role: "user", Text: "for A two"},
		{ID: "a3", Role: "user", Text: "for A three"},
	})
	up.setScript("agent-b", []upstreamMessage{
		{ID: "b1", Role: "user", Text: "for B one"},
		{ID: "b2", Role: "user", Text: "for B two"},
	})

	r := newTestRelay(t, srv.URL)
	spoolA, err := r.register("agent-a")
	if err != nil {
		t.Fatalf("register agent-a: %v", err)
	}
	spoolB, err := r.register("agent-b")
	if err != nil {
		t.Fatalf("register agent-b: %v", err)
	}
	defer r.unregister("agent-a")
	defer r.unregister("agent-b")

	idsA := waitForSpoolCount(t, spoolA, 3)
	idsB := waitForSpoolCount(t, spoolB, 2)

	wantA := []string{"a1", "a2", "a3"}
	wantB := []string{"b1", "b2"}

	if fmt.Sprint(idsA) != fmt.Sprint(wantA) {
		t.Errorf("agent-a spool = %v, want %v", idsA, wantA)
	}
	if fmt.Sprint(idsB) != fmt.Sprint(wantB) {
		t.Errorf("agent-b spool = %v, want %v", idsB, wantB)
	}

	// The core isolation assertion: no B id in A's spool, no A id in B's spool.
	for _, id := range idsA {
		if strings.HasPrefix(id, "b") {
			t.Errorf("channel leak: agent-b message %q found in agent-a spool", id)
		}
	}
	for _, id := range idsB {
		if strings.HasPrefix(id, "a") {
			t.Errorf("channel leak: agent-a message %q found in agent-b spool", id)
		}
	}
}

// TestRestartNoDuplicate is the second riskiest-behavior test: after a relay
// restart, the poll loop must resume from the last spooled id and NOT replay
// messages already in the spool. We prime a spool with two messages, then start
// a fresh relay whose upstream is scripted to only advance past those two ids —
// the restarted loop must seed lastID from the spool (so after=<last spooled>),
// receive only genuinely-new messages, and never duplicate the primed lines.
func TestRestartNoDuplicate(t *testing.T) {
	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()

	dir := t.TempDir()
	spool := filepath.Join(dir, "agent-a.chan")

	// Simulate a pre-restart spool: two messages already delivered and written.
	primed := []*upstreamMessage{
		{ID: "m1", Role: "user", Text: "first"},
		{ID: "m2", Role: "user", Text: "second"},
	}
	for _, m := range primed {
		if err := appendSpool(spool, m); err != nil {
			t.Fatalf("prime spool: %v", err)
		}
	}
	if got := readSpoolIDs(t, spool); fmt.Sprint(got) != fmt.Sprint([]string{"m1", "m2"}) {
		t.Fatalf("primed spool = %v, want [m1 m2]", got)
	}

	// The upstream's full history for this channel is m1,m2,m3. A naive restart
	// that polled from after="" would re-serve m1 and m2 (duplicates). The
	// fake only advances past after=m2 to serve m3, exactly mirroring the real
	// server replaying from the caller's last-seen id. Correct resume => the
	// loop seeds lastID=m2 from the spool and asks for what comes after m2.
	up.setScript("agent-a", []upstreamMessage{
		{ID: "m1", Role: "user", Text: "first"},
		{ID: "m2", Role: "user", Text: "second"},
		{ID: "m3", Role: "user", Text: "third-new"},
	})
	// Pre-advance the fake to the point a real server would be for this caller:
	// it has already delivered m1,m2, so served=2 and the next legit poll is
	// after=m2 -> m3. This models "the relay died after spooling m1,m2".
	up.mu.Lock()
	up.served["agent-a"] = 2
	up.mu.Unlock()

	// Start a fresh relay against the SAME runtime dir / spool — the restart.
	r := &relay{
		server:     strings.TrimRight(srv.URL, "/"),
		runtimeDir: dir,
		client:     &http.Client{},
		loops:      make(map[string]*agentLoop),
	}
	if _, err := r.register("agent-a"); err != nil {
		t.Fatalf("register after restart: %v", err)
	}
	defer r.unregister("agent-a")

	// After restart we expect exactly m1,m2 (primed, untouched) then m3 (new).
	ids := waitForSpoolCount(t, spool, 3)

	want := []string{"m1", "m2", "m3"}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("post-restart spool = %v, want %v (duplicate replay or lost resume)", ids, want)
	}

	// Explicit no-duplicate assertion: each id appears exactly once.
	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("message %q appears %d times after restart (want 1) — replay dedup broken", id, n)
		}
	}
}

// TestLastSpooledID_Resume covers the resume-seed helper directly, including the
// empty/absent cases and the partial-first-line skip in the tail window.
func TestLastSpooledID_Resume(t *testing.T) {
	dir := t.TempDir()

	// Absent file -> "".
	if got := lastSpooledID(filepath.Join(dir, "nope.chan")); got != "" {
		t.Errorf("absent spool lastID = %q, want empty", got)
	}

	// Empty file -> "".
	empty := filepath.Join(dir, "empty.chan")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lastSpooledID(empty); got != "" {
		t.Errorf("empty spool lastID = %q, want empty", got)
	}

	// Normal case -> last well-formed id.
	spool := filepath.Join(dir, "s.chan")
	for _, m := range []*upstreamMessage{
		{ID: "x1", Role: "user", Text: "one"},
		{ID: "x2", Role: "user", Text: "two"},
	} {
		if err := appendSpool(spool, m); err != nil {
			t.Fatal(err)
		}
	}
	if got := lastSpooledID(spool); got != "x2" {
		t.Errorf("lastSpooledID = %q, want x2", got)
	}

	// A spool whose tail is a device-level event line (written before the
	// tts_event filter existed) must seed from the last CHAT-HISTORY id: event
	// ids are unknown to /api/chat/poll's after-index, and seeding one resets
	// the cursor to -1 upstream and replays the channel's backlog.
	if err := appendSpool(spool, &upstreamMessage{ID: "tts9", Role: "tts_event", Text: ""}); err != nil {
		t.Fatal(err)
	}
	if got := lastSpooledID(spool); got != "x2" {
		t.Errorf("lastSpooledID with trailing tts_event = %q, want x2", got)
	}
}

// TestValidAgentID guards the path-traversal defense that keeps a spool inside
// the runtime dir. A slug that could escape (slash, dotdot) must be rejected.
func TestValidAgentID(t *testing.T) {
	good := []string{"main-agent", "resume", "a", "agent-1", "x1y2"}
	bad := []string{"", "-lead", "trail-", "a--b", "../etc", "a/b", "Agent", "a b", "a.b"}
	for _, s := range good {
		if !validAgentID(s) {
			t.Errorf("validAgentID(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validAgentID(s) {
			t.Errorf("validAgentID(%q) = true, want false", s)
		}
	}
}

// TestFlatten ensures multi-line message text collapses to one spool line so the
// monitor's line-oriented tail reader never splits one message across lines.
func TestFlatten(t *testing.T) {
	cases := map[string]string{
		"a\nb":     "a b",
		"a\r\nb":   "a b",
		"a\rb":     "a b",
		"no break": "no break",
		"x\n\ny":   "x  y",
	}
	for in, want := range cases {
		if got := flatten(in); got != want {
			t.Errorf("flatten(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRegisterIdempotent confirms a second register of a live agent returns the
// same spool and does not start a second loop (which would double-spool).
func TestRegisterIdempotent(t *testing.T) {
	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()
	up.setScript("agent-a", []upstreamMessage{{ID: "a1", Role: "user", Text: "one"}})

	r := newTestRelay(t, srv.URL)
	s1, err := r.register("agent-a")
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	s2, err := r.register("agent-a")
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	defer r.unregister("agent-a")

	if s1 != s2 {
		t.Errorf("idempotent register returned different spools: %q vs %q", s1, s2)
	}
	r.mu.Lock()
	n := len(r.loops)
	r.mu.Unlock()
	if n != 1 {
		t.Errorf("registry has %d loops after double register, want 1", n)
	}

	// Exactly one copy of the single scripted message must be spooled.
	ids := waitForSpoolCount(t, s1, 1)
	time.Sleep(150 * time.Millisecond) // let any errant second loop double-write
	ids = readSpoolIDs(t, s1)
	if len(ids) != 1 || ids[0] != "a1" {
		t.Errorf("spool = %v, want [a1] (a second loop would duplicate)", ids)
	}
}

// small sanity that the JSON contract we assert on matches upstreamMessage.
func TestUpstreamMessageJSON(t *testing.T) {
	var m upstreamMessage
	if err := json.Unmarshal([]byte(`{"timeout":true}`), &m); err != nil {
		t.Fatal(err)
	}
	if !m.Timeout {
		t.Errorf("timeout tick not decoded")
	}
	m = upstreamMessage{}
	if err := json.Unmarshal([]byte(`{"id":"z","role":"user","text":"hi","ts":"t"}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "z" || m.Role != "user" || m.Text != "hi" {
		t.Errorf("message decode = %+v", m)
	}
}

// ── Terminal 410: a channel the server has unregistered ──────────────────────
//
// robots-ycfa. Every non-200 used to be retryable, on the sound principle that a
// relay must survive a server restart. But 410 Gone is the server saying the
// channel was deliberately removed, and retrying it forever is exactly how 82
// leaked test channels kept live poll loops against a registry that had already
// dropped them. 410 — and ONLY 410 — is terminal for that agent's loop.

// goneUpstream answers 410 for the named channel and idles for every other one.
type goneUpstream struct {
	gone  string
	mu    sync.Mutex
	polls map[string]int
}

func (g *goneUpstream) handler(w http.ResponseWriter, req *http.Request) {
	channel := req.URL.Query().Get("channel")
	g.mu.Lock()
	g.polls[channel]++
	g.mu.Unlock()
	if channel == g.gone {
		writeJSON(w, http.StatusGone, map[string]any{"error": "channel gone", "gone": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"timeout": true})
}

func (g *goneUpstream) pollCount(channel string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.polls[channel]
}

// waitForLoopGone waits until the relay has dropped an agent from its registry.
func waitForLoopGone(t *testing.T, r *relay, agent string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		_, still := r.loops[agent]
		r.mu.Unlock()
		if !still {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay still holds a poll loop for %q after 410 Gone", agent)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestUpstream410DropsTheLoop(t *testing.T) {
	up := &goneUpstream{gone: "leaked-fixture-z1", polls: make(map[string]int)}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()

	r := newTestRelay(t, srv.URL)
	if _, err := r.register("leaked-fixture-z1"); err != nil {
		t.Fatalf("register: %v", err)
	}

	// The loop must remove itself from the registry — not merely stop logging.
	waitForLoopGone(t, r, "leaked-fixture-z1")

	// And it must STAY stopped: no retry loop quietly continuing behind the map
	// removal. Sample the poll count, wait well past the poll cadence, re-sample.
	before := up.pollCount("leaked-fixture-z1")
	time.Sleep(300 * time.Millisecond)
	if after := up.pollCount("leaked-fixture-z1"); after != before {
		t.Errorf("relay kept polling a 410 channel: %d → %d polls", before, after)
	}
}

func TestUpstream410DoesNotAffectOtherChannels(t *testing.T) {
	up := &goneUpstream{gone: "leaked-fixture-z1", polls: make(map[string]int)}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()

	r := newTestRelay(t, srv.URL)
	if _, err := r.register("leaked-fixture-z1"); err != nil {
		t.Fatalf("register leaked: %v", err)
	}
	if _, err := r.register("real-agent"); err != nil {
		t.Fatalf("register real: %v", err)
	}
	defer r.unregister("real-agent")

	waitForLoopGone(t, r, "leaked-fixture-z1")

	r.mu.Lock()
	_, realStillThere := r.loops["real-agent"]
	r.mu.Unlock()
	if !realStillThere {
		t.Fatal("a 410 on one channel dropped an unrelated channel's loop")
	}

	// The healthy channel must still be actively polling.
	before := up.pollCount("real-agent")
	time.Sleep(300 * time.Millisecond)
	if after := up.pollCount("real-agent"); after <= before {
		t.Errorf("healthy channel stopped polling after a sibling's 410: %d → %d", before, after)
	}
}

// ── Pruning survives a relay restart (task-0n80i) ────────────────────────────
// A 410 alone only drops the in-memory loop. resumeFromSpools() re-registers
// every *.chan file it finds at the NEXT relay startup, so without also
// tombstoning the spool on disk, a retired agent's dead spool would resurrect
// its poll loop — and immediately hit 410 again — on every restart forever.

// TestUpstream410TombstonesTheSpool: a terminal 410 must rename the agent's
// spool out of the *.chan glob, not just drop the in-memory loop, so a later
// resumeFromSpools does not resurrect it.
func TestUpstream410TombstonesTheSpool(t *testing.T) {
	up := &goneUpstream{gone: "leaked-fixture-z1", polls: make(map[string]int)}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()

	r := newTestRelay(t, srv.URL)
	spool, err := r.register("leaked-fixture-z1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	waitForLoopGone(t, r, "leaked-fixture-z1")

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(spool + tombstoneSuffix); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("spool %s was never tombstoned after a 410", spool)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(spool); !os.IsNotExist(err) {
		t.Fatalf("plain spool %s still exists after tombstoning (err=%v)", spool, err)
	}
}

// ── Terminal Gone: server resolves an in-flight poll on explicit shutdown ────
// task-35ww. An explicit `parlay shutdown` unregisters server-side while a
// relay's long-poll may already be parked waiting on that exact channel.
// Rather than make that poll sit out its own up-to-30s timeout and only learn
// the channel is gone on its NEXT request's 410, the server resolves it
// immediately with a 200 body carrying {"gone": true}. This must be handled
// exactly like a fresh request's 410 (errChannelGone): drop the loop and
// tombstone the spool, not treated as an ordinary message or idle tick.

// goneBodyUpstream answers 200 {"gone": true} for the named channel — the
// server resolving an in-flight poll on explicit unregister — and idles for
// every other one.
type goneBodyUpstream struct {
	gone  string
	mu    sync.Mutex
	polls map[string]int
}

func (g *goneBodyUpstream) handler(w http.ResponseWriter, req *http.Request) {
	channel := req.URL.Query().Get("channel")
	g.mu.Lock()
	g.polls[channel]++
	g.mu.Unlock()
	if channel == g.gone {
		writeJSON(w, http.StatusOK, map[string]any{"gone": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"timeout": true})
}

func (g *goneBodyUpstream) pollCount(channel string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.polls[channel]
}

func TestUpstreamGoneBodyDropsAndTombstonesTheLoop(t *testing.T) {
	up := &goneBodyUpstream{gone: "leaked-fixture-z1", polls: make(map[string]int)}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()

	r := newTestRelay(t, srv.URL)
	spool, err := r.register("leaked-fixture-z1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	waitForLoopGone(t, r, "leaked-fixture-z1")

	before := up.pollCount("leaked-fixture-z1")
	time.Sleep(300 * time.Millisecond)
	if after := up.pollCount("leaked-fixture-z1"); after != before {
		t.Errorf("relay kept polling after an in-body gone:true: %d → %d polls", before, after)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(spool + tombstoneSuffix); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("spool %s was never tombstoned after an in-body gone:true", spool)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(spool); !os.IsNotExist(err) {
		t.Fatalf("plain spool %s still exists after tombstoning (err=%v)", spool, err)
	}
}

func TestUpstreamGoneBodyDoesNotAffectOtherChannels(t *testing.T) {
	up := &goneBodyUpstream{gone: "leaked-fixture-z1", polls: make(map[string]int)}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()

	r := newTestRelay(t, srv.URL)
	if _, err := r.register("leaked-fixture-z1"); err != nil {
		t.Fatalf("register leaked: %v", err)
	}
	if _, err := r.register("real-agent"); err != nil {
		t.Fatalf("register real: %v", err)
	}
	defer r.unregister("real-agent")

	waitForLoopGone(t, r, "leaked-fixture-z1")

	r.mu.Lock()
	_, realStillThere := r.loops["real-agent"]
	r.mu.Unlock()
	if !realStillThere {
		t.Fatal("an in-body gone:true on one channel dropped an unrelated channel's loop")
	}
}

// task-35ww: r.unregister (the local half of `parlay shutdown`) must tombstone
// the spool itself — not just drop the in-memory loop — for the same reason a
// terminal 410 does: resumeFromSpools() would otherwise resurrect the retired
// agent's poll loop on the relay's next restart. It also reports whether the
// agent was actually registered, so a caller can tell a real teardown from a
// no-op on an already-retired (or never-registered) id — the idempotent case.
func TestUnregisterTombstonesTheSpool(t *testing.T) {
	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		up.handler(w, req)
	}))
	defer srv.Close()

	r := newTestRelay(t, srv.URL)
	spool, err := r.register("agent-a")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if found := r.unregister("agent-a"); !found {
		t.Fatal("unregister(agent-a) = false, want true for a registered agent")
	}

	if _, err := os.Stat(spool + tombstoneSuffix); err != nil {
		t.Fatalf("spool %s was not tombstoned by unregister: %v", spool, err)
	}
	if _, err := os.Stat(spool); !os.IsNotExist(err) {
		t.Fatalf("plain spool %s still exists after unregister (err=%v)", spool, err)
	}
}

func TestUnregisterIsIdempotent(t *testing.T) {
	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		up.handler(w, req)
	}))
	defer srv.Close()

	r := newTestRelay(t, srv.URL)
	if _, err := r.register("agent-a"); err != nil {
		t.Fatalf("register: %v", err)
	}

	if found := r.unregister("agent-a"); !found {
		t.Fatal("first unregister(agent-a) = false, want true")
	}
	if found := r.unregister("agent-a"); found {
		t.Fatal("second unregister(agent-a) = true, want false (already retired, not an error)")
	}
	if found := r.unregister("never-registered"); found {
		t.Fatal("unregister(never-registered) = true, want false")
	}
}

// TestResumeFromSpoolsSkipsTombstoned: a relay restart must not resurrect a
// retired agent whose spool was tombstoned by a prior 410.
func TestResumeFromSpoolsSkipsTombstoned(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "agent-a.chan")
	retired := filepath.Join(dir, "leaked-fixture-z1.chan"+tombstoneSuffix)
	for _, p := range []string{live, retired} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	r := &relay{
		server:     "http://127.0.0.1:1",
		runtimeDir: dir,
		client:     &http.Client{},
		loops:      make(map[string]*agentLoop),
	}
	defer r.unregister("agent-a")

	if got := resumeFromSpools(r, dir); got != 1 {
		t.Fatalf("resumeFromSpools = %d, want 1 (the tombstoned agent must be skipped)", got)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.loops["leaked-fixture-z1"]; ok {
		t.Fatal("resumeFromSpools resurrected a tombstoned agent")
	}
	if _, ok := r.loops["agent-a"]; !ok {
		t.Fatal("resumeFromSpools did not resume the live agent")
	}
}

// TestReregisterAfterTombstoneWorks: pruning must not permanently blacklist an
// id — an explicit re-registration (the normal path a relaunched agent takes)
// must succeed on the first try and start polling again, tombstone or not.
func TestReregisterAfterTombstoneWorks(t *testing.T) {
	up := &goneUpstream{gone: "leaked-fixture-z1", polls: make(map[string]int)}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()

	r := newTestRelay(t, srv.URL)
	if _, err := r.register("leaked-fixture-z1"); err != nil {
		t.Fatalf("register: %v", err)
	}
	waitForLoopGone(t, r, "leaked-fixture-z1")

	// The agent comes back for real: the upstream no longer answers 410 for it.
	up.mu.Lock()
	up.gone = ""
	up.mu.Unlock()

	spool, err := r.register("leaked-fixture-z1")
	if err != nil {
		t.Fatalf("re-register after tombstone: %v", err)
	}
	defer r.unregister("leaked-fixture-z1")

	r.mu.Lock()
	_, watched := r.loops["leaked-fixture-z1"]
	r.mu.Unlock()
	if !watched {
		t.Fatal("re-registered agent is not back on the watch list")
	}
	if _, err := os.Stat(spool + tombstoneSuffix); !os.IsNotExist(err) {
		t.Fatalf("stale tombstone %s still present after re-registration (err=%v)", spool+tombstoneSuffix, err)
	}

	// And it must actually be polled again, not just present in the map.
	before := up.pollCount("leaked-fixture-z1")
	time.Sleep(300 * time.Millisecond)
	if after := up.pollCount("leaked-fixture-z1"); after <= before {
		t.Errorf("re-registered agent was not polled: %d → %d polls", before, after)
	}
}

// A 500 is the server being broken, not the channel being gone: the relay must
// keep retrying, because surviving a server restart is the whole point.
func TestUpstream500KeepsRetrying(t *testing.T) {
	var polls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		polls++
		mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "boom"})
	}))
	defer srv.Close()

	r := newTestRelay(t, srv.URL)
	if _, err := r.register("real-agent"); err != nil {
		t.Fatalf("register: %v", err)
	}
	defer r.unregister("real-agent")

	time.Sleep(200 * time.Millisecond)
	r.mu.Lock()
	_, still := r.loops["real-agent"]
	r.mu.Unlock()
	if !still {
		t.Fatal("relay dropped a channel on HTTP 500 — only 410 is terminal")
	}
	mu.Lock()
	defer mu.Unlock()
	if polls == 0 {
		t.Fatal("relay never polled")
	}
}

// ── Backoff + error-log throttling (robots-dcgg) ─────────────────────────────
// The live defect: a permanently failing poll retried on a flat 2s cadence
// forever, writing an identical error line each time — ~25 MiB/day of log for
// a poll that could never succeed. Backoff bounds the retry rate; the throttle
// bounds the log rate.

func TestNextBackoffDoublesToCap(t *testing.T) {
	got := []time.Duration{}
	d := reconnectDelay
	for i := 0; i < 8; i++ {
		got = append(got, d)
		d = nextBackoff(d)
	}
	want := []time.Duration{
		2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		30 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("backoff step %d = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestErrorLogThrottleSuppressesRepeats(t *testing.T) {
	var th errorLogThrottle
	logged := 0
	for i := 1; i <= 1000; i++ {
		if logIt, _ := th.observe("HTTP 502 Bad Gateway"); logIt {
			logged++
		}
	}
	// 1st (fresh) + decade milestones 10, 100, 1000.
	if logged != 4 {
		t.Errorf("1000 identical errors produced %d log lines, want 4", logged)
	}
}

func TestErrorLogThrottleNewMessageAlwaysLogs(t *testing.T) {
	var th errorLogThrottle
	th.observe("HTTP 502")
	th.observe("HTTP 502")
	logIt, count := th.observe("connection refused")
	if !logIt || count != 1 {
		t.Errorf("a changed error message must log immediately: logIt=%v count=%d", logIt, count)
	}
	// And the run restarts: the old message is fresh again after a change.
	logIt, count = th.observe("HTTP 502")
	if !logIt || count != 1 {
		t.Errorf("returning to a prior message is a fresh run: logIt=%v count=%d", logIt, count)
	}
}

func TestErrorLogThrottleRecoveredReportsAndResets(t *testing.T) {
	var th errorLogThrottle
	for i := 0; i < 37; i++ {
		th.observe("HTTP 502")
	}
	if n := th.recovered(); n != 37 {
		t.Errorf("recovered() = %d, want 37", n)
	}
	if n := th.recovered(); n != 0 {
		t.Errorf("second recovered() = %d, want 0", n)
	}
	// After recovery the same message is fresh again.
	if logIt, count := th.observe("HTTP 502"); !logIt || count != 1 {
		t.Errorf("post-recovery observe: logIt=%v count=%d, want true/1", logIt, count)
	}
}

func TestIsDecade(t *testing.T) {
	for _, n := range []int{10, 100, 1000, 10000} {
		if !isDecade(n) {
			t.Errorf("isDecade(%d) = false, want true", n)
		}
	}
	for _, n := range []int{1, 2, 9, 11, 20, 50, 99, 101, 200, 999, 1001} {
		if isDecade(n) {
			t.Errorf("isDecade(%d) = true, want false", n)
		}
	}
}
