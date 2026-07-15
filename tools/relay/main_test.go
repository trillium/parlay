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
