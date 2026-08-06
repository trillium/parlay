package commands

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// spentPane is the defect shape robots-9d2w was filed for: a mechanic that
// posted `done` and is sitting at its prompt with a full transcript behind it.
func spentPane(id string) StaleWindow {
	return StaleWindow{ID: id, State: "done", Detail: "PR #63 merged"}
}

func TestClassifyStaleWindowFlagsDonePane(t *testing.T) {
	v := ClassifyStaleWindow(spentPane("mc-robots-g4qz"))
	if !v.Stale {
		t.Fatalf("a done pane is the whole point of this verb, got FRESH (%s)", v.Reason)
	}
	if v.ExitCode != ExitStale {
		t.Errorf("exit code = %d, want %d (branchable relaunch signal)", v.ExitCode, ExitStale)
	}
	if !strings.Contains(v.Reason, "state=done") {
		t.Errorf("reason should name the state, got %q", v.Reason)
	}
	if !strings.Contains(v.Reason, "PR #63 merged") {
		t.Errorf("reason should carry the status detail, got %q", v.Reason)
	}
}

func TestClassifyStaleWindowFlagsFailedPane(t *testing.T) {
	v := ClassifyStaleWindow(StaleWindow{ID: "mc-x", State: "failed", Detail: "gate refused"})
	if !v.Stale {
		t.Fatalf("failed is terminal too — the pane stopped and its context is spent, got FRESH (%s)", v.Reason)
	}
}

// The exclusions below are each load-bearing: a stale check that swallows the
// fleet's steering path is a worse defect than the one it fixes.
func TestClassifyStaleWindowNeverFlagsAgentsAwaitingAReply(t *testing.T) {
	for _, state := range []string{"needs-decision", "blocked"} {
		v := ClassifyStaleWindow(StaleWindow{ID: "mc-x", State: state, Detail: "which option?"})
		if v.Stale {
			t.Errorf("state=%s is WAITING on a message — refusing that send breaks the unblock path", state)
		}
		if !strings.Contains(v.Reason, "waiting on a reply") {
			t.Errorf("state=%s reason should say why it is sendable, got %q", state, v.Reason)
		}
		if v.ExitCode != 0 {
			t.Errorf("state=%s exit = %d, want 0", state, v.ExitCode)
		}
	}
}

func TestClassifyStaleWindowNeverFlagsWorkingOrPaused(t *testing.T) {
	for _, state := range []string{"working", "paused", "resolved", "captain-held"} {
		if ClassifyStaleWindow(StaleWindow{ID: "mc-x", State: state}).Stale {
			t.Errorf("state=%s must not read as a spent window", state)
		}
	}
}

// Fail-open. CrewStateForAgent returns unknown whenever the relay is
// unreachable, so a stale check that treats unknown as stale converts every
// transport hiccup into a refused send — trading a token leak for a lost
// message, which robots-ngg5 established is the worse failure.
func TestClassifyStaleWindowFailsOpenOnUnknown(t *testing.T) {
	v := ClassifyStaleWindow(StaleWindow{ID: "mc-x", State: "unknown", Detail: "agent not enrolled with relay"})
	if v.Stale {
		t.Fatal("unknown state must never be stale — a relay hiccup cannot become a refused send")
	}
	if !strings.Contains(v.Reason, "not provably spent") {
		t.Errorf("reason should say WHY unknown is not stale, got %q", v.Reason)
	}
}

// A keep-listed agent is one designed to be re-tasked in place, so its `done`
// is a resting state, not a spent window. Same list, same reason, as sweep's.
func TestClassifyStaleWindowHonorsKeepList(t *testing.T) {
	w := spentPane("dispatcher")
	w.Keep = true
	v := ClassifyStaleWindow(w)
	if v.Stale {
		t.Fatal("a keep-listed agent sits at done between jobs by design — never stale")
	}
	if !strings.Contains(v.Reason, "sweep-keep") {
		t.Errorf("reason should name the escape hatch, got %q", v.Reason)
	}
}

func TestClassifyStaleWindowReportsSessionAge(t *testing.T) {
	w := spentPane("mc-x")
	w.SessionAge = 95 * time.Minute
	if got := ClassifyStaleWindow(w).Reason; !strings.Contains(got, "session up 1.6h") {
		t.Errorf("reason should carry the pane's age as the token-cost evidence, got %q", got)
	}

	w.SessionAge = 12 * time.Minute
	if got := ClassifyStaleWindow(w).Reason; !strings.Contains(got, "session up 12m") {
		t.Errorf("sub-hour age should read in minutes, got %q", got)
	}
}

// Age is reported, never decisive — a five-minute agent that posted `done` is
// already spent, and a six-hour `working` agent is not.
func TestClassifyStaleWindowDoesNotDecideOnAge(t *testing.T) {
	young := spentPane("mc-x")
	young.SessionAge = 30 * time.Second
	if !ClassifyStaleWindow(young).Stale {
		t.Error("a brand-new pane that already posted done is still stale")
	}
	old := StaleWindow{ID: "mc-y", State: "working", SessionAge: 8 * time.Hour}
	if ClassifyStaleWindow(old).Stale {
		t.Error("a long-running WORKING agent is not stale — age alone must not trip it")
	}
}

func TestReadSessionAgeToleratesJunk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", home)

	seed := func(id, body string) {
		dir := filepath.Join(home, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session-start"), []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	seed("junk", "not-a-timestamp\n")
	seed("future", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	seed("good", strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10)+"\n")

	if got := readSessionAge("junk"); got != 0 {
		t.Errorf("unparseable stamp = %v, want 0 (reporting colour must not fail the caller)", got)
	}
	if got := readSessionAge("future"); got != 0 {
		t.Errorf("clock-skewed future stamp = %v, want 0", got)
	}
	if got := readSessionAge("missing-agent"); got != 0 {
		t.Errorf("missing store = %v, want 0", got)
	}
	if got := readSessionAge("good"); got < 119*time.Minute || got > 121*time.Minute {
		t.Errorf("readSessionAge = %v, want ~2h", got)
	}
}

func TestRelaunchAdviceNamesBothHalvesOfTheRemedy(t *testing.T) {
	t.Setenv("PARLAY_STATE_HOME", "/tmp/state")
	advice := relaunchAdvice("mc-robots-9d2w")
	for _, want := range []string{
		"parlay sweep --apply --agent mc-robots-9d2w", // close the spent pane
		"--claim",               // spawn a fresh one
		"/tmp/state/sweep-keep", // and the escape hatch if it was wrong
	} {
		if !strings.Contains(advice, want) {
			t.Errorf("relaunch advice missing %q:\n%s", want, advice)
		}
	}
}

func TestStaleVerbRequiresAnAgentID(t *testing.T) {
	code, exited := withExitTrap(t, func() { Stale(nil) })
	if !exited || code != 2 {
		t.Fatalf("bare 'parlay stale' should be a usage error: exited=%v code=%d", exited, code)
	}
}
