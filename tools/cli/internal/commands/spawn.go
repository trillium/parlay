// `parlay spawn` — thin wrapper that resolves the spawner binary and runs it,
// forwarding all arguments verbatim.
//
// parlay-spawn's full CLI surface (id, name, color, prompt, --cwd, --model,
// --kind, --mode, --claim, --worktree, --batch, …) is intentionally NOT
// reimplemented here — this subcommand is the "how you call a spawner from
// within the parlay CLI" surface, not the spawner itself.
package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// Spawn resolves the spawner binary (parlay-bin or parlay-spawn, in that
// preference order) and runs it with inherited stdio, forwarding all argv
// verbatim.  Exit codes propagate so the caller sees exactly what the spawner
// reported.
func Spawn(argv []string) {
	if helpWanted("spawn", argv) {
		return
	}

	bin, spawnArgv, err := resolveSpawner(argv)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay spawn: %v", err), config.ExitRuntime)
		return
	}

	cmd := exec.Command(bin, spawnArgv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		httpc.Die(fmt.Sprintf("parlay spawn: %s failed: %v", filepath.Base(bin), err), config.ExitRuntime)
	}
}
