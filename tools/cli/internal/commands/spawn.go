// `parlay spawn` — runs the Go spawn pipeline in-process (task-42qot: the
// former standalone tools/parlay-bin module, folded into this CLI as
// internal/spawn). `parlay spawn` is the sole public entry point for
// launching agents (task-qyu8q scope 3) — by construction now, not by a
// cross-binary PARLAY_SPAWN_VIA_CLI handshake.
package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/spawn"
)

// Spawn dispatches a spawn request per runSpawnArgv. Exit codes propagate
// via httpc.Exit so the caller sees exactly what the pipeline reported.
func Spawn(argv []string) {
	if helpWanted("spawn", argv) {
		return
	}
	runSpawnArgv(argv)
}

// Reset performs a clean self-restart for a persistent agent (the port of
// bin/context-reset; `parlay reincarnate` is its legacy-named alias).
func Reset(argv []string) {
	if helpWanted("reset", argv) {
		return
	}
	httpc.Exit(spawn.RunReset(argv))
}

// SpawnWatchdog runs one post-launch liveness watch — the detached child
// `parlay spawn` arms after a successful launch, one arm per launcher.
func SpawnWatchdog(argv []string) {
	if helpWanted("spawn-watchdog", argv) {
		return
	}
	httpc.Exit(spawn.RunSpawnWatchdog(argv))
}

// SubprocessSpawn starts a detached subprocess session — the herdr-free
// launcher path, also reachable via `parlay spawn --subprocess`.
func SubprocessSpawn(argv []string) { httpc.Exit(spawn.RunSubprocessSpawn(argv)) }

// SubprocessStop stops a subprocess-spawn session.
func SubprocessStop(argv []string) { httpc.Exit(spawn.RunSubprocessStop(argv)) }

// SubprocessPing exits 0 if a subprocess-spawn session is alive, 1 if not.
func SubprocessPing(argv []string) { httpc.Exit(spawn.RunSubprocessPing(argv)) }

// runSpawnArgv executes one spawn request: in-process by default, or via
// bin/parlay-spawn when config.SpawnImpl() says "bash" (PARLAY_SPAWN_IMPL
// env var, else config.toml's `spawnImpl` key) — the documented escape
// hatch while the bash spawner still exists. "go" and empty both select
// in-process; any other non-empty value is a usage error — a typo must not
// silently resolve to a spawner the operator did not pick.
func runSpawnArgv(argv []string) {
	impl := strings.TrimSpace(config.SpawnImpl())
	switch strings.ToLower(impl) {
	case "", "go":
		httpc.Exit(spawn.RunSpawn(argv))
	case "bash":
		execBashSpawner(argv)
	default:
		httpc.Die(fmt.Sprintf("parlay spawn: invalid %s (or config.toml spawnImpl) value %q — must be \"go\" or \"bash\"", config.SpawnImplEnv, impl), config.ExitUsage)
	}
}

// execBashSpawner runs bin/parlay-spawn with inherited stdio and the
// PARLAY_SPAWN_VIA_CLI=1 handshake bash still checks (it proves the caller
// went through `parlay spawn` rather than invoking the script directly),
// propagating its exit code via httpc.Exit — the injectable exit hook,
// never a raw os.Exit, so tests can assert it without tearing down the
// test binary.
func execBashSpawner(argv []string) {
	abs, lookErr := exec.LookPath("parlay-spawn")
	if lookErr != nil || abs == "" {
		httpc.Die(fmt.Sprintf("parlay spawn: %s=bash (or config.toml spawnImpl=bash) demands parlay-spawn, but it is not on PATH", config.SpawnImplEnv), config.ExitRuntime)
		return
	}
	cmd := exec.Command(abs, argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "PARLAY_SPAWN_VIA_CLI=1")
	err := cmd.Run()
	if err == nil {
		return
	}
	if ee, ok := err.(*exec.ExitError); ok {
		httpc.Exit(ee.ExitCode())
		return
	}
	httpc.Die(fmt.Sprintf("parlay spawn: parlay-spawn failed: %v", err), config.ExitRuntime)
}
