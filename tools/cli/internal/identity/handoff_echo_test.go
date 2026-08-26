// Covers printHandoffContent — the stdout echo that runs immediately before
// `identity --submit` execs context-reset, while claude is still alive and the
// pane's scrollback is still writable.
//
// The bound on that echo is the part worth pinning. It runs SYNCHRONOUSLY in
// front of the reset exec, so a `handoff` binary that never returns (an
// unreachable store, a wedged dolt) would strand the agent in a session it has
// already announced it is leaving. A missed echo costs scrollback; a missed
// reset costs the session — so the timeout must always release.
package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubHandoffShow puts a fake `handoff` on PATH whose `show` subcommand runs
// body (a /bin/sh fragment). Distinct from say_test.go's stubHandoffStore,
// which stubs `list` for the death-window guard.
func stubHandoffShow(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n  show) " + body + ";;\n  *) exit 3;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "handoff"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// captureStdout lives in testharness_test.go — printHandoffContent hands
// os.Stdout to the child process, which that helper's os.Pipe swap covers.

func TestPrintHandoffContentEchoesShowOutput(t *testing.T) {
	stubHandoffShow(t, `printf 'HANDOFF BODY for %s\n' "$2"; exit 0`)

	out := captureStdout(t, func() { printHandoffContent("handoff-abc") })

	if !strings.Contains(out, "HANDOFF BODY for handoff-abc") {
		t.Fatalf("expected the handoff body on stdout, got %q", out)
	}
}

// The regression this file exists for: a blocking `handoff show` must not hold
// the submit path open. The call has to return so runInheritEnv reaches
// context-reset/reincarnate.
func TestPrintHandoffContentReturnsWhenHandoffBlocks(t *testing.T) {
	stubHandoffShow(t, `sleep 60`)

	orig := printHandoffTimeout
	printHandoffTimeout = 150 * time.Millisecond
	t.Cleanup(func() { printHandoffTimeout = orig })

	returned := make(chan string, 1)
	go func() {
		returned <- captureStdout(t, func() { printHandoffContent("handoff-stuck") })
	}()

	select {
	case out := <-returned:
		if !strings.Contains(out, "timed out") {
			t.Fatalf("expected the timeout to be announced, got %q", out)
		}
		if !strings.Contains(out, "continuing to reset") {
			t.Fatalf("expected the notice to say the reset still proceeds, got %q", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("printHandoffContent never returned — a hung `handoff show` would strand the submit before context-reset")
	}
}

// A `handoff` that fails outright (missing binary, non-zero exit) is a warning,
// never fatal — same reason as the timeout: the reset must still fire.
func TestPrintHandoffContentWarnsButReturnsOnFailure(t *testing.T) {
	stubHandoffShow(t, `exit 4`)

	out := captureStdout(t, func() { printHandoffContent("handoff-broken") })

	if !strings.Contains(out, "could not show handoff handoff-broken") {
		t.Fatalf("expected a non-fatal warning naming the handoff, got %q", out)
	}
	if strings.Contains(out, "timed out") {
		t.Fatalf("a plain non-zero exit must not be reported as a timeout, got %q", out)
	}
}
