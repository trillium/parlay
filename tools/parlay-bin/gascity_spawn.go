// gascity-spawn / gascity-stop / gascity-ping give bin/parlay-spawn a
// second, herdr-free way to launch a background claude session: a detached
// subprocess instead of a herdr terminal tab. herdr has a known SIGKILL
// failure mode in headless/no-WindowServer environments; this path exists
// as the escape hatch.
//
// The scoping brief for this (Parlay message m16b) called for importing
// github.com/gastownhall/gascity/internal/runtime/subprocess directly. That
// is impossible: Go's internal-package-visibility rule blocks any import of
// an internal/ path from code not rooted at its parent directory, and no
// replace directive changes that — it only changes where source is fetched
// from, not import-path visibility. That half of the argument was re-verified
// on 2026-08-28 and still holds: `ls pkg/` returns exactly `eventexport`.
//
// CORRECTED 2026-08-28 (P0, docs/gascity-integration-contract.md). This block
// also used to reject shelling out to gascity's own `gc` CLI, on the grounds
// that `gc` "requires a city.toml, a dolt DB, k8s client wiring" and "does not
// even build in this environment (missing system lib for a CGO dolt
// dependency)". Every clause of that rejection is now known to be wrong or
// overstated: `gc version` and `gc --help` run outside a city with no database
// and no k8s; upstream 7c817e064 builds clean; and the build failure that was
// observed came from keg-only Homebrew icu4c (a local toolchain gap needing
// CGO_CPPFLAGS, not CGO_CXXFLAGS) plus the captain's local merge branch — not
// from gascity, and not from dolt directly. The integration contract records
// the measured shell-out cost (~34ms floor) and selects a hybrid seam.
//
// None of that changes what this file IS: a from-scratch port of just the
// lifecycle semantics of gascity's internal/runtime/subprocess.Provider
// (detached sh -c child, process-group signaling, SIGTERM-then-SIGKILL stop),
// not an import of it and not a wrapper around `gc`. It contains no Gas City
// code. The `gascity` name here is residue and is misleading; renaming it —
// along with the --gascity flag, the PARLAY_SPAWN_LAUNCHER value, and the
// config key, which must all move together — belongs to P9, not here.
//
// One deliberate design departure from the gascity source: that provider
// tracks liveness via a unix control socket, which requires the process
// that Accept()s on it to stay running for the life of the session — fine
// for gascity, where `gc` is a long-lived daemon, but not for us, where
// `gascity-spawn` and `gascity-stop` are separate one-shot CLI invocations
// with no supervisor process in between. A plain PID file does the same
// cross-process liveness/control job with no persistent listener required:
// a detached child (Setpgid, stdio redirected to /dev/null, parent never
// calls Wait) keeps running under init/the nearest subreaper after the
// spawning CLI process exits, exactly like any other backgrounded Unix
// process — Go does not need to hold a socket open for that to be true.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const gascitySpawnUsage = `Usage: parlay-bin gascity-spawn <agent-id> <command> <workdir> [--state-dir DIR] [--env KEY=VALUE ...] [--worktree-path PATH] [--bead-id ID]
       parlay-bin gascity-stop <agent-id> [--state-dir DIR]
       parlay-bin gascity-ping <agent-id> [--state-dir DIR]

  agent-id          kebab-slug identifying the session
  command           shell command line, run via sh -c
  workdir           working directory for the command

  --state-dir DIR   where the pid/treehouse-path files live
                     (default: ~/.parlay/agents/<agent-id>/gascity, honoring
                     PARLAY_AGENT_HOME — pass this explicitly from
                     bin/parlay-spawn so it always agrees with that script's
                     own $HOME-based AGENT_DIR)
  --env KEY=VALUE   one or more environment overrides for the child
  --worktree-path P a treehouse-leased worktree path; gascity-stop returns
                     it via 'treehouse return' before stopping the process
  --bead-id ID      the beads work item bound to this session (beads-required
                     mode); gascity-stop closes it via its store wrapper
                     before stopping the process
