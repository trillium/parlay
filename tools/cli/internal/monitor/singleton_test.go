// Unit tests for the robots-fgyz per-agent singleton guard. Every test runs
// against a synthetic process table and a recording signal function — the
// real ones read the live host and send real signals.
package monitor

import (
	"syscall"
	"testing"
	"time"
)

// realListenerArgs is the exact `ps -xo args=` shape a live listener has,
// copied from the robots-fgyz repro with the home path genericized. What
// matters is the shape: an absolute repo-checkout path ending in the
// parlay-cli basename, followed by `listen --agent <id>`.
const realListenerArgs = "/Users/dev/code/parlay/tools/cli/bin/parlay-cli listen --agent mayor"

func TestListensForAgentMatchesARealListener(t *testing.T) {
	if !listensForAgent(realListenerArgs, "mayor") {
		t.Errorf("a real `parlay-cli listen --agent mayor` must be recognised: %q", realListenerArgs)
	}
}

func TestListensForAgentRejectsADifferentAgent(t *testing.T) {
	// Prefix collisions are the dangerous case: killing mayor's listener
	// because "mayor-2" armed would end a live session.
	cases := []struct {
		args  string
		agent string
	}{
		{"/usr/local/bin/parlay-cli listen --agent mayor-2", "mayor"},
		{"/usr/local/bin/parlay-cli listen --agent mayo", "mayor"},
		{"/usr/local/bin/parlay-cli listen --agent may", "mayor"},
		{"/usr/local/bin/parlay-cli listen --agent mayorly", "mayor"},
	}
	for _, tc := range cases {
		if listensForAgent(tc.args, tc.agent) {
			t.Errorf("%q must NOT match agent %q", tc.args, tc.agent)
		}
	}
}

func TestListensForAgentAcceptsEqualsFormAndOtherLoopVerbs(t *testing.T) {
	cases := []string{
		"/usr/local/bin/parlay-cli listen --agent=mayor",
		"/usr/local/bin/parlay-cli agent-up --agent mayor",
		"/usr/local/bin/parlay-cli monitor --agent mayor",
		"/usr/local/bin/parlay-cli monitor --legacy-poll --agent mayor",
		"parlay listen --agent mayor --notify-safe",
	}
	for _, args := range cases {
		if !listensForAgent(args, "mayor") {
			t.Errorf("%q must match agent mayor", args)
		}
	}
}

func TestListensForAgentIgnoresNonParlayProcesses(t *testing.T) {
	// A shell wrapper's command STRING contains the whole invocation. It is
	// not the listener, and on the observed host its `--agent` value carries
	// a trailing quote from the eval — either way it must not be a candidate.
	cases := []string{
		"/bin/zsh -c eval 'PARLAY_SERVER=http://localhost:31337 parlay listen --agent mayor'",
		"grep listen --agent mayor",
		"tail -F /tmp/parlay/mayor.chan",
		"listen --agent mayor",
	}
	for _, args := range cases {
		if listensForAgent(args, "mayor") {
			t.Errorf("%q must NOT be treated as a listener process", args)
		}
	}
}

func TestListensForAgentStopsAtFreeTextFlagValues(t *testing.T) {
	// A ticket title routinely contains "--agent <something>", and `ps`
	// flattens argv with no quoting, so past --name/--caps nothing can be
	// told apart from a real flag. The safe direction is "not a duplicate".
	args := `/usr/local/bin/parlay-cli listen --name robots-fgyz: --agent mayor accumulated 12 loops`
	if listensForAgent(args, "mayor") {
		t.Errorf("a --agent occurrence inside a --name value must not match: %q", args)
	}
	// The real flag before the free text still matches.
	real := `/usr/local/bin/parlay-cli listen --agent mayor --name --agent mayor-2 is a title`
	if !listensForAgent(real, "mayor") {
		t.Errorf("the real --agent flag before --name must still match: %q", real)
	}
	if listensForAgent(real, "mayor-2") {
		t.Errorf("--agent inside the --name value must not match mayor-2: %q", real)
	}
}

func TestListensForAgentRejectsEmptyAgent(t *testing.T) {
	if listensForAgent("/usr/local/bin/parlay-cli listen --agent ", "") {
		t.Error("an empty agent id must never match anything")
	}
}

