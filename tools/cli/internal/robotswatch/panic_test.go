// Covers the panic-isolation contract: a single bad pass must never bring
// down the daemon. Mirrors index.ts's runPollOnce try/catch and tail.ts's
// inline try/catch around tick() — here, a real panic (forced via an
// unwritable state dir) must be recovered and logged, not propagated.
package robotswatch

import (
	"io"
	"os"
	"strings"
	"testing"
)

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = orig
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestRunPollOnceRecoversFromPanic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)

	// writeCursor's temp-file creation will panic once the robots-watch
	// subdir is read-only — runPollOnce must recover from that, not crash.
	swDir := stateDir()
	if err := os.MkdirAll(swDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(swDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(swDir, 0o755)

	out := captureStderr(t, func() { runPollOnce(false) })
	if !strings.Contains(out, "poll pass failed") {
		t.Fatalf("expected a recovered-panic log line, got %q", out)
	}
}

func TestTickIsolatedRecoversFromPanic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)

	eventsFile := dir + "/events.jsonl"
	mustWriteFile(t, eventsFile, `{"id":"robots-aaa"}`+"\n")
	t.Setenv("ROBOTS_EVENTS_FILE", eventsFile)

	writeOffset(0) // prime a valid offset while the dir is still writable

	swDir := stateDir()
	if err := os.Chmod(swDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(swDir, 0o755)

	out := captureStderr(t, func() { tickIsolated(false) })
	if !strings.Contains(out, "pass failed") {
		t.Fatalf("expected a recovered-panic log line, got %q", out)
	}
}
