// Per-agent singleton guard for the listen/monitor poll loop (robots-fgyz).
//
// Arming used to be purely additive: every restart, reconnect, or fresh turn
// that ran `parlay listen --agent X` started ANOTHER loop on X's channel and
// nothing ever ended the previous one. Observed on the captain's box: the
// Mayor agent held 12 live `parlay-cli listen --agent mayor` processes (every
// other agent had exactly one), so every captain->mayor message was delivered
// and processed up to 12 times, with 14 leaked long-poll shells and the Mayor
// session burning 20-27% CPU feeding the duplicates.
//
// The rule this file enforces: ONE live loop per agent channel. Arming is a
// takeover — before registering, any other live loop on this agent's channel
// is ended, loudly, on stderr. Takeover (not "reuse the existing one and
// exit") is deliberate: the process arming now is the one wired to the live
// harness Monitor task, and exiting immediately would leave that task dead
// with the agent registered-but-deaf, the exact failure shape robots-dcag
// warns about.
//
// Detection is a `ps` match, not a pidfile. A pidfile only knows about
// listeners armed after this code lands; the twelve that already existed had
// none, and a pidfile adds its own staleness failure mode on top. The process
// table is the authoritative answer to "is another loop on this channel alive
// right now?" — cheap, and it cannot go stale.
//
// Matching is deliberately strict, because the two error directions are not
// symmetric: killing a process that is NOT a duplicate ends a live agent's
// session, while failing to spot one only leaves the pre-existing duplicate
// running. Every ambiguous shape therefore resolves to "not a duplicate".
package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// listenSubcommands are the verbs that arm a poll loop on ONE agent's
// channel. All three are mutually exclusive on the same channel: two live
// loops mean every directive is delivered twice.
var listenSubcommands = map[string]bool{
	"listen":   true,
	"agent-up": true, // documented alias of `listen`
	"monitor":  true,
}

// parlayBinaryNames are the basenames a parlay CLI process can run under:
// the `bin/parlay` wrapper and the Go binary it execs
// (tools/cli/bin/parlay-cli). Requiring the subcommand to be preceded by one
// of these is what keeps a shell wrapper whose *command string* merely
// contains "parlay listen --agent X" from being mistaken for the listener
// itself. ("parlay-bin", the former standalone spawn binary, was folded into
// the CLI by task-42qot and no longer runs as its own process.)
var parlayBinaryNames = map[string]bool{
	"parlay":     true,
	"parlay-cli": true,
}

// freeTextFlagValues take an arbitrary human string whose contents can
// themselves look like flags — a ticket title routinely contains "--agent".
// `ps` flattens argv into one space-joined string with no quoting, so once
// scanning reaches one of these there is no longer any way to tell a real
// flag from text inside a value. Scanning stops there rather than guessing.
var freeTextFlagValues = map[string]bool{
	"--name": true,
	"--caps": true,
}

type procEntry struct {
	pid  int
	ppid int
	args string
}

// Injection points for tests — the real implementations touch the live
// process table and send real signals, which no unit test may do.
var (
	listProcesses = psSnapshot
	signalProcess = func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }
	nowSleep      = time.Sleep
)

// listensForAgent reports whether one `ps` args line is a parlay poll loop on
// exactly this agent's channel.
//
// Shape required: a parlay binary, then a listen-ish subcommand, then
// `--agent <agent>` (or `--agent=<agent>`) among the flags that follow it,
// with an exact token compare so `--agent mayor` never matches `mayor-2`.
func listensForAgent(args, agent string) bool {
	return agent != "" && listenerAgent(args) == agent
}

// listenerAgent returns the agent id one `ps` args line is polling for, or ""
// if the line is not a parlay poll loop at all. Same strictness as
// listensForAgent — which is now a thin equality on top of it — factored out
// so a caller with MANY agent ids to classify (`parlay launch`, robots-jkwc)
// parses each process line once instead of once per candidate id.
func listenerAgent(args string) string {
	tokens := strings.Fields(args)

	sub := -1
	for i := 1; i < len(tokens); i++ {
		if !listenSubcommands[tokens[i]] {
			continue
		}
		if parlayBinaryNames[baseName(tokens[i-1])] {
			sub = i
			break
		}
	}
	if sub < 0 {
		return ""
	}

	for i := sub + 1; i < len(tokens); i++ {
		tok := tokens[i]
		if freeTextFlagValues[tok] {
			return "" // past here, argv is unparseable from a flattened string
		}
		if value, found := strings.CutPrefix(tok, "--agent="); found {
			return value
		}
		if tok == "--agent" {
			if i+1 < len(tokens) {
				return tokens[i+1]
			}
			return ""
		}
	}
	return ""
}

