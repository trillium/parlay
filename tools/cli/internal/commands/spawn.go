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

// Spawn resolves the spawner binary via resolveSpawnerChoice's precedence
// (config.SpawnImpl() override, else parlay-bin on PATH, else bash) and runs
// it with inherited stdio, forwarding all argv verbatim. Exit codes propagate
// so the caller sees exactly what the spawner reported.
//
// A parlay-bin picked by auto-preference (not an explicit PARLAY_SPAWN_IMPL=go)
// that fails to even START — a stale/corrupt build, wrong architecture, a
// permission error — falls back to bin/parlay-spawn instead of leaving the
// operator with no agent and a confusing exec error; see execSpawner. The
// fallback always logs to stderr so it can never be mistaken for a normal
// spawn.
func Spawn(argv []string) {
	if helpWanted("spawn", argv) {
		return
	}

	choice, err := resolveSpawnerChoice(argv)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay spawn: %v", err), config.ExitRuntime)
		return
	}

	if !execSpawner(choice) {
		return
	}

	// choice's Go binary could not even start, and the pick was auto — an
	// explicit PARLAY_SPAWN_IMPL=go demand fails loudly instead of reaching
	// here (execSpawner Die's it directly). Fall back to bash, loudly.
	fmt.Fprintf(os.Stderr, "parlay spawn: %s did not start — falling back to bin/parlay-spawn (set %s=bash to force this, or fix the Go binary)\n", choice.name, config.SpawnImplEnv)
	bashAbs, lookErr := exec.LookPath("parlay-spawn")
	if lookErr != nil || bashAbs == "" {
		httpc.Die("parlay spawn: parlay-bin did not start and no fallback (parlay-spawn) is on PATH", config.ExitRuntime)
		return
	}
	execSpawner(spawnerChoice{bin: bashAbs, argv: argv, name: "parlay-spawn", explicit: true})
}

// execSpawner runs choice with inherited stdio and the PARLAY_SPAWN_VIA_CLI=1
// handshake, propagating its exit code via httpc.Exit (the injectable exit
// hook — never a raw os.Exit — so tests can assert it without tearing down
// the test binary).
//
// It returns true only when choice.name is "parlay-bin", the pick was NOT
// explicit, and the process could not even start: a fork/exec-level error
// (*exec.ExitError is excluded), never a plain nonzero exit — that is normal
// business-logic refusal (bad flags, missing model, a beads gate) and must
// propagate untouched. true is Spawn's signal to try the bash fallback;
// every other outcome (success, a propagated exit code, an explicit-pick
// failure) is fully handled here.
func execSpawner(choice spawnerChoice) (tryFallback bool) {
	cmd := exec.Command(choice.bin, choice.argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// task-qyu8q scope 3: `parlay spawn` is the sole public entry point.
	// bin/parlay-spawn refuses to run without this handshake — it proves the
	// caller went through here rather than being invoked directly.
	cmd.Env = append(os.Environ(), "PARLAY_SPAWN_VIA_CLI=1")
	err := cmd.Run()
	if err == nil {
		return false
	}
	if ee, ok := err.(*exec.ExitError); ok {
		httpc.Exit(ee.ExitCode())
		return false
	}
	if choice.name == "parlay-bin" && !choice.explicit {
		return true
	}
	httpc.Die(fmt.Sprintf("parlay spawn: %s failed: %v", filepath.Base(choice.bin), err), config.ExitRuntime)
	return false
}
