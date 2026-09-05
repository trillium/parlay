// `parlay spawn` — runs the Go spawn pipeline in-process (task-42qot: the
// former standalone tools/parlay-bin module, folded into this CLI as
// internal/spawn). `parlay spawn` is the sole entry point for launching
// agents (task-qyu8q scope 3) — the ONLY one now that bin/parlay-spawn and
// its PARLAY_SPAWN_IMPL=bash escape hatch are deleted, so the property holds
// structurally rather than by a cross-binary handshake.
package commands

import (
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

// runSpawnArgv executes one spawn request through the in-process pipeline.
// It is a package var so `parlay launch`'s tests can observe the argv this
// dispatch is handed without a second process to watch from outside.
var runSpawnArgv = func(argv []string) { httpc.Exit(spawn.RunSpawn(argv)) }
