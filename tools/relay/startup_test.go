package main

import (
	"bytes"
	"context"
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

	bin := filepath.Join(dir, "relay-bin")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build relay: %v\n%s", err, out)
	}

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
