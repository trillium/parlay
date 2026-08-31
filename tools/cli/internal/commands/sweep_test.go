package commands

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doneTaskAgent is the ordinary sweep target: a mechanic-dispatch agent with
// a bound task that has posted `done`.
func doneTaskAgent(id string) SweepAgent {
	return SweepAgent{ID: id, State: "done", Detail: "PR merged", HasFrontmatter: true, Task: "robots-abcd"}
}

func TestClassifySweepClosesDoneTaskBoundAgent(t *testing.T) {
	v := ClassifySweep(doneTaskAgent("mc-robots-abcd"), SweepOpts{})
	if v.Action != SweepTeardown {
		t.Fatalf("done + bound task must be closeable, got %s (%s)", v.Action, v.Reason)
	}
	if v.Reason != "done · task robots-abcd" {
		t.Errorf("reason should name why it is closeable, got %q", v.Reason)
	}
}

func TestClassifySweepClosesDoneWorktreeAgent(t *testing.T) {
	a := SweepAgent{ID: "wt-agent", State: "done", HasFrontmatter: true, Worktree: "/tmp/wt"}
	v := ClassifySweep(a, SweepOpts{})
	if v.Action != SweepTeardown {
		t.Fatalf("done + recorded worktree must be closeable, got %s (%s)", v.Action, v.Reason)
	}
	if v.Reason != "done · worktree /tmp/wt" {
		t.Errorf("reason should name the worktree, got %q", v.Reason)
	}
}

func TestClassifySweepNeverSweepsItself(t *testing.T) {
	a := doneTaskAgent("mc-robots-6xq7")
	v := ClassifySweep(a, SweepOpts{Self: "mc-robots-6xq7"})
	if v.Action != SweepSkip {
		t.Fatalf("the sweeping agent must never sweep itself, got %s", v.Action)
	}
	// Even the explicit escape hatches must not override the self guard.
	v = ClassifySweep(a, SweepOpts{Self: "mc-robots-6xq7", Explicit: true, All: true, Force: true})
	if v.Action != SweepSkip {
		t.Fatalf("--agent/--all/--force must not defeat the self guard, got %s", v.Action)
	}
}

func TestClassifySweepHonorsKeepList(t *testing.T) {
	a := doneTaskAgent("mechanic")
	opts := SweepOpts{Keep: map[string]bool{"mechanic": true}}
	if v := ClassifySweep(a, opts); v.Action != SweepSkip {
		t.Fatalf("keep-listed agent must be skipped, got %s", v.Action)
	}
	opts.Explicit, opts.All, opts.Force = true, true, true
	if v := ClassifySweep(a, opts); v.Action != SweepSkip {
		t.Fatalf("--agent/--all/--force must not defeat the keep-list, got %s", v.Action)
	}
}

// The regression that motivated the whole safety design: an agent whose
// identity.md has no frontmatter (what a --worktree spawn produced while
// identity --register rejected --worktree; see internal/identity/store.go)
// may own a worktree full of unpushed commits that teardown's git checks
// never reach, because teardown only checks a RECORDED worktree.
func TestClassifySweepHoldsAgentWithNoLaunchSpec(t *testing.T) {
	a := SweepAgent{ID: "subprocess-deadlink", State: "done", Detail: "PR #9"}
	if v := ClassifySweep(a, SweepOpts{}); v.Action != SweepHold {
		t.Fatalf("empty launch spec must be HELD, got %s", v.Action)
	}
	if v := ClassifySweep(a, SweepOpts{All: true}); v.Action != SweepHold {
		t.Fatalf("--all must not defeat the empty-launch-spec guard, got %s", v.Action)
	}
	if v := ClassifySweep(a, SweepOpts{Explicit: true}); v.Action != SweepHold {
		t.Fatalf("--agent must not defeat the empty-launch-spec guard, got %s", v.Action)
	}
	if v := ClassifySweep(a, SweepOpts{Force: true}); v.Action != SweepTeardown {
		t.Fatalf("--force is the documented override, got %s (%s)", v.Action, v.Reason)
	}
}

func TestClassifySweepHoldsCaptainRelevantTerminalStates(t *testing.T) {
	for _, state := range []string{"needs-decision", "blocked", "failed"} {
		a := SweepAgent{ID: "x", State: state, Detail: "PR green, needs merge", HasFrontmatter: true, Task: "robots-abcd"}
		v := ClassifySweep(a, SweepOpts{All: true, Explicit: true, Force: true})
		if v.Action != SweepHold {
			t.Errorf("state %s must be HELD for the captain, got %s", state, v.Action)
		}
	}
}

func TestClassifySweepLeavesLiveAgentsAlone(t *testing.T) {
	for _, state := range []string{"working", "paused", "resolved", "captain-held"} {
		a := SweepAgent{ID: "x", State: state, HasFrontmatter: true, Task: "robots-abcd"}
		if v := ClassifySweep(a, SweepOpts{All: true}); v.Action != SweepSkip {
			t.Errorf("state %s must be skipped, got %s (%s)", state, v.Action, v.Reason)
		}
	}
}

