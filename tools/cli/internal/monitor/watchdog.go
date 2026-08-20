// Deregistration watchdog for the relay-backed monitor (robots-jkwc).
//
// robots-ycfa built a closed self-healing loop for a leaked listener:
//
//	prune -> the id is tombstoned -> its next poll gets 410 Gone -> the relay
//	drops that channel's poll loop -> the MONITOR sees itself absent from the
//	registry twice -> it kills its child and exits.
//
// The last link of that loop was implemented only in packages/cli/src/
// monitor.ts (startRegistryWatchdog / registryStrike). By then ticket B10 had
// already repointed bin/parlay at the Go binary for every verb except
// lavish-import, so the watchdog shipped onto the retired TS path and the live
// path — internal/monitor's runRelayMonitor, which just execs
// tools/monitor/parlay-monitor.sh and waits — never retired anything. A pruned
// channel's monitor kept tailing a spool nobody writes to, forever. This file
// is that missing link, ported to the path that actually runs.
//
// Every ambiguity resolves toward STAYING ALIVE, because a monitor that quits
// while its agent is real goes registered-but-deaf (robots-dcag): a failed
// request, a non-2xx, an unparseable body, or an EMPTY registry all reset the
// evidence. Only repeated, successful, well-formed answers that omit this
// agent count — the server has to say "you are gone" twice in a row.
package monitor

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// registryCheckInterval is how often the watchdog asks the registry whether
// this agent still exists. Matches monitor.ts's REGISTRY_CHECK_MS.
const registryCheckInterval = 60 * time.Second

// missingStrikesToRetire is the number of consecutive clean "you are not in
// the registry" answers required before a monitor retires itself. Two, so a
// single sweep landing between an agent's unregister and its re-register
// cannot evict a live monitor.
const missingStrikesToRetire = 2

// registryFetchTimeout bounds one watchdog probe. Deliberately generous:
// /api/chat/agents serializes the whole registry and grows with the fleet
// (>2s at 269 agents — see the robots-dcag note in parlay-monitor.sh), and a
// timeout here is indistinguishable from a dead server, which resets the
// evidence and delays a real retirement by a minute.
const registryFetchTimeout = 15 * time.Second

// registryStrike classifies one registry response and returns the strike
// count carried forward — 0 means "evidence reset". Pure, so the
// retire/stay decision is testable without a server, a clock, or a child
// process. Mirrors monitor.ts's registryStrike exactly.
//
// `ok` is false whenever the request failed, answered non-2xx, or could not
// be decoded as the expected array; `agents` is only meaningful when ok.
func registryStrike(agent string, agents []wire.AgentInfo, ok bool, priorStrikes int) int {
	// Anything short of a well-formed answer is not evidence of anything.
	if !ok {
		return 0
	}
	// An empty registry means the server just restarted and has not been
	// re-populated, not that this agent was evicted. Never a strike.
	if len(agents) == 0 {
		return 0
	}
	for _, a := range agents {
		if a.ID == agent {
			return 0
		}
	}
	return priorStrikes + 1
}

// fetchRegistry is the watchdog's one network call, injectable for tests.
var fetchRegistry = func() ([]wire.AgentInfo, bool) {
	return httpc.TryGetJSON[[]wire.AgentInfo]("/api/chat/agents", registryFetchTimeout)
}

// startRegistryWatchdog polls the registry in the background and calls
// `retire` once the server has cleanly reported this agent missing
// missingStrikesToRetire times running. Returns a function that stops it.
//
// Set PARLAY_NO_REGISTRY_WATCHDOG=1 to disable — for a monitor deliberately
// armed before its channel is registered, and as the escape hatch if this
// ever misjudges a live agent.
func startRegistryWatchdog(agent string, retire func(), interval time.Duration) (stop func()) {
	if os.Getenv("PARLAY_NO_REGISTRY_WATCHDOG") == "1" {
		return func() {}
	}
	done := make(chan struct{})
	var once bool
	stop = func() {
		if once {
			return
		}
		once = true
		close(done)
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		strikes := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			agents, ok := fetchRegistry()
			strikes = registryStrike(agent, agents, ok, strikes)
			if strikes < missingStrikesToRetire {
				continue
			}
			fmt.Fprintf(os.Stderr,
				"parlay monitor: '%s' is no longer in the server's registry — it was unregistered.\n"+
					"parlay monitor:   Retiring this monitor rather than streaming a channel nobody owns.\n"+
					"parlay monitor:   Re-arm with 'parlay listen --agent %s' if this was wrong.\n",
				agent, agent)
			retire()
			return
		}
	}()

	return stop
}

// descendantsOf returns pid plus every process below it in the table,
// deepest-last. The bound on iterations is the table size, so a corrupt
// ppid cycle cannot spin forever.
func descendantsOf(procs []procEntry, pid int) []int {
	children := make(map[int][]int, len(procs))
	for _, p := range procs {
		if p.pid != p.ppid {
			children[p.ppid] = append(children[p.ppid], p.pid)
		}
	}
	seen := map[int]bool{pid: true}
	out := []int{pid}
	for i := 0; i < len(out) && i < len(procs)+1; i++ {
		for _, c := range children[out[i]] {
			if seen[c] || c <= 1 {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// terminateProcessTree SIGTERMs the monitor's child and everything under it.
//
// Killing the child pid alone is not enough in --notify-safe mode: the script
// runs `tail -F | awk` as a real pipeline under bash (it cannot `exec` a
// pipeline), so SIGTERM to bash leaves an orphaned `tail -F` holding the spool
// — the exact leaked-tail shape robots-ycfa's self-heal loop exists to end.
// The default path execs tail in place, so the tree is just the one pid and
// this costs a single ps.
//
// Best-effort in every direction: a failed ps or an unkillable pid still kills
// what it can and says so, because failing to retire is the lesser evil
// compared with never returning from here.
func terminateProcessTree(pid int) {
	if pid <= 1 {
		return
	}
	targets := []int{pid}
	if procs, err := listProcesses(); err == nil {
		targets = descendantsOf(procs, pid)
	} else {
		fmt.Fprintf(os.Stderr, "parlay monitor: could not read the process table (%v) — signalling pid %d only; a stray 'tail -F' may survive\n", err, pid)
	}
	// Children first: killing bash before its pipeline is what orphans the tail.
	for i := len(targets) - 1; i >= 0; i-- {
		if err := signalProcess(targets[i], syscall.SIGTERM); err != nil {
			fmt.Fprintf(os.Stderr, "parlay monitor: SIGTERM pid %d failed: %v\n", targets[i], err)
		}
	}
}
