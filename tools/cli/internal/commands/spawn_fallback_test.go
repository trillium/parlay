// Coverage for execSpawner/Spawn's fail-closed fallback (docs/scope-go-spawn.md
// Stage 4): an auto-picked parlay-bin that cannot even start falls back to
// bin/parlay-spawn loudly; an explicit PARLAY_SPAWN_IMPL=go pick does not; and
// a parlay-bin that starts fine but exits nonzero (ordinary business-logic
// refusal) propagates that code untouched, never triggering a fallback. Every
// binary here is a hermetic shell stub on a t.TempDir() PATH — nothing in this
// file can launch a real agent.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// brokenBinary writes an executable named `name` whose shebang points at a
// nonexistent interpreter — exec(2) fails before the process ever starts, so
// cmd.Run() returns a fork/exec-level error rather than an *exec.ExitError.
// This is the "genuinely broken Go binary" execSpawner must fall back on,
// distinct from fakeSpawner's ordinary nonzero exit.
func brokenBinary(t *testing.T, dir, name string) {
	t.Helper()
	script := "#!/nonexistent/parlay-test-interpreter-xyz\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestSpawnFallsBackToBashWhenAutoPickedParlayBinFailsToStart(t *testing.T) {
	dir := t.TempDir()
	brokenBinary(t, dir, "parlay-bin")
	bashArgv := fakeSpawner(t, dir, "parlay-spawn", 0)
	t.Setenv("PATH", dir)
	t.Setenv("PARLAY_SPAWN_IMPL", "")

	stderr := captureStderr(t, func() {
		Spawn([]string{"some-id", "Some Name", "#abcdef", "task"})
	})

	if !strings.Contains(stderr, "falling back to bin/parlay-spawn") {
		t.Errorf("Spawn() stderr = %q, want a loud fallback message", stderr)
	}
	got, err := os.ReadFile(bashArgv)
	if err != nil {
		t.Fatalf("parlay-spawn fallback did not run: %v", err)
	}
	want := "some-id\nSome Name\n#abcdef\ntask\n"
	if string(got) != want {
		t.Errorf("fallback argv = %q, want %q", got, want)
	}
}

func TestSpawnExplicitGoOverrideBrokenBinaryFailsLoudlyNoFallback(t *testing.T) {
	dir := t.TempDir()
	brokenBinary(t, dir, "parlay-bin")
	bashArgv := fakeSpawner(t, dir, "parlay-spawn", 0)
	t.Setenv("PATH", dir)
	t.Setenv("PARLAY_SPAWN_IMPL", "go")

	var stderr string
	code, exited := withExitTrap(t, func() {
		stderr = captureStderr(t, func() {
			Spawn([]string{"some-id", "Some Name", "#abcdef", "task"})
		})
	})

	if !exited || code != config.ExitRuntime {
		t.Fatalf("Spawn() exited=%v code=%d, want exited=true code=%d", exited, code, config.ExitRuntime)
	}
	if strings.Contains(stderr, "falling back") {
		t.Errorf("Spawn() stderr = %q, must not attempt a fallback on an explicit override", stderr)
	}
	if _, err := os.ReadFile(bashArgv); err == nil {
		t.Error("parlay-spawn ran despite an explicit PARLAY_SPAWN_IMPL=go pick")
	}
}

func TestSpawnPropagatesOrdinaryNonzeroExitWithoutFallback(t *testing.T) {
	dir := t.TempDir()
	fakeSpawner(t, dir, "parlay-bin", 7)
	bashArgv := fakeSpawner(t, dir, "parlay-spawn", 0)
	t.Setenv("PATH", dir)
	t.Setenv("PARLAY_SPAWN_IMPL", "")

	code, exited := withExitTrap(t, func() {
		Spawn([]string{"some-id", "Some Name", "#abcdef", "task"})
	})

	if !exited || code != 7 {
		t.Fatalf("Spawn() exited=%v code=%d, want exited=true code=7 (parlay-bin's own exit code)", exited, code)
	}
	if _, err := os.ReadFile(bashArgv); err == nil {
		t.Error("parlay-spawn ran even though parlay-bin exited normally (nonzero, but not a start failure)")
	}
}
