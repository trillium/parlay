// Unit tests for the robots-fgyz per-agent singleton guard. Every test runs
// against a synthetic process table and a recording signal function — the
// real ones read the live host and send real signals.
package monitor

import (
	"errors"
	"io"
	"os"
	"strings"
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
		"/bin/zsh -c eval 'parlay listen --agent mayor'",
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

// Verified 2026-10-05 by reproduction: `parlay listen` reaped the shell that
// launched it. The guard used to accept a line when the subcommand token was
// merely PRECEDED by a parlay binary name, which every wrapper satisfies —
// and selectDuplicateListeners' ancestor walk cannot save it, because once an
// intermediate ancestor has exited (the `( ... & )` subshell below) the
// process is reparented and its real ancestors are gone from the ppid map.
//
// The rule that excludes them is argv[0]: only a process that IS a parlay
// binary can be a listener.
func TestListensForAgentNeverMatchesAShellWrapperContainingTheInvocation(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{
			// The exact shape that reproduced the kill: the outer shell's argv
			// is this whole string, and the token before "listen" is the real
			// binary path, so the old preceded-by rule matched it.
			"bash -c with the invocation inline",
			"/bin/bash -c 'cd /tmp && export X=1 && ( /path/to/parlay-cli listen --agent demo --legacy-poll > /tmp/l.log 2>&1 & )'",
		},
		{
			"sh -c wrapping the invocation",
			"/bin/sh -c /path/to/bin/parlay listen --agent mayor",
		},
		{
			"a shell running a script that arms it",
			"/bin/bash /Users/dev/scripts/arm-the-mayor.sh listen --agent mayor",
		},
		{
			"herdr / tmux, which hand a full command string to the pane shell",
			"/opt/homebrew/bin/tmux send-keys -t 3 'parlay-cli listen --agent mayor' Enter",
		},
		{
			"an unrelated binary invoked with those words as arguments",
			"/usr/bin/env parlay-cli listen --agent mayor",
		},
	}
	for _, tc := range cases {
		if got := listenerAgent(tc.args); got != "" {
			t.Errorf("%s: listenerAgent(%q) = %q, want \"\" — a wrapper is not a listener and\nkilling it takes the caller's own shell down with it", tc.name, tc.args, got)
		}
		if listensForAgent(tc.args, "mayor") || listensForAgent(tc.args, "demo") {
			t.Errorf("%s: %q must not be reaped as a duplicate listener", tc.name, tc.args)
		}
	}
}

// The end-to-end consequence at the process boundary: a wrapper sitting in the
// table alongside one real duplicate must produce a signal for the duplicate
// ONLY. Before the argv[0] rule this test fails with the wrapper signalled
// alongside it.
func TestReapDuplicateListenersNeverSignalsTheShellThatLaunchedIt(t *testing.T) {
	launcher := 700 // the `bash -c '... parlay-cli listen --agent mayor ...'` that ran us
	stubProcessTable(t, []procEntry{
		{pid: 1, ppid: 0, args: "/sbin/launchd"},
		{pid: launcher, ppid: 1, args: "/bin/bash -c ( /usr/local/bin/parlay-cli listen --agent mayor )"},
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}, nil)
	rec := recordSignals(t, map[int]bool{601: true})

	reapDuplicateListeners("mayor")

	if len(rec.sigsTo(launcher)) != 0 {
		t.Errorf("the launching shell (pid %d) was signalled; a `parlay listen` must never\ntake down the shell that started it", launcher)
	}
	if len(rec.sigsTo(601)) == 0 {
		t.Error("the real duplicate listener (pid 601) was not reaped")
	}
}

