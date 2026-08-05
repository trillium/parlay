package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyHerdrClosePrefersTheTabWhenTheAgentOwnsItAlone(t *testing.T) {
	got := classifyHerdrClose(herdrSurface{PaneID: "w1:p2", TabID: "w1:t2", PaneCount: 1})
	if got.Kind != herdrCloseTab || got.Target != "w1:t2" {
		t.Fatalf("want tab close of w1:t2, got %+v", got)
	}
}

func TestClassifyHerdrCloseNarrowsToThePaneWhenTheTabIsShared(t *testing.T) {
	// The bystander guard: `herdr tab close` would take the other panes with
	// it, which during an unattended sweep destroys work this agent does not
	// own.
	got := classifyHerdrClose(herdrSurface{PaneID: "w1:p2", TabID: "w1:t2", PaneCount: 3})
	if got.Kind != herdrClosePane || got.Target != "w1:p2" {
		t.Fatalf("want pane close of w1:p2, got %+v", got)
	}
}

func TestClassifyHerdrCloseTreatsUnreportedPaneCountAsCloseTheTab(t *testing.T) {
	// The label-fallback shape: the agent process is gone, so only the tab
	// was resolved and PaneCount is 0 meaning "not reported", not "empty".
	got := classifyHerdrClose(herdrSurface{TabID: "w1:t9"})
	if got.Kind != herdrCloseTab || got.Target != "w1:t9" {
		t.Fatalf("want tab close of w1:t9, got %+v", got)
	}
}

func TestClassifyHerdrCloseFallsBackToThePaneWhenNoTabIsKnown(t *testing.T) {
	got := classifyHerdrClose(herdrSurface{PaneID: "w1:p2"})
	if got.Kind != herdrClosePane || got.Target != "w1:p2" {
		t.Fatalf("want pane close of w1:p2, got %+v", got)
	}
}

func TestClassifyHerdrCloseDoesNothingWithNoSurface(t *testing.T) {
	if got := classifyHerdrClose(herdrSurface{}); got.Kind != herdrCloseNone {
		t.Fatalf("want no action, got %+v", got)
	}
}

// stubHerdr puts a fake `herdr` on PATH that replies with the given
// per-subcommand JSON and appends every invocation to a log file, so the
// tests below can assert both what was closed and that nothing else was.
// Keys are matched as a prefix of the joined argv ("agent get x").
func stubHerdr(t *testing.T, replies map[string]string) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")

	var cases strings.Builder
	for prefix, reply := range replies {
		cases.WriteString("if [ \"${ARGS#" + prefix + "}\" != \"$ARGS\" ]; then printf '%s' '" + reply + "'; exit 0; fi\n")
	}
	script := "#!/bin/sh\nARGS=\"$*\"\nprintf '%s\\n' \"$ARGS\" >> " + logPath + "\n" + cases.String() + "exit 0\n"

	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("PARLAY_AGENT_ID", "")
	return logPath
}

func herdrCalls(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestCloseHerdrSurfaceClosesTheTabOfALiveSoleAgent(t *testing.T) {
	log := stubHerdr(t, map[string]string{
		"agent get a1":  `{"result":{"agent":{"pane_id":"w1:p2","tab_id":"w1:t2"}}}`,
		"tab get w1:t2": `{"result":{"tab":{"tab_id":"w1:t2","pane_count":1}}}`,
	})

	msg := closeHerdrSurface("a1")
	if !strings.Contains(msg, "herdr tab w1:t2 closed") {
		t.Fatalf("want the message to name the closed tab, got %q", msg)
	}
	if calls := herdrCalls(t, log); !strings.Contains(calls, "tab close w1:t2") {
		t.Fatalf("want a tab close call, got:\n%s", calls)
	}
}

func TestCloseHerdrSurfaceClosesOnlyThePaneWhenTheTabIsShared(t *testing.T) {
	log := stubHerdr(t, map[string]string{
		"agent get a1":  `{"result":{"agent":{"pane_id":"w1:p2","tab_id":"w1:t2"}}}`,
		"tab get w1:t2": `{"result":{"tab":{"tab_id":"w1:t2","pane_count":2}}}`,
	})

	msg := closeHerdrSurface("a1")
	if !strings.Contains(msg, "herdr pane w1:p2 closed") {
		t.Fatalf("want the message to name the closed pane, got %q", msg)
	}
	calls := herdrCalls(t, log)
	if !strings.Contains(calls, "pane close w1:p2") {
		t.Fatalf("want a pane close call, got:\n%s", calls)
	}
	if strings.Contains(calls, "tab close") {
		t.Fatalf("must not close a shared tab out from under its other panes, got:\n%s", calls)
	}
}

func TestCloseHerdrSurfaceFallsBackToTheLabelledTabWhenTheAgentIsGone(t *testing.T) {
	// The stale-residue case that accumulates in `herdr tab list`: the agent
	// process exited, so `agent get` errors, but its labelled tab is still
	// open and is exactly what the sweep must reclaim.
	log := stubHerdr(t, map[string]string{
		"agent get a1": `{"error":{"code":"agent_not_found"}}`,
		"tab list":     `{"result":{"tabs":[{"tab_id":"w1:t1","label":"other"},{"tab_id":"w1:t9","label":"a1"}]}}`,
	})

	msg := closeHerdrSurface("a1")
	if !strings.Contains(msg, "herdr tab w1:t9 closed") {
		t.Fatalf("want the labelled tab closed, got %q", msg)
	}
	if calls := herdrCalls(t, log); !strings.Contains(calls, "tab close w1:t9") {
		t.Fatalf("want a tab close call for the labelled tab, got:\n%s", calls)
	}
}

func TestCloseHerdrSurfaceClosesNothingWhenHerdrKnowsNoSuchAgent(t *testing.T) {
	log := stubHerdr(t, map[string]string{
		"agent get a1": `{"error":{"code":"agent_not_found"}}`,
		"tab list":     `{"result":{"tabs":[{"tab_id":"w1:t1","label":"other"}]}}`,
	})

	if msg := closeHerdrSurface("a1"); msg != "" {
		t.Fatalf("want no suffix when there is nothing to close, got %q", msg)
	}
	if calls := herdrCalls(t, log); strings.Contains(calls, "close") {
		t.Fatalf("must not close anything it cannot attribute, got:\n%s", calls)
	}
}

func TestCloseHerdrSurfaceNeverClosesTheCallingAgentsOwnPane(t *testing.T) {
	// `parlay teardown $SELF` would otherwise kill the pane running the
	// command before it could print its own result.
	log := stubHerdr(t, map[string]string{
		"agent get a1": `{"result":{"agent":{"pane_id":"w1:p2","tab_id":"w1:t2"}}}`,
	})
	t.Setenv("PARLAY_AGENT_ID", "a1")

	msg := closeHerdrSurface("a1")
	if !strings.Contains(msg, "left open") {
		t.Fatalf("want the message to say the pane was left open, got %q", msg)
	}
	if calls := herdrCalls(t, log); calls != "" {
		t.Fatalf("want no herdr calls at all for self, got:\n%s", calls)
	}
}

func TestCloseHerdrSurfaceIsSilentWhenHerdrIsNotInstalled(t *testing.T) {
	// Teardown's real work must never be blocked by a missing multiplexer.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PARLAY_AGENT_ID", "")
	if msg := closeHerdrSurface("a1"); msg != "" {
		t.Fatalf("want no suffix without herdr on PATH, got %q", msg)
	}
}