func TestClassifySweepHoldsUnknownState(t *testing.T) {
	a := SweepAgent{ID: "x", State: "unknown", Detail: "agent not enrolled with relay", HasFrontmatter: true, Task: "robots-abcd"}
	v := ClassifySweep(a, SweepOpts{All: true})
	if v.Action != SweepHold {
		t.Fatalf("unknown state must be HELD, not closed, got %s", v.Action)
	}
	if v.Reason != "state=unknown — agent not enrolled with relay" {
		t.Errorf("hold reason should carry the oracle's detail, got %q", v.Reason)
	}
}

// A done agent that is neither task-bound nor worktree-recorded is a
// hand-made named agent: closeable only when the caller names it.
func TestClassifySweepHoldsUnprovenDoneAgentUntilNamed(t *testing.T) {
	a := SweepAgent{ID: "oswgate", State: "done", HasFrontmatter: true}
	v := ClassifySweep(a, SweepOpts{})
	if v.Action != SweepHold {
		t.Fatalf("unproven done agent must be HELD by default, got %s", v.Action)
	}
	if want := "parlay sweep --apply --agent oswgate"; !strings.Contains(v.Reason, want) {
		t.Errorf("hold reason should tell the reader how to proceed (%q), got %q", want, v.Reason)
	}
	if v := ClassifySweep(a, SweepOpts{Explicit: true}); v.Action != SweepTeardown {
		t.Errorf("--agent must make it closeable, got %s", v.Action)
	}
	if v := ClassifySweep(a, SweepOpts{All: true}); v.Action != SweepTeardown {
		t.Errorf("--all must make it closeable, got %s", v.Action)
	}
}

func TestClassifySweepSkipsEmptyID(t *testing.T) {
	if v := ClassifySweep(SweepAgent{State: "done"}, SweepOpts{Force: true}); v.Action != SweepSkip {
		t.Fatalf("empty id must be skipped, got %s", v.Action)
	}
}

func TestReadSweepKeepParsesCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	body := "# long-lived dispatchers\nmechanic\n\n  code  # the captain's shell agent\nmayor\n"
	if err := os.WriteFile(filepath.Join(dir, "sweep-keep"), []byte(body), 0o644); err != nil {
		t.Fatalf("write sweep-keep: %v", err)
	}
	keep := readSweepKeep()
	for _, id := range []string{"mechanic", "code", "mayor"} {
		if !keep[id] {
			t.Errorf("%s should be keep-listed", id)
		}
	}
	if len(keep) != 3 {
		t.Errorf("comments/blank lines must not become ids, got %v", keep)
	}
}

func TestReadSweepKeepMissingFileIsEmpty(t *testing.T) {
	t.Setenv("PARLAY_STATE_HOME", t.TempDir())
	if keep := readSweepKeep(); len(keep) != 0 {
		t.Fatalf("a missing keep-list must be empty, got %v", keep)
	}
}

// robots-8783: a sweep pass over N candidates must ask the relay for its
// registry exactly once, not once per candidate — per-agent probes made a
// dead-relay sweep of ~254 agents take >120s (3 attempts × 3s timeout each,
// serially). Pinned by counting subscriber hits, not by timing.
func TestSweepPassFetchesRegistryOnce(t *testing.T) {
	noRetrySleep(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_STATE_HOME", t.TempDir())

	subscriberCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/subscribers", func(w http.ResponseWriter, r *http.Request) {
		subscriberCalls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"registered":{"agents":[{"id":"agent-a","name":"n","color":"#fff"}]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("PARLAY_SERVER", srv.URL)

	for _, id := range []string{"agent-a", "agent-b", "agent-c", "agent-d"} {
		if err := os.MkdirAll(filepath.Join(home, ".parlay", "agents", id), 0o755); err != nil {
			t.Fatalf("mkdir agent home: %v", err)
		}
	}

	sweepPass("", false, false, SweepOpts{}, nil)

	if subscriberCalls != 1 {
		t.Fatalf("sweep pass over 4 candidates hit /api/chat/subscribers %d times, want exactly 1", subscriberCalls)
	}
}

// The batched fetch must preserve the robots-me7m three-way answer: a dead
// relay yields enrollmentUnknown for every candidate (status-degraded /
// fail-open), never enrolledNo.
func TestSweepDeadRelayIsUnknownNotUnenrolled(t *testing.T) {
	noRetrySleep(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_STATE_HOME", t.TempDir())
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1") // nothing listening

	if err := os.MkdirAll(filepath.Join(home, ".parlay", "agents", "leg9"), 0o755); err != nil {
		t.Fatalf("mkdir agent home: %v", err)
	}
	writeStatus(t, home, "leg9", "working: still going\n")

	reg, regOK := fetchRegisteredAgents()
	agent := resolveSweepAgent("leg9", reg, regOK)
	if agent.State != "working" {
		t.Fatalf("resolveSweepAgent with dead relay = %+v, want state=working from the status file", agent)
	}
	if strings.Contains(agent.Detail, "not registered") || strings.Contains(agent.Detail, "does not list") {
		t.Errorf("detail = %q must not claim unenrollment — the relay never answered", agent.Detail)
	}
}