// argv[0] is the whole rule; a later relaxation that lets a wrapper through
// is exactly the regression above. Asserted against a live process table so
// the guard cannot be quietly widened back.
func TestListenerDetectionIsAnchoredOnArgvZero(t *testing.T) {
	if listenerAgent("parlay-cli listen --agent mayor") != "mayor" {
		t.Error("a bare parlay binary name as argv[0] must still be recognised")
	}
	for _, args := range []string{
		"",
		"parlay --agent mayor",
		"listen --agent mayor",
		"  ",
	} {
		if got := listenerAgent(args); got != "" {
			t.Errorf("listenerAgent(%q) = %q, want \"\"", args, got)
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
	// The grace is now a poll, not a fixed sleep: SIGTERM, then signal-0
	// probes until the budget runs out, then exactly one SIGKILL (plus the
	// survivor-report probe after it).
	stubProcessTable(t, []procEntry{
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}, nil)
	rec := recordSignals(t, map[int]bool{601: true})

	reapDuplicateListeners("mayor")

	sigs := rec.sigsTo(601)
	if len(sigs) < 3 || sigs[0] != syscall.SIGTERM {
		t.Fatalf("signals to 601 = %v, want SIGTERM first, then probes, then SIGKILL", sigs)
	}
	kills := 0
	for i, s := range sigs[1:] {
		if s == syscall.SIGKILL {
			kills++
			// Everything between SIGTERM and SIGKILL must be a probe.
			for _, p := range sigs[1 : i+1] {
				if p != syscall.Signal(0) {
					t.Errorf("signals to 601 = %v, want only signal-0 probes between SIGTERM and SIGKILL", sigs)
					break
				}
			}
		}
	}
	if kills != 1 {
		t.Errorf("signals to 601 = %v, want exactly one SIGKILL for a survivor", sigs)
	}
}

func TestReapDuplicateListenersReturnsEarlyWhenTheVictimExits(t *testing.T) {
	// The fixed-2s-grace regression: a victim that dies on SIGTERM must not
	// cost the whole grace. The victim here exits after 2 probes; the reap
	// must then stop probing (no SIGKILL, few signal-0s, little slept time).
	stubProcessTable(t, []procEntry{
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}, nil)
	origSignal, origSleep := signalProcess, nowSleep
	probes := 0
	var slept time.Duration
	alive := true
	signalProcess = func(pid int, sig syscall.Signal) error {
		if sig == syscall.Signal(0) {
			probes++
			if probes >= 2 {
				alive = false // victim reaped after 2 probes
			}
			if !alive {
				return syscall.ESRCH
			}
			return nil
		}
		return nil
	}
	nowSleep = func(d time.Duration) { slept += d }
	t.Cleanup(func() { signalProcess, nowSleep = origSignal, origSleep })

	reapDuplicateListeners("mayor")

	if probes > 3 {
		t.Errorf("probes = %d, want the grace poll to stop soon after the victim exits", probes)
	}
	if slept >= singletonGraceTotal {
		t.Errorf("slept %v, want well under the %v grace budget for a fast exit", slept, singletonGraceTotal)
	}
}

func TestTerminateListenersNeverExceedsTheGraceBudget(t *testing.T) {
	// Worst case is unchanged: a SIGTERM-ignoring survivor costs the full
	// 2s budget, never more.
	stubProcessTable(t, []procEntry{
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}, nil)
	origSignal, origSleep := signalProcess, nowSleep
	var slept time.Duration
	signalProcess = func(pid int, sig syscall.Signal) error {
		if sig == syscall.Signal(0) {
			return nil // always alive
		}
		return nil
	}
	nowSleep = func(d time.Duration) { slept += d }
	t.Cleanup(func() { signalProcess, nowSleep = origSignal, origSleep })

	if got := terminateListeners([]int{601}); len(got) != 1 || got[0] != 601 {
		t.Fatalf("terminateListeners survivors = %v, want [601]", got)
	}
	if slept != singletonGraceTotal {
		t.Errorf("slept %v, want exactly the %v grace budget for a survivor", slept, singletonGraceTotal)
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

// ── KillLocalListeners: the local half of `parlay shutdown` (task-35ww) ──────

func TestKillLocalListenersTerminatesEveryMatch(t *testing.T) {
	stubProcessTable(t, []procEntry{
		{pid: 801, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
		{pid: 802, ppid: 1, args: "/usr/local/bin/parlay-cli monitor --agent mayor"},
		{pid: 803, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent brain-dev"},
	}, nil)
	rec := recordSignals(t, map[int]bool{}) // all exit on SIGTERM

	killed := KillLocalListeners("mayor")

	want := map[int]bool{801: true, 802: true}
	if len(killed) != len(want) {
		t.Fatalf("killed = %v, want pids %v", killed, want)
	}
	for _, pid := range killed {
		if !want[pid] {
			t.Errorf("killed unexpected pid %d", pid)
		}
		sigs := rec.sigsTo(pid)
		if len(sigs) == 0 || sigs[0] != syscall.SIGTERM {
			t.Errorf("pid %d signals = %v, want SIGTERM first", pid, sigs)
		}
	}
	if sigs := rec.sigsTo(803); len(sigs) != 0 {
		t.Errorf("brain-dev's listener (pid 803) must not be signalled, got %v", sigs)
	}
}

func TestKillLocalListenersEscalatesToKillForASurvivor(t *testing.T) {
	stubProcessTable(t, []procEntry{
		{pid: 801, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}, nil)
	rec := recordSignals(t, map[int]bool{801: true})

	killed := KillLocalListeners("mayor")

	if len(killed) != 1 || killed[0] != 801 {
		t.Fatalf("killed = %v, want [801]", killed)
	}
	sigs := rec.sigsTo(801)
	if len(sigs) < 3 || sigs[0] != syscall.SIGTERM {
		t.Fatalf("signals to 801 = %v, want SIGTERM first, then probes, then SIGKILL", sigs)
	}
	kills := 0
	for _, s := range sigs[1:] {
		if s == syscall.SIGKILL {
			kills++
		} else if s != syscall.Signal(0) {
			t.Errorf("signals to 801 = %v, want only signal-0 probes between SIGTERM and SIGKILL", sigs)
			break
		}
	}
	if kills != 1 {
		t.Errorf("signals to 801 = %v, want exactly one SIGKILL for a survivor", sigs)
	}
}

func TestKillLocalListenersReturnsNilWhenNothingIsRunningHere(t *testing.T) {
	stubProcessTable(t, []procEntry{
		{pid: 700, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent brain-dev"},
	}, nil)
	rec := recordSignals(t, map[int]bool{})

	if killed := KillLocalListeners("mayor"); killed != nil {
		t.Errorf("KillLocalListeners on a clean channel = %v, want nil", killed)
	}
	if len(rec.sent) != 0 {
		t.Errorf("no signals expected on a clean channel, got %v", rec.sent)
	}
}

func TestKillLocalListenersToleratesAFailedProcessProbe(t *testing.T) {
	stubProcessTable(t, nil, syscall.ENOENT)
	rec := recordSignals(t, map[int]bool{})

	if killed := KillLocalListeners("mayor"); killed != nil {
		t.Errorf("KillLocalListeners with an unreadable process table = %v, want nil", killed)
	}
	if len(rec.sent) != 0 {
		t.Errorf("a failed process probe must signal nothing, got %v", rec.sent)
	}
}

// ── LiveListenerAgents: ground truth for "who is actually listening" ─────────
//
// robots-jkwc: the registry reported 148 mc-robots agents [live] while only 11
// had a real poll loop. These pin the process-table half of that answer.

func TestLiveListenerAgentsCollectsEveryRealListener(t *testing.T) {
	stubProcessTable(t, []procEntry{
		{pid: 100, ppid: 1, args: "parlay-cli listen --agent mayor"},
		{pid: 101, ppid: 1, args: "/usr/local/bin/parlay monitor --agent=mc-robots-jkwc --notify-safe"},
		{pid: 102, ppid: 1, args: "parlay-cli agent-up --agent deckhand"},
		{pid: 103, ppid: 1, args: "vim notes-about-parlay-listen.md"},
		{pid: 104, ppid: 1, args: "grep listen --agent mayor"}, // not a parlay binary
	}, nil)

	got, ok := LiveListenerAgents()
	if !ok {
		t.Fatal("a readable process table must report ok")
	}
	want := map[string]bool{"mayor": true, "mc-robots-jkwc": true, "deckhand": true}
	if len(got) != len(want) {
		t.Fatalf("LiveListenerAgents = %v, want exactly %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("LiveListenerAgents missed the live listener for %q", id)
		}
	}
}

func TestLiveListenerAgentsReportsNotOkWhenPsFails(t *testing.T) {
	stubProcessTable(t, nil, errors.New("ps unavailable"))
	got, ok := LiveListenerAgents()
	if ok {
		t.Errorf("a failed ps must report not-ok, got ok with %v", got)
	}
	if got != nil {
		t.Errorf("a failed ps must return no set, got %v", got)
	}
}

func TestLiveListenerAgentsIsEmptyNotUnknownOnACleanBox(t *testing.T) {
	// The distinction callers depend on: "ps worked and found nothing" is a
	// real answer (every registration is a ghost); "ps failed" is not.
	stubProcessTable(t, []procEntry{{pid: 100, ppid: 1, args: "zsh"}}, nil)
	got, ok := LiveListenerAgents()
	if !ok || len(got) != 0 {
		t.Errorf("LiveListenerAgents = %v, ok=%v; want an empty set with ok=true", got, ok)
	}
}

func TestListenerAgentReturnsTheIdItIsPolling(t *testing.T) {
	cases := []struct{ args, want string }{
		{"parlay-cli listen --agent mayor", "mayor"},
		{"parlay monitor --agent=mc-robots-jkwc", "mc-robots-jkwc"},
		{"parlay-cli listen --notify-safe --agent mayor", "mayor"},
		{"parlay-cli listen --agent", ""},            // truncated argv
		{"parlay-cli listen --notify-safe", ""},      // a loop with no --agent
		{"parlay-cli listen --name a --agent m", ""}, // free text: unparseable
		{"parlay-cli send --agent mayor hi", ""},     // not a loop verb
	}
	for _, tc := range cases {
		if got := listenerAgent(tc.args); got != tc.want {
			t.Errorf("listenerAgent(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// The takeover announcement is the only thing an operator sees at the moment a
// listener dies, and the match is host-wide by agent ID: verified 2026-10-05
// that two instances on different servers evict each other's listener for a
// colliding id. The message therefore has to carry that scope and the opt-out
// itself — "this channel keeps exactly one" reads as per-instance and is the
// wording that let the cross-instance case look impossible.
func TestReapAnnouncementNamesTheHostWideScopeAndTheOptOut(t *testing.T) {
	stubProcessTable(t, []procEntry{
		{pid: 601, ppid: 1, args: "/usr/local/bin/parlay-cli listen --agent mayor"},
	}, nil)
	recordSignals(t, map[int]bool{601: true})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	reapDuplicateListeners("mayor")
	os.Stderr = orig
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	msg := string(got)

	for _, want := range []string{"HOST-WIDE", "any server", "PARLAY_LISTEN_NO_SINGLETON=1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("reap announcement does not mention %q; an operator whose OTHER instance just lost its\nlistener reads only this line, and needs to learn the scope and the escape hatch there:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "keeps exactly one\n") {
		t.Errorf("reap announcement still scopes the takeover to a channel: %q", msg)
	}
}
