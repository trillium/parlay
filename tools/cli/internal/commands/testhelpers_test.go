package commands

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// TestMain neutralizes the one ambient input a per-test HOME redirect does
// NOT cover. Redirecting HOME already isolates config.StateHome() (and so the
// spawn account's config.toml), because os.UserHomeDir honors $HOME — but
// PARLAY_SPAWN_DEFAULT_ACCOUNT out-ranks that file, so an exported one on the
// developer's shell would inject an --account into a spawner argv no test
// asked for. Clearing it here isolates every current and future test in this
// package by construction rather than each one remembering.
func TestMain(m *testing.M) {
	_ = os.Setenv(config.SpawnAccountEnv, "")
	os.Exit(m.Run())
}

// captureStdout runs fn with os.Stdout redirected to an in-memory pipe and
// returns everything it printed. Command functions here write straight to
// os.Stdout via fmt.Println/Printf (no injectable writer, matching the TS
// original's plain console.log), so tests intercept at the file-descriptor
// level instead.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	w.Close()
	os.Stdout = orig
	return <-done
}

// withExitTrap installs a RecordingExit for the duration of fn and returns
// the exit code fn triggered via httpc.Die, plus whether it exited at all —
// mirrors identity-test-harness.ts's trapExit().
func withExitTrap(t *testing.T, fn func()) (code int, exited bool) {
	t.Helper()
	orig := httpc.Exit
	httpc.Exit = testsupport.RecordingExit()
	defer func() { httpc.Exit = orig }()
	return testsupport.Capture(fn)
}
