// task-qyu8q scope 3: `parlay spawn` sets PARLAY_SPAWN_VIA_CLI=1 before
// exec'ing the resolved spawner binary — this is the handshake bin/parlay-spawn
// requires to prove the call came through the CLI, not a direct invocation.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpawnSetsViaCliHandshakeEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.out")

	stub := filepath.Join(dir, "parlay-spawn")
	script := "#!/bin/sh\nenv > " + envFile + "\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")

	Spawn([]string{"some-id", "Some Name", "#abcdef", "task"})

	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("stub did not run / write env: %v", err)
	}
	if !strings.Contains(string(got), "PARLAY_SPAWN_VIA_CLI=1") {
		t.Errorf("spawner env = %q, want PARLAY_SPAWN_VIA_CLI=1", got)
	}
}
