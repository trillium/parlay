// task-qyu8q scope 3: the PARLAY_SPAWN_IMPL=bash escape hatch sets
// PARLAY_SPAWN_VIA_CLI=1 before exec'ing bin/parlay-spawn — this is the
// handshake the bash script requires to prove the call came through the CLI,
// not a direct invocation. (The default in-process path has no handshake to
// set — there is no cross-binary call left to police; task-42qot.)
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

func TestSpawnSetsViaCliHandshakeEnv(t *testing.T) {
	t.Setenv(config.SpawnImplEnv, "bash")
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
