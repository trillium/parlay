// Shared test harness for the store-mutating identity verbs. Mirrors
// packages/cli/src/identity-test-harness.ts: a tmp PARLAY_AGENT_HOME, a
// recording httptest server for /api/chat/register-agent, stdout capture,
// and an exit-code trap (via internal/testsupport) — not a test file
// itself, just helpers shared by identity_*_test.go.
package identity

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

type harness struct {
	mu             sync.Mutex
	registerBodies []map[string]any
	server         *httptest.Server
}

// startHarness starts the recording server, points PARLAY_SERVER at it, and
// registers cleanup to restore state. Call once per test (or subtest) — each
// test gets an isolated server since Go's testing.T doesn't share BeforeAll
// across files the way bun:test does.
func startHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/register-agent", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.mu.Lock()
		h.registerBodies = append(h.registerBodies, body)
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	t.Setenv("PARLAY_SERVER", h.server.URL)
	return h
}

// freshHome points PARLAY_AGENT_HOME at a fresh t.TempDir() and returns it.
func freshHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", dir)
	return dir
}

type seedOpts struct {
	Ephemeral     bool
	Name          string
	Color         string
	Reincarnation bool
}

// seedAgent writes a minimal agent store (identity.md frontmatter +
// context.json) on disk, mirroring identity-test-harness.ts's seedAgent.
func seedAgent(t *testing.T, home, id string, opts seedOpts) string {
	t.Helper()
	dir := filepath.Join(home, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := opts.Name
	if name == "" {
		name = id
	}
	color := opts.Color
	if color == "" {
		color = "#123456"
	}
	fmLines := []string{"id: " + id, "name: " + name, `color: "` + color + `"`, "cwd: /tmp/" + id}
	if opts.Ephemeral {
		fmLines = append(fmLines, "ephemeral: true")
	}
	content := "---\n" + joinLines(fmLines) + "\n---\n# Identity — " + id + "\n\n- a durable fact\n"
	if err := os.WriteFile(filepath.Join(dir, "identity.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _ := json.MarshalIndent(map[string]string{"id": id, "name": name, "color": color}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "context.json"), append(ctx, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if opts.Reincarnation {
		if err := os.WriteFile(filepath.Join(dir, "reincarnations.log"), []byte(`{"ts":"old","agent":"`+id+`"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

// trapExit installs a RecordingExit on httpc.Exit and restores the original
// on cleanup, mirroring identity-test-harness.ts's trapExit.
func trapExit(t *testing.T) {
	t.Helper()
	orig := httpc.Exit
	httpc.Exit = testsupport.RecordingExit()
	t.Cleanup(func() { httpc.Exit = orig })
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	out := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		out <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-out
}

// runCapturingExit runs fn (a cmdIdentity/cmdScratchpad-style call) with
// exit-trapping + stdout capture, returning both the log output and the
// (code, ok) result from testsupport.Capture.
func runCapturingExit(t *testing.T, fn func()) (logs string, code int, exited bool) {
	t.Helper()
	trapExit(t)
	logs = captureStdout(t, func() {
		code, exited = testsupport.Capture(fn)
	})
	return
}

// withFakeContextReset puts a no-op executable named "reincarnate" on PATH
// for the duration of the test (restored by t.Setenv). identity.md's
// --submit/--park spawn ContextResetCmd() with inherited stdio and BLOCK
// until it exits (docs/scope-go-cli.md §5 item 10); since neither
// "context-reset" nor "reincarnate" is guaranteed to be installed on PATH in
// a sandboxed test environment, tests that exercise --submit/--park stub one
// in rather than depending on (or risking side effects from) a real,
// possibly session-killing binary.
func withFakeContextReset(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "reincarnate")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