`

// stopGrace is a var, not a const, so tests can shrink it to keep the
// SIGKILL-escalation path fast.
var stopGrace = 5 * time.Second

func defaultGascityStateDir(agentID string) string {
	return filepath.Join(agentHomeDir(agentID), "gascity")
}

func runGascitySpawnCommand(args []string) int {
	if len(args) < 3 {
		fmt.Fprint(os.Stderr, gascitySpawnUsage)
		return 2
	}
	agentID, command, workdir := args[0], args[1], args[2]
	if err := validateKebabSlug(agentID); err != nil {
		fmt.Fprintf(os.Stderr, "gascity-spawn: %v\n", err)
		return 2
	}

	stateDir := defaultGascityStateDir(agentID)
	var envOverrides []string
	var worktreePath string
	var beadID string

	rest := args[3:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--state-dir":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "gascity-spawn: --state-dir requires a value")
				return 2
			}
			stateDir = rest[i+1]
			i++
		case "--env":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "gascity-spawn: --env requires a value")
				return 2
			}
			envOverrides = append(envOverrides, rest[i+1])
			i++
		case "--worktree-path":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "gascity-spawn: --worktree-path requires a value")
				return 2
			}
			worktreePath = rest[i+1]
			i++
		case "--bead-id":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "gascity-spawn: --bead-id requires a value")
				return 2
			}
			beadID = rest[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "gascity-spawn: unknown arg: %s\n", rest[i])
			return 2
		}
	}

	if err := gascitySpawn(stateDir, agentID, command, workdir, envOverrides, worktreePath, beadID); err != nil {
		fmt.Fprintf(os.Stderr, "gascity-spawn: %v\n", err)
		return 1
	}
	return 0
}

func runGascityStopCommand(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, gascitySpawnUsage)
		return 2
	}
	agentID := args[0]
	stateDir := defaultGascityStateDir(agentID)
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--state-dir":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "gascity-stop: --state-dir requires a value")
				return 2
			}
			stateDir = rest[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "gascity-stop: unknown arg: %s\n", rest[i])
			return 2
		}
	}

	if err := gascityStop(stateDir); err != nil {
		fmt.Fprintf(os.Stderr, "gascity-stop: %v\n", err)
		return 1
	}
	return 0
}

func runGascityPingCommand(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, gascitySpawnUsage)
		return 2
	}
	agentID := args[0]
	stateDir := defaultGascityStateDir(agentID)
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--state-dir":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "gascity-ping: --state-dir requires a value")
				return 2
			}
			stateDir = rest[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "gascity-ping: unknown arg: %s\n", rest[i])
			return 2
		}
	}

	if gascityAlive(stateDir) {
		return 0
	}
	return 1
}

func pidFilePath(stateDir string) string       { return filepath.Join(stateDir, "pid") }
func treehousePathFile(stateDir string) string { return filepath.Join(stateDir, "treehouse-path") }
func startedAtFilePath(stateDir string) string { return filepath.Join(stateDir, "started-at") }
func beadIDFilePath(stateDir string) string    { return filepath.Join(stateDir, "bead-id") }

// readPID returns the recorded PID, or 0 if none is recorded.
func readPID(stateDir string) int {
	data, err := os.ReadFile(pidFilePath(stateDir))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// pidAlive reports whether pid names a live process, via a signal-0 probe.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func gascityAlive(stateDir string) bool {
	pid := readPID(stateDir)
	if pid == 0 {
		return false
	}
	if pidAlive(pid) {
		return true
	}
	// Stale pid file — clean it up so a later spawn doesn't see a false "already running".
	_ = os.Remove(pidFilePath(stateDir))
	return false
}

// gascitySpawn starts command as a detached sh -c child in workdir, records
// its PID (and, when set, treehouse/bead sidecars) in stateDir, and returns
// once the child is running — it never waits on it.
func gascitySpawn(stateDir, agentID, command, workdir string, envOverrides []string, worktreePath, beadID string) error {
	if gascityAlive(stateDir) {
		return fmt.Errorf("session %q already running", agentID)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = workdir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	nullFile, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("opening %s: %w", os.DevNull, err)
	}
	defer nullFile.Close() //nolint:errcheck
	cmd.Stdout = nullFile
	cmd.Stderr = nullFile
	cmd.Stdin = nullFile

	env := os.Environ()
	if len(envOverrides) > 0 {
		sorted := append([]string(nil), envOverrides...)
		sort.Strings(sorted)
		env = append(env, sorted...)
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting session %q: %w", agentID, err)
	}

	if err := os.WriteFile(pidFilePath(stateDir), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("recording pid for %q: %w", agentID, err)
	}
	_ = os.WriteFile(startedAtFilePath(stateDir), []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o644)

	if worktreePath != "" {
		if err := os.WriteFile(treehousePathFile(stateDir), []byte(worktreePath+"\n"), 0o644); err != nil {
			// Non-fatal: the session is already running. Losing the sidecar
			// only means gascity-stop can't auto-return the treehouse lease.
			fmt.Fprintf(os.Stderr, "gascity-spawn: warning: could not record treehouse path: %v\n", err)
		}
	}

	// Same sidecar shape, same accepted hazard: the file is written AFTER the
	// child is running, so a spawn that dies between here and registration can
	// leave a stale bead-id behind — a later gascity-stop would then close a
	// bead this session never actually worked. That is the identical exposure
	// the treehouse sidecar above already carries, and the alternative
	// (writing it before Start) trades it for the worse one: a recorded bead
	// for a session that never existed at all.
	if beadID != "" {
		if err := os.WriteFile(beadIDFilePath(stateDir), []byte(beadID+"\n"), 0o644); err != nil {
			// Non-fatal for the same reason: the session is already running,
			// and losing this only means gascity-stop cannot auto-close the bead.
			fmt.Fprintf(os.Stderr, "gascity-spawn: warning: could not record bead id: %v\n", err)
		}
	}

	// Release the child from our own process group tracking — it is now
	// detached (own pgid) and fully independent of this CLI invocation.
	go func() { _, _ = cmd.Process.Wait() }() //nolint:errcheck

	return nil
}

// gascityStop closes any bound bead and returns any leased treehouse worktree
// (both best-effort, never blocking), then sends SIGTERM to the session's
// process group, escalating to SIGKILL after stopGrace if it hasn't exited.
// Idempotent: returns nil if no session is recorded or it is already dead.
func gascityStop(stateDir string) error {
	closeBoundBead(stateDir)
	returnTreehouseWorktree(stateDir)

	pid := readPID(stateDir)
	if pid == 0 {
		return nil
	}
	if !pidAlive(pid) {
		cleanupGascityState(stateDir)
		return nil
	}

	// Setpgid:true at spawn time made the child's pid its own pgid.
	_ = syscall.Kill(-pid, syscall.SIGTERM)

	deadline := time.Now().Add(stopGrace)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			cleanupGascityState(stateDir)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	if pidAlive(pid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	cleanupGascityState(stateDir)
	return nil
}

func cleanupGascityState(stateDir string) {
	_ = os.Remove(pidFilePath(stateDir))
	_ = os.Remove(treehousePathFile(stateDir))
	_ = os.Remove(startedAtFilePath(stateDir))
	_ = os.Remove(beadIDFilePath(stateDir))
}

// closeBoundBead reads the bead sidecar written by gascitySpawn and closes
// that work item through its own federation store wrapper (the id's leading
// token: task-oyaj → `task close task-oyaj`), falling back to a bare `bd`.
// Stopping the session IS the end of the work in beads-required mode, and the
// closed bead is what stops anything from relaunching the agent afterwards.
//
// Best-effort in every direction, exactly like returnTreehouseWorktree: a
// missing sidecar, no store CLI on PATH, or a failing close must never block
// the stop that follows it — a session left running is worse than a bead left
// open, and the operator can always close the bead by hand.
//
// The sidecar is NOT removed here: cleanupGascityState clears it on every path
// that actually ends the session, and removing it early would lose the record
// if the stop itself fails partway.
func closeBoundBead(stateDir string) {
	data, err := os.ReadFile(beadIDFilePath(stateDir))
	if err != nil {
		return
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return
	}
	store := ""
	if i := strings.IndexByte(id, '-'); i > 0 {
		store = id[:i]
	}
	bin := ""
	if store != "" {
		if abs, err := exec.LookPath(store); err == nil {
			bin = abs
		}
	}
	if bin == "" {
		abs, err := exec.LookPath("bd")
		if err != nil {
			return
		}
		bin = abs
	}
	if err := exec.Command(bin, "close", id).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "gascity-stop: warning: could not close bead %s: %v\n", id, err)
	}
}

// returnTreehouseWorktree reads the treehouse sidecar written by
// gascitySpawn and, if present and the treehouse binary is on PATH, returns
// the lease before the process is signalled — matching this repo's
// treehouse-return-before-teardown ordering (CLAUDE.md: "treehouse get
// RESETS the slot it hands out"). Best-effort in every direction: a
// missing sidecar, a missing treehouse binary, or a failing `treehouse
// return` must never block the stop that follows it.
func returnTreehouseWorktree(stateDir string) {
	data, err := os.ReadFile(treehousePathFile(stateDir))
	if err != nil {
		return
	}
	path := strings.TrimSpace(string(data))
	if path == "" {
		return
	}
	if _, err := exec.LookPath("treehouse"); err != nil {
		return
	}
	// treehouse resolves its pool from the process cwd (CLAUDE.md: robots-d04t)
	// — run it from inside the leased worktree itself so it always resolves
	// the correct repo's pool, regardless of this CLI's own invocation cwd.
	cmd := exec.Command("treehouse", "return", path)
	cmd.Dir = path
	_ = cmd.Run()
}
