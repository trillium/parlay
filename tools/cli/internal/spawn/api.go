// Package spawn is the Go port of bin/parlay-spawn + bin/context-reset
// (+ the bin/reincarnate alias), folded into the parlay CLI (task-42qot) from
// the former standalone tools/parlay-bin module. See docs/scope-go-spawn.md
// for the scoping analysis this port follows.
//
// Each exported entry point takes the verb's argv (without the verb itself)
// and returns a process exit code; the CLI dispatcher owns os.Exit.
package spawn

// RunSpawn launches a new background claude agent — the port of
// bin/parlay-spawn's named / --ephemeral / batch invocation shapes.
func RunSpawn(args []string) int { return runSpawnCommand(args) }

// RunReset performs a clean self-restart for a persistent agent — the port
// of bin/context-reset (and its bin/reincarnate alias). Unlike
// bin/context-reset, this port does not echo the pinned handoff to the pane
// on a clean end.
func RunReset(args []string) int { return runResetCommand(args) }

// RunSubprocessSpawn starts a detached subprocess session (the herdr-free
// launcher path).
func RunSubprocessSpawn(args []string) int { return runSubprocessSpawnCommand(args) }

// RunSubprocessStop stops a subprocess-spawn session.
func RunSubprocessStop(args []string) int { return runSubprocessStopCommand(args) }

// RunSubprocessPing exits 0 if a subprocess-spawn session is alive, 1 if not.
func RunSubprocessPing(args []string) int { return runSubprocessPingCommand(args) }