func TestSelectDuplicateListenersFindsTheAccumulatedLoops(t *testing.T) {
	// The robots-fgyz repro, shrunk: one live listener in this process's own
	// subtree plus three leaked ones reparented to init.
	self := 500
	procs := []procEntry{
		{pid: 1, ppid: 0, args: "/sbin/launchd"},
		{pid: 100, ppid: 1, args: "/bin/zsh -c eval 'parlay listen --agent mayor'"},
		{pid: self, ppid: 100, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
		{pid: 602, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
		{pid: 603, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
		{pid: 700, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent brain-dev"},
	}

	got := selectDuplicateListeners(procs, "mayor", self)

	want := map[int]bool{601: true, 602: true, 603: true}
	if len(got) != len(want) {
		t.Fatalf("duplicates = %v, want exactly %v", got, []int{601, 602, 603})
	}
	for _, pid := range got {
		if !want[pid] {
			t.Errorf("pid %d must not be reaped (duplicates = %v)", pid, got)
		}
	}
}

func TestSelectDuplicateListenersNeverReapsSelfOrAnAncestor(t *testing.T) {
	// The harness arms the monitor through a shell whose command string is
	// the whole invocation; reaping an ancestor kills the reaper.
	self := 500
	procs := []procEntry{
		{pid: 1, ppid: 0, args: "/sbin/launchd"},
		{pid: 90, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},   // grandparent
		{pid: 100, ppid: 90, args: "/usr/local/bin/parlay-cli listen --agent mayor"}, // parent
		{pid: self, ppid: 100, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}

	if got := selectDuplicateListeners(procs, "mayor", self); len(got) != 0 {
		t.Errorf("self and ancestors must never be reaped, got %v", got)
	}
}

func TestSelectDuplicateListenersSurvivesAPPIDCycle(t *testing.T) {
	// A corrupt/racy ps snapshot must not hang the ancestry walk.
	procs := []procEntry{
		{pid: 10, ppid: 11, args: "a"},
		{pid: 11, ppid: 10, args: "b"},
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}
	done := make(chan []int, 1)
	go func() { done <- selectDuplicateListeners(procs, "mayor", 10) }()
	select {
	case got := <-done:
		if len(got) != 1 || got[0] != 601 {
			t.Errorf("duplicates = %v, want [601]", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("selectDuplicateListeners did not terminate on a ppid cycle")
	}
}

// signalRecorder swaps the process-signalling and sleep hooks for recording
// fakes. alive is the set of pids that report as still running.
type signalRecorder struct {
	sent []struct {
		pid int
		sig syscall.Signal
	}
	alive map[int]bool
}

func recordSignals(t *testing.T, alive map[int]bool) *signalRecorder {
	t.Helper()
	rec := &signalRecorder{alive: alive}
	origSignal, origSleep := signalProcess, nowSleep
	signalProcess = func(pid int, sig syscall.Signal) error {
		rec.sent = append(rec.sent, struct {
			pid int
			sig syscall.Signal
		}{pid, sig})
		if sig == syscall.Signal(0) && !rec.alive[pid] {
			return syscall.ESRCH
		}
		return nil
	}
	nowSleep = func(time.Duration) {}
	t.Cleanup(func() { signalProcess, nowSleep = origSignal, origSleep })
	return rec
}

func (r *signalRecorder) sigsTo(pid int) []syscall.Signal {
	var out []syscall.Signal
	for _, s := range r.sent {
		if s.pid == pid {
			out = append(out, s.sig)
		}
	}
	return out
}

func stubProcessTable(t *testing.T, procs []procEntry, err error) {
	t.Helper()
	orig := listProcesses
	listProcesses = func() ([]procEntry, error) { return procs, err }
	t.Cleanup(func() { listProcesses = orig })
}

func TestReapDuplicateListenersTerminatesEveryDuplicate(t *testing.T) {
	stubProcessTable(t, []procEntry{
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
		{pid: 602, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}, nil)
	rec := recordSignals(t, map[int]bool{}) // both exit on SIGTERM

	reapDuplicateListeners("mayor")

	for _, pid := range []int{601, 602} {
		sigs := rec.sigsTo(pid)
		if len(sigs) == 0 || sigs[0] != syscall.SIGTERM {
			t.Errorf("pid %d signals = %v, want SIGTERM first", pid, sigs)
		}
		for _, s := range sigs {
			if s == syscall.SIGKILL {
				t.Errorf("pid %d was SIGKILLed even though it exited on SIGTERM", pid)
			}
		}
	}
}

func TestReapDuplicateListenersEscalatesToKillForASurvivor(t *testing.T) {
	// A loop blocked in a long poll can miss its SIGTERM window, and a
	// survivor is exactly the duplicate delivery this guard exists to stop.
	stubProcessTable(t, []procEntry{
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}, nil)
	rec := recordSignals(t, map[int]bool{601: true})

	reapDuplicateListeners("mayor")

	sigs := rec.sigsTo(601)
	if len(sigs) < 3 || sigs[0] != syscall.SIGTERM || sigs[1] != syscall.Signal(0) || sigs[2] != syscall.SIGKILL {
		t.Errorf("signals to 601 = %v, want SIGTERM, probe, SIGKILL", sigs)
	}
}

func TestReapDuplicateListenersSignalsNothingWhenChannelIsClean(t *testing.T) {
	stubProcessTable(t, []procEntry{
		{pid: 700, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent brain-dev"},
	}, nil)
	rec := recordSignals(t, map[int]bool{})

	reapDuplicateListeners("mayor")

	if len(rec.sent) != 0 {
		t.Errorf("no signals expected on a clean channel, got %v", rec.sent)
	}
}

func TestReapDuplicateListenersTolerObservesAFailedProcessProbe(t *testing.T) {
	// robots-dcag's rule: an optional probe must never be able to stop
	// arming. A failed `ps` warns and continues; it does not signal blindly.
	stubProcessTable(t, nil, syscall.ENOENT)
	rec := recordSignals(t, map[int]bool{})

	reapDuplicateListeners("mayor")

	if len(rec.sent) != 0 {
		t.Errorf("a failed process probe must signal nothing, got %v", rec.sent)
	}
}

func TestReapDuplicateListenersRespectsTheOptOut(t *testing.T) {
	t.Setenv("PARLAY_LISTEN_NO_SINGLETON", "1")
	stubProcessTable(t, []procEntry{
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}, nil)
	rec := recordSignals(t, map[int]bool{601: true})

	reapDuplicateListeners("mayor")

	if len(rec.sent) != 0 {
		t.Errorf("PARLAY_LISTEN_NO_SINGLETON must suppress every signal, got %v", rec.sent)
	}
}
