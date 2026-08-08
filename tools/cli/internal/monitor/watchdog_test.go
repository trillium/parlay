// Unit tests for the robots-jkwc deregistration watchdog. The registry probe
// and the signal function are both injected, so no test reaches the network,
// the live process table, or a real pid.
package monitor

import (
	"errors"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/wire"
)

func agentList(ids ...string) []wire.AgentInfo {
	out := make([]wire.AgentInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, wire.AgentInfo{ID: id})
	}
	return out
}

func TestRegistryStrikeOnlyCountsACleanAbsence(t *testing.T) {
	cases := []struct {
		name   string
		agents []wire.AgentInfo
		ok     bool
		prior  int
		want   int
	}{
		// Every ambiguity resolves toward staying alive (robots-dcag): the
		// evidence resets rather than accumulating.
		{"unreachable server resets", nil, false, 1, 0},
		{"non-2xx or unparseable body resets", nil, false, 1, 0},
		{"empty registry is a restart, not an eviction", agentList(), true, 1, 0},
		{"present resets", agentList("other", "mc-robots-jkwc"), true, 1, 0},

		// The one shape that counts: a well-formed, non-empty answer that
		// omits this agent.
		{"absent from a real registry strikes", agentList("other"), true, 0, 1},
		{"absent twice reaches the retire threshold", agentList("other"), true, 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := registryStrike("mc-robots-jkwc", tc.agents, tc.ok, tc.prior); got != tc.want {
				t.Errorf("registryStrike = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRegistryStrikeNeedsTwoInARowToRetire(t *testing.T) {
	// A single sweep landing between an agent's unregister and its
	// re-register must not evict a live monitor.
	strikes := registryStrike("mc-robots-jkwc", agentList("other"), true, 0)
	if strikes >= missingStrikesToRetire {
		t.Fatalf("one absence must not retire: strikes = %d", strikes)
	}
	strikes = registryStrike("mc-robots-jkwc", agentList("other", "mc-robots-jkwc"), true, strikes)
	if strikes != 0 {
		t.Fatalf("a reappearance must reset the evidence: strikes = %d", strikes)
	}
}

// withFakeRegistry swaps the watchdog's one network call for a scripted
// sequence of answers; the last entry repeats once exhausted.
func withFakeRegistry(t *testing.T, answers []func() ([]wire.AgentInfo, bool)) {
	t.Helper()
	orig := fetchRegistry
	var mu sync.Mutex
	i := 0
	fetchRegistry = func() ([]wire.AgentInfo, bool) {
		mu.Lock()
		defer mu.Unlock()
		a := answers[i]
		if i < len(answers)-1 {
			i++
		}
		return a()
	}
	t.Cleanup(func() { fetchRegistry = orig })
}

func TestWatchdogRetiresAfterTwoCleanAbsences(t *testing.T) {
	withFakeRegistry(t, []func() ([]wire.AgentInfo, bool){
		func() ([]wire.AgentInfo, bool) { return agentList("other"), true },
	})
	retired := make(chan struct{})
	stop := startRegistryWatchdog("mc-robots-jkwc", func() { close(retired) }, time.Millisecond)
	defer stop()

	select {
	case <-retired:
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog never retired a monitor the registry twice said was gone")
	}
}

func TestWatchdogStaysAliveWhileTheAgentIsRegistered(t *testing.T) {
	withFakeRegistry(t, []func() ([]wire.AgentInfo, bool){
		func() ([]wire.AgentInfo, bool) { return agentList("other", "mc-robots-jkwc"), true },
	})
	var retired bool
	var mu sync.Mutex
	stop := startRegistryWatchdog("mc-robots-jkwc", func() { mu.Lock(); retired = true; mu.Unlock() }, time.Millisecond)
	defer stop()

	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if retired {
		t.Fatal("a registered agent's monitor must never be retired")
	}
}

func TestWatchdogNeverRetiresOnAnUnreachableServer(t *testing.T) {
	// robots-dcag: a monitor that quits while its agent is real goes
	// registered-but-deaf, which is worse than the leak this guard prevents.
	withFakeRegistry(t, []func() ([]wire.AgentInfo, bool){
		func() ([]wire.AgentInfo, bool) { return nil, false },
	})
	var retired bool
	var mu sync.Mutex
	stop := startRegistryWatchdog("mc-robots-jkwc", func() { mu.Lock(); retired = true; mu.Unlock() }, time.Millisecond)
	defer stop()

	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if retired {
		t.Fatal("an unreachable registry must never be read as an eviction")
	}
}

func TestWatchdogOptOutIsANoOp(t *testing.T) {
	t.Setenv("PARLAY_NO_REGISTRY_WATCHDOG", "1")
	withFakeRegistry(t, []func() ([]wire.AgentInfo, bool){
		func() ([]wire.AgentInfo, bool) { return agentList("other"), true },
	})
	var retired bool
	var mu sync.Mutex
	stop := startRegistryWatchdog("mc-robots-jkwc", func() { mu.Lock(); retired = true; mu.Unlock() }, time.Millisecond)
	defer stop()

	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if retired {
		t.Fatal("PARLAY_NO_REGISTRY_WATCHDOG=1 must disable the watchdog entirely")
	}
}

func TestWatchdogStopEndsThePolling(t *testing.T) {
	var mu sync.Mutex
	probes := 0
	withFakeRegistry(t, []func() ([]wire.AgentInfo, bool){
		func() ([]wire.AgentInfo, bool) {
			mu.Lock()
			probes++
			mu.Unlock()
			return agentList("other", "mc-robots-jkwc"), true
		},
	})
	stop := startRegistryWatchdog("mc-robots-jkwc", func() {}, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	stop()
	stop() // idempotent — a double stop must not panic on a closed channel
	mu.Lock()
	after := probes
	mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if probes > after+1 {
		t.Errorf("stop() left the watchdog polling: %d probes before, %d after", after, probes)
	}
}

func TestDescendantsOfCollectsTheWholePipeline(t *testing.T) {
	// The --notify-safe shape: bash (100) runs `tail | awk` as real children.
	procs := []procEntry{
		{pid: 1, ppid: 0},
		{pid: 99, ppid: 1},  // unrelated
		{pid: 100, ppid: 1}, // the monitor's bash child
		{pid: 101, ppid: 100},
		{pid: 102, ppid: 100},
		{pid: 103, ppid: 101},
	}
	got := descendantsOf(procs, 100)
	want := map[int]bool{100: true, 101: true, 102: true, 103: true}
	if len(got) != len(want) {
		t.Fatalf("descendantsOf = %v, want exactly %v", got, want)
	}
	for _, pid := range got {
		if !want[pid] {
			t.Errorf("descendantsOf included unrelated pid %d", pid)
		}
	}
}

func TestDescendantsOfSurvivesAPpidCycle(t *testing.T) {
	procs := []procEntry{
		{pid: 100, ppid: 101},
		{pid: 101, ppid: 100},
	}
	done := make(chan []int, 1)
	go func() { done <- descendantsOf(procs, 100) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("descendantsOf spun forever on a ppid cycle")
	}
}

func TestTerminateProcessTreeSignalsChildrenBeforeTheParent(t *testing.T) {
	// Killing bash before its pipeline is exactly what orphans a `tail -F`.
	origList, origSignal := listProcesses, signalProcess
	t.Cleanup(func() { listProcesses, signalProcess = origList, origSignal })

	listProcesses = func() ([]procEntry, error) {
		return []procEntry{
			{pid: 100, ppid: 1},
			{pid: 101, ppid: 100},
			{pid: 102, ppid: 101},
		}, nil
	}
	var order []int
	signalProcess = func(pid int, sig syscall.Signal) error {
		if sig != syscall.SIGTERM {
			t.Errorf("pid %d got signal %v, want SIGTERM", pid, sig)
		}
		order = append(order, pid)
		return nil
	}

	terminateProcessTree(100)

	if len(order) != 3 {
		t.Fatalf("signalled %v, want all three of 100/101/102", order)
	}
	if order[len(order)-1] != 100 {
		t.Errorf("parent 100 must be signalled last, got order %v", order)
	}
}

func TestTerminateProcessTreeStillSignalsWhenPsFails(t *testing.T) {
	origList, origSignal := listProcesses, signalProcess
	t.Cleanup(func() { listProcesses, signalProcess = origList, origSignal })

	listProcesses = func() ([]procEntry, error) { return nil, errPsUnavailable }
	var signalled []int
	signalProcess = func(pid int, _ syscall.Signal) error {
		signalled = append(signalled, pid)
		return nil
	}

	terminateProcessTree(100)

	if len(signalled) != 1 || signalled[0] != 100 {
		t.Errorf("a failed ps must still end the child itself, got %v", signalled)
	}
}

func TestTerminateProcessTreeRefusesInitAndBelow(t *testing.T) {
	origSignal := signalProcess
	t.Cleanup(func() { signalProcess = origSignal })
	signalProcess = func(pid int, _ syscall.Signal) error {
		t.Fatalf("pid %d must never be signalled", pid)
		return nil
	}
	terminateProcessTree(0)
	terminateProcessTree(1)
	terminateProcessTree(-1)
}

// errPsUnavailable stands in for a failed `ps` in the tests above.
var errPsUnavailable = errors.New("ps unavailable")
