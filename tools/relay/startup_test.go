package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Regression coverage for robots-mpr3: the relay used to replay every spooled
// agent BEFORE binding its control socket, so /health was unanswerable for the
// whole replay (~7s for 206 agents on 2026-08-05). `ensure-up.sh` read that as
// "relay is dead", force-restarted the perfectly healthy mid-startup relay, and
// then gave up — silently breaking agent enrollment.

// TestResumeFromSpools covers the extracted resume walk on its own: every valid
// .chan file becomes a registered agent, and nothing else in the runtime dir
// does.
func TestResumeFromSpools(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{
		"agent-a.chan",     // resumed
		"agent-b.chan",     // resumed
		"Not A Slug.chan",  // invalid id — skipped
		"relay.sock",       // not a spool — skipped
		"agent-c.chan.tmp", // wrong suffix — skipped
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested.chan"), 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}

	r := &relay{
		server:     "http://127.0.0.1:1", // never reachable; poll loops just error
		runtimeDir: dir,
		client:     &http.Client{},
		loops:      make(map[string]*agentLoop),
	}
	defer func() {
		for _, id := range []string{"agent-a", "agent-b"} {
			r.unregister(id)
		}
	}()

	if got := resumeFromSpools(r, dir); got != 2 {
		t.Fatalf("resumeFromSpools = %d, want 2", got)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.loops) != 2 {
		t.Fatalf("registry has %d loops, want 2 (%v)", len(r.loops), r.loops)
	}
	for _, id := range []string{"agent-a", "agent-b"} {
		if _, ok := r.loops[id]; !ok {
			t.Errorf("agent %q was not resumed from its spool", id)
		}
	}
}

// TestResumeFromSpoolsMissingDir: an absent runtime dir resumes nothing and does
// not panic (the pre-fix code silently swallowed this too).
func TestResumeFromSpoolsMissingDir(t *testing.T) {
	r := &relay{
		server:     "http://127.0.0.1:1",
		runtimeDir: "/nonexistent/parlay-runtime",
		client:     &http.Client{},
		loops:      make(map[string]*agentLoop),
	}
	if got := resumeFromSpools(r, r.runtimeDir); got != 0 {
		t.Fatalf("resumeFromSpools on missing dir = %d, want 0", got)
	}
}

// TestResumeFromSpoolsRecordsEveryChannelItBroughtUp: the durable answer to
// "did my agents come back after the restart?" — one `resumed` line per channel
// the walk brought up, and nothing for the files it skipped. Without it a
// restart's consequences are only visible in a log the operator has to find.
func TestResumeFromSpoolsRecordsEveryChannelItBroughtUp(t *testing.T) {
	t.Setenv("PARLAY_RELAY_DELIVERY_LOG", "")
	dir := t.TempDir()
	for _, name := range []string{"agent-b.chan", "agent-a.chan", "Not A Slug.chan", "agent-c.chan.retired"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	r := &relay{
		server:     "http://127.0.0.1:1",
		runtimeDir: dir,
		client:     &http.Client{},
		loops:      make(map[string]*agentLoop),
	}
	t.Cleanup(func() { r.shutdown(&http.Server{}) })

	if got := resumeFromSpools(r, dir); got != 2 {
		t.Fatalf("resumeFromSpools = %d, want 2", got)
	}

	entries, exists := r.readDelivery(maxDeliveryLimit, "")
	if !exists {
		t.Fatalf("no ledger at %s after resuming two channels", r.deliveryPath())
	}
	resumed := deliveryEvents(entries, deliveryResumed)
	if len(resumed) != 2 {
		t.Fatalf("resumed entries = %+v, want one per channel brought up (2)", resumed)
	}
	ids := []string{resumed[0].Agent, resumed[1].Agent}
	sortStrings(ids)
	if ids[0] != "agent-a" || ids[1] != "agent-b" {
		t.Errorf("resumed agents = %v, want agent-a and agent-b — a row for a file the walk skipped (an invalid id, a tombstone) would claim a channel came back that never did", ids)
	}
	for _, e := range resumed {
		if e.Ts == "" {
			t.Errorf("resumed entry %+v has no stamp; a row nobody can place in time is not an answer", e)
		}
		if e.Event == deliveryStarted {
			t.Errorf("the resume walk wrote a %q row; starting the process is main()'s event, not the walk's", deliveryStarted)
		}
	}
}

// TestResumeRecordsAChannelAlreadyHeldByFlag: the row exists so that SILENCE is
// unambiguous. A channel brought up by -agents and then seen again by the walk
// must still get one, or an operator diffing a restart's `delivery-ended` rows
// against `resumed` rows could not tell "did not come back" from "came back by
// flag".
func TestResumeRecordsAChannelAlreadyHeldByFlag(t *testing.T) {
	t.Setenv("PARLAY_RELAY_DELIVERY_LOG", "")
	dir := t.TempDir()
	r := &relay{
		server:     "http://127.0.0.1:1",
		runtimeDir: dir,
		client:     &http.Client{},
		loops:      make(map[string]*agentLoop),
	}
	t.Cleanup(func() { r.shutdown(&http.Server{}) })

	// -agents registers before the socket binds; the walk then finds its spool.
	if _, err := r.register("agent-a"); err != nil {
		t.Fatalf("startup register: %v", err)
	}
	if got := resumeFromSpools(r, dir); got != 1 {
		t.Fatalf("resumeFromSpools = %d, want 1 (the walk is idempotent)", got)
	}

	resumed := deliveryEvents(deliveryEntries(t, r), deliveryResumed)
	if len(resumed) != 1 || resumed[0].Agent != "agent-a" {
		t.Fatalf("resumed entries = %+v, want exactly one for agent-a", resumed)
	}
}

// TestControlSocketBindsBeforeSpoolResume runs the real binary and asserts the
// ordering that is the actual fix: "up — ..." (socket bound and serving) is
// logged BEFORE the first "resumed agent" line, and /health answers while the
// resume is still running. This is an exact ordering assertion on the process's
// own log, so it does not depend on how long a replay happens to take.
func TestControlSocketBindsBeforeSpoolResume(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the relay binary; skipped under -short")
	}

	// Deliberately NOT t.TempDir(): its long generated name plus "/relay.sock"
	// can exceed the ~104-byte sun_path limit on macOS.
	dir, err := os.MkdirTemp("", "prlyr")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	bin := buildRelayBinary(t, dir)

	// Enough spools that a pre-fix binary would still be replaying when we probe.
	const spools = 40
	for i := 0; i < spools; i++ {
		name := filepath.Join(dir, "resume-agent-"+strconv.Itoa(i)+".chan")
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("seed spool: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Upstream is a closed port: every resumed agent's poll loop fails fast and
	// keeps retrying, which is exactly the noisy-startup case from the field.
	cmd := exec.CommandContext(ctx, bin,
		"-server", "http://127.0.0.1:1",
		"-runtime-dir", dir,
	)
	// Pin the ledger on for the child: this test reads it, and an ambient
	// PARLAY_RELAY_DELIVERY_LOG=0 in the developer's shell would otherwise make
	// the assertion below fail for a reason that has nothing to do with the code.
	cmd.Env = append(os.Environ(), "PARLAY_RELAY_DELIVERY_LOG=")
	var stderr syncBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	sock := filepath.Join(dir, "relay.sock")
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
		Timeout: 2 * time.Second,
	}

	// /health must come up promptly — it no longer waits on the spool replay.
	// A pre-fix binary spends the whole replay unbound and fails this.
	deadline := time.Now().Add(15 * time.Second)
	healthy := false
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://relay/health")
		if err == nil {
			body := make([]byte, 64)
			n, _ := resp.Body.Read(body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body[:n]), `"ok":true`) {
				healthy = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthy {
		t.Fatalf("/health never answered on %s\nstderr:\n%s", sock, stderr.String())
	}

	// Wait for the resume walk to finish so both markers are present.
	for time.Now().Before(deadline) {
		if strings.Contains(stderr.String(), "spool resume complete") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	logs := stderr.String()
	upAt := strings.Index(logs, "up — server=")
	resumedAt := strings.Index(logs, "resumed agent ")
	doneAt := strings.Index(logs, "spool resume complete")
	if upAt < 0 {
		t.Fatalf("relay never logged its bind line\nstderr:\n%s", logs)
	}
	if resumedAt < 0 || doneAt < 0 {
		t.Fatalf("relay never resumed the seeded spools\nstderr:\n%s", logs)
	}
	if upAt > resumedAt {
		t.Errorf("control socket was bound AFTER the spool replay started "+
			"(up at %d, first resume at %d) — /health is unanswerable during "+
			"replay again, which is the robots-mpr3 defect\nstderr:\n%s",
			upAt, resumedAt, logs)
	}
	if !strings.Contains(logs, "spool resume complete — 40 agent(s) resumed") {
		t.Errorf("resume did not report all %d seeded agents\nstderr:\n%s", spools, logs)
	}

	// The durable half of the same event. `started` must be the FIRST line —
	// read order is write order, and a trail whose start row lands after the
	// channels it brought up would misdate the restart it exists to explain —
	// and every channel the walk reported must have its own `resumed` row, or
	// the ledger cannot answer "which agents came back?".
	ledger := readLedgerFile(t, filepath.Join(dir, deliveryFileName))
	if len(ledger) == 0 || ledger[0].Event != deliveryStarted {
		t.Fatalf("first ledger line = %+v, want %q; the relay's own start must be the first row it writes", ledger, deliveryStarted)
	}
	if starts := len(deliveryEvents(ledger, deliveryStarted)); starts != 1 {
		t.Errorf("%d %q rows, want exactly 1 for one boot", starts, deliveryStarted)
	}
	resumedIDs := map[string]int{}
	for _, e := range deliveryEvents(ledger, deliveryResumed) {
		resumedIDs[e.Agent]++
	}
	if len(resumedIDs) != spools {
		t.Errorf("ledger holds %d distinct resumed channel(s), want %d — a channel that came back with no row makes the restart unanswerable\nledger:\n%s",
			len(resumedIDs), spools, ledgerSummary(ledger))
	}
	for i := 0; i < spools; i++ {
		if id := "resume-agent-" + strconv.Itoa(i); resumedIDs[id] != 1 {
			t.Errorf("channel %s has %d resumed row(s), want 1\nledger:\n%s", id, resumedIDs[id], ledgerSummary(ledger))
		}
	}
	if ledger[0].Ts == "" {
		t.Errorf("the started row has no stamp: %+v", ledger[0])
	}
}

// TestABootThatCannotTakeTheSocketRecordsNoStart pins what the `started` row
// means: this relay took the control socket and served. Writing it before the
// bind would make every refused second boot (a duplicate relay, the exact case
// ensure-up exists for) look like a relay that came up.
func TestABootThatCannotTakeTheSocketRecordsNoStart(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the relay binary; skipped under -short")
	}
	dir, err := os.MkdirTemp("", "prlyr")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	bin := buildRelayBinary(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := func() (*exec.Cmd, *syncBuffer) {
		cmd := exec.CommandContext(ctx, bin, "-server", "http://127.0.0.1:1", "-runtime-dir", dir)
		cmd.Env = append(os.Environ(), "PARLAY_RELAY_DELIVERY_LOG=")
		var stderr syncBuffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start relay: %v", err)
		}
		return cmd, &stderr
	}

	first, _ := start()
	defer func() {
		_ = first.Process.Kill()
		_, _ = first.Process.Wait()
	}()

	ledgerPath := filepath.Join(dir, deliveryFileName)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if entries := readLedgerFile(t, ledgerPath); len(entries) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if before := len(deliveryEvents(readLedgerFile(t, ledgerPath), deliveryStarted)); before != 1 {
		t.Fatalf("the first relay recorded %d %q row(s), want 1", before, deliveryStarted)
	}

	second, secondErr := start()
	secondErrStr := secondErr.String()
	if err := second.Wait(); err == nil {
		t.Fatalf("a second relay bound the socket another relay holds; it must fail instead\nstderr:\n%s", secondErrStr)
	}

	if after := len(deliveryEvents(readLedgerFile(t, ledgerPath), deliveryStarted)); after != 1 {
		t.Errorf("a boot that could not take the socket recorded %d %q row(s) instead of 1 — the row would claim a relay came up that never served\nstderr:\n%s",
			after, deliveryStarted, secondErrStr)
	}
}

// buildRelayBinary compiles the relay for a test that needs the real main().
// Go's build cache makes the second call in a package run cheap; dir keeps the
// socket path short (macOS caps sun_path near 104 bytes).
func buildRelayBinary(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "relay-bin")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build relay: %v\n%s", err, out)
	}
	return bin
}

// readLedgerFile decodes the raw ledger lines a relay process wrote. The
// in-process tests use readDelivery; this is for rows written by a subprocess.
// A missing file is an empty trail here — the caller asserts on its contents.
func readLedgerFile(t *testing.T, path string) []deliveryEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", path, err)
	}
	var out []deliveryEntry
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e deliveryEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("decode %s line %q: %v", path, line, err)
		}
		out = append(out, e)
	}
	return out
}

// ledgerSummary renders a ledger for a failure message without turning a large
// trail into pages of output.
func ledgerSummary(entries []deliveryEntry) string {
	if len(entries) <= 6 {
		return fmt.Sprintf("%+v", entries)
	}
	return fmt.Sprintf("%d entries; first=%+v last=%+v", len(entries), entries[0], entries[len(entries)-1])
}

// syncBuffer is a bytes.Buffer safe for concurrent Write (os/exec's stderr pump
// goroutine) and String (the test's assertions).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