// LiveListenerAgents returns the set of agent ids that have a real poll loop
// running on this host right now, and whether the process table could be read
// at all.
//
// This is the ground truth the agent REGISTRY cannot supply: a registration is
// just a row on the server, so an agent whose listener died — or was never
// armed — stays "registered" forever with nothing reading its channel
// (robots-jkwc: 148 registrations marked live against 11 real listeners). A
// caller that reports liveness must intersect the two.
//
// Local by construction: `ps` sees only this host, so a caller must treat
// `false` here as "no listener HERE", not "no listener anywhere". When ok is
// false, nothing is known and the caller must not downgrade anything.
func LiveListenerAgents() (agents map[string]bool, ok bool) {
	procs, err := listProcesses()
	if err != nil {
		return nil, false
	}
	agents = make(map[string]bool)
	for _, p := range procs {
		if id := listenerAgent(p.args); id != "" {
			agents[id] = true
		}
	}
	return agents, true
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// selectDuplicateListeners returns the pids of live poll loops on this
// agent's channel that this process must end before arming its own.
//
// self and every one of self's ancestors are protected: the harness arms the
// monitor through a shell whose command string is the whole `parlay listen …`
// invocation, so an ancestor can match the pattern, and killing it would kill
// the very process doing the reaping.
func selectDuplicateListeners(procs []procEntry, agent string, self int) []int {
	parent := make(map[int]int, len(procs))
	for _, p := range procs {
		parent[p.pid] = p.ppid
	}

	protected := map[int]bool{self: true}
	for pid, hops := self, 0; hops < 64; hops++ {
		ppid, ok := parent[pid]
		if !ok || ppid <= 1 || protected[ppid] {
			break
		}
		protected[ppid] = true
		pid = ppid
	}

	var dupes []int
	for _, p := range procs {
		if protected[p.pid] || p.pid <= 1 {
			continue
		}
		if listensForAgent(p.args, agent) {
			dupes = append(dupes, p.pid)
		}
	}
	return dupes
}

// psSnapshot reads the current user's process table. `-x` (no `-a`) scopes it
// to this uid, so another user's processes are never even candidates.
func psSnapshot() ([]procEntry, error) {
	out, err := exec.Command("ps", "-xo", "pid=,ppid=,args=").Output()
	if err != nil {
		return nil, err
	}
	var procs []procEntry
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		procs = append(procs, procEntry{pid: pid, ppid: ppid, args: strings.Join(fields[2:], " ")})
	}
	return procs, nil
}

// reapDuplicateListeners ends every other live poll loop on this agent's
// channel. Best-effort in every direction — a failed probe or an unkillable
// pid warns on stderr and arming continues, because a second listener is a
// duplicate-delivery bug while a refusal to arm at all is a deaf agent.
func reapDuplicateListeners(agent string) {
	if os.Getenv("PARLAY_LISTEN_NO_SINGLETON") != "" {
		fmt.Fprintf(os.Stderr, "parlay listen: PARLAY_LISTEN_NO_SINGLETON set — NOT reaping existing listeners for '%s' (duplicate delivery is possible)\n", agent)
		return
	}

	procs, err := listProcesses()
	if err != nil {
		fmt.Fprintf(os.Stderr, "parlay listen: could not read the process table (%v) — arming without the singleton check; run 'ps -xo pid=,args= | grep \"listen --agent %s\"' if messages arrive more than once\n", err, agent)
		return
	}

	dupes := selectDuplicateListeners(procs, agent, os.Getpid())
	if len(dupes) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "parlay listen: %d existing listener(s) for '%s' (pid %s) — ending them so this channel keeps exactly one\n",
		len(dupes), agent, joinPIDs(dupes))
	for _, pid := range dupes {
		if err := signalProcess(pid, syscall.SIGTERM); err != nil {
			fmt.Fprintf(os.Stderr, "parlay listen: SIGTERM pid %d failed: %v\n", pid, err)
		}
	}

	// Grace, then confirm. A loop blocked in a long poll can miss its window,
	// and a survivor is exactly the duplicate delivery this guard exists to
	// stop — so the second pass is not optional.
	nowSleep(2 * time.Second)
	for _, pid := range dupes {
		if signalProcess(pid, syscall.Signal(0)) != nil {
			continue // already gone
		}
		fmt.Fprintf(os.Stderr, "parlay listen: pid %d ignored SIGTERM — sending SIGKILL\n", pid)
		if err := signalProcess(pid, syscall.SIGKILL); err != nil {
			fmt.Fprintf(os.Stderr, "parlay listen: SIGKILL pid %d failed: %v — this channel may still deliver twice\n", pid, err)
		}
	}
}

// KillLocalListeners ends every live poll-loop process on THIS host for
// agent's channel — the local half of `parlay shutdown` (task-35ww): a
// listener still running here must stop, not just fall silent once the
// server/relay side is torn down. Reuses the same detection and kill
// sequence as the arming-time singleton guard (reapDuplicateListeners):
// SIGTERM, a 2s grace period, then SIGKILL any survivor. Returns the pids it
// found and terminated — nil if none were running here, or if the process
// table could not be read (never treated as an error: shutdown must still
// proceed to deregister server-side).
func KillLocalListeners(agent string) []int {
	procs, err := listProcesses()
	if err != nil {
		return nil
	}
	dupes := selectDuplicateListeners(procs, agent, os.Getpid())
	if len(dupes) == 0 {
		return nil
	}
	for _, pid := range dupes {
		_ = signalProcess(pid, syscall.SIGTERM)
	}
	nowSleep(2 * time.Second)
	for _, pid := range dupes {
		if signalProcess(pid, syscall.Signal(0)) != nil {
			continue // already gone
		}
		_ = signalProcess(pid, syscall.SIGKILL)
	}
	return dupes
}

func joinPIDs(pids []int) string {
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ", ")
}
