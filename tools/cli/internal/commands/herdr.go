// Closing an agent's terminal surface — the second half of teardown
// (robots-iz9o).
//
// `parlay teardown` (and therefore `parlay sweep --apply`, which drives the
// same chain) used to unregister the agent from the relay, remove its
// worktree and delete its store — and stop there. The herdr pane the agent
// actually runs in was never touched, so a sweep reported `closed` for a
// fleet of agents whose OS processes and panes were all still alive; the
// captain then had to walk 57 of them by hand with `herdr tab close`.
// "Reclaimed" that leaves the process running is worse than not sweeping at
// all, because the sweep's own log says the resource is gone.
//
// The mapping teardown needs is set up at spawn time by the spawn pipeline (internal/spawn):
// `herdr agent start <id>` names the herdr agent after the parlay agent id,
// and `herdr tab create --label <id>` labels its tab the same. So the parlay
// agent id is the lookup key on both sides, and both sides matter:
//
//   - a swept `done` agent is usually still LIVE, so `herdr agent get <id>`
//     resolves its pane and tab directly;
//   - an agent whose process already exited leaves its tab behind with the
//     label still on it and no herdr agent to find — the residue that
//     accumulates in `herdr tab list` as dozens of stale `mc-*` tabs. Only
//     the label lookup can see those.
//
// Everything here is best-effort by construction, like bestEffortUnregister:
// no herdr on PATH, no daemon, an unparseable reply or an agent herdr has
// never heard of all resolve to "nothing to close". Teardown's real work —
// the git safety checks, the worktree removal, the store delete — must never
// be blocked or reverted by a terminal multiplexer being unavailable.
package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// herdrCloseKinds. Pane is the narrow close, used when the tab holds panes
// this agent does not own; Tab is the full close, which also reclaims the
// tab itself rather than leaving an empty one behind.
const (
	herdrCloseNone = ""
	herdrClosePane = "pane"
	herdrCloseTab  = "tab"
)

// herdrSurface is one agent's terminal surface as herdr reports it, already
// resolved from JSON so the policy below stays pure.
type herdrSurface struct {
	PaneID string
	TabID  string
	// PaneCount is how many panes the tab holds, 0 when unknown (the tab was
	// found by label with no pane_count, or `tab get` did not answer).
	PaneCount int
}

// herdrCloseAction is what to do about that surface: one herdr subcommand,
// or nothing.
type herdrCloseAction struct {
	Kind   string
	Target string
}

// classifyHerdrClose picks between closing the tab and closing just the
// pane. Pure, so herdr_test.go can pin each rule with no daemon.
//
// Closing the tab is the desired outcome — a pane-only close can leave an
// empty tab in the tab list, which is the same "looks reclaimed, isn't"
// failure one level up. But `herdr tab close` takes every pane in the tab
// with it, so it is only safe once the tab is known to hold nothing but
// this agent. PaneCount > 1 means the tab is shared (a split, or a pane the
// captain opened alongside the agent), and destroying a bystander's pane
// during an unattended sweep would be a far worse defect than the one this
// fixes — so a shared tab gets the narrow close and keeps the tab.
//
// PaneCount == 0 means "not reported", not "empty": the label-fallback path
// finds a tab whose agent is already gone, and closing that whole tab is
// exactly the intent.
func classifyHerdrClose(s herdrSurface) herdrCloseAction {
	if s.TabID != "" && s.PaneCount <= 1 {
		return herdrCloseAction{Kind: herdrCloseTab, Target: s.TabID}
	}
	if s.PaneID != "" {
		return herdrCloseAction{Kind: herdrClosePane, Target: s.PaneID}
	}
	if s.TabID != "" {
		return herdrCloseAction{Kind: herdrCloseTab, Target: s.TabID}
	}
	return herdrCloseAction{Kind: herdrCloseNone}
}

// herdrJSON runs a herdr subcommand and parses its stdout. herdr exits 0
// even when it prints an `error` object (verified against `herdr agent get
// <unknown>`), so the exit code is deliberately ignored and the reply body
// is the only signal — same reasoning as internal/spawn's runHerdrJSON.
// A missing binary, a dead daemon or unparseable output all return nil.
func herdrJSON(argv ...string) map[string]any {
	if _, err := exec.LookPath("herdr"); err != nil {
		return nil
	}
	cmd := exec.Command("herdr", argv...)
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		return nil
	}
	if _, isErr := v["error"]; isErr {
		return nil
	}
	return v
}

// herdrDigString walks a decoded reply to a string leaf, "" if any hop is
// missing or the wrong type.
func herdrDigString(v map[string]any, path ...string) string {
	var cur any = v
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[p]
	}
	s, _ := cur.(string)
	return s
}

// herdrDigInt is herdrDigString for a JSON number (always float64 through
// encoding/json's `any` path).
func herdrDigInt(v map[string]any, path ...string) int {
	var cur any = v
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = m[p]
	}
	n, _ := cur.(float64)
	return int(n)
}

// resolveHerdrSurface finds agentID's pane/tab, preferring the live agent
// lookup and falling back to the tab label for an agent whose process has
// already exited (see the package comment).
func resolveHerdrSurface(agentID string) herdrSurface {
	if v := herdrJSON("agent", "get", agentID); v != nil {
		s := herdrSurface{
			PaneID: herdrDigString(v, "result", "agent", "pane_id"),
			TabID:  herdrDigString(v, "result", "agent", "tab_id"),
		}
		if s.PaneID != "" || s.TabID != "" {
			s.PaneCount = herdrTabPaneCount(s.TabID)
			return s
		}
	}
	return herdrSurface{TabID: herdrTabIDForLabel(agentID)}
}

// herdrTabPaneCount reports how many panes tabID holds, 0 if herdr does not
// answer — which classifyHerdrClose reads as "not reported".
func herdrTabPaneCount(tabID string) int {
	if tabID == "" {
		return 0
	}
	v := herdrJSON("tab", "get", tabID)
	if v == nil {
		return 0
	}
	return herdrDigInt(v, "result", "tab", "pane_count")
}

// herdrTabIDForLabel finds the tab `parlay spawn` labeled with this agent id.
// Ties (a label reused across tabs) resolve to the first listed; the next
// sweep pass picks up whatever is left, which is strictly better than
// closing tabs this call cannot attribute.
func herdrTabIDForLabel(label string) string {
	if label == "" {
		return ""
	}
	v := herdrJSON("tab", "list")
	if v == nil {
		return ""
	}
	result, _ := v["result"].(map[string]any)
	tabs, _ := result["tabs"].([]any)
	for _, t := range tabs {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if l, _ := tm["label"].(string); l != label {
			continue
		}
		id, _ := tm["tab_id"].(string)
		return id
	}
	return ""
}

// closeHerdrSurface closes agentID's terminal surface and returns a suffix
// for teardown's success line naming what it closed (or "" when there was
// nothing to close, so the message is unchanged for agents with no pane).
//
// It refuses to close the CALLER's own surface: `parlay teardown $SELF`
// would otherwise kill the very pane running the command, mid-command,
// before it could print its own result. `parlay sweep` already never sweeps
// itself; this makes the guard hold on the direct teardown path too.
func closeHerdrSurface(agentID string) string {
	if agentID == "" {
		return ""
	}
	if self := strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID")); self != "" && self == agentID {
		return " · herdr pane left open (this is the calling agent)"
	}

	action := classifyHerdrClose(resolveHerdrSurface(agentID))
	switch action.Kind {
	case herdrCloseTab:
		if err := exec.Command("herdr", "tab", "close", action.Target).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warn: herdr tab close %s failed — %v\n", action.Target, err)
			return ""
		}
	case herdrClosePane:
		if err := exec.Command("herdr", "pane", "close", action.Target).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warn: herdr pane close %s failed — %v\n", action.Target, err)
			return ""
		}
	default:
		return ""
	}
	return fmt.Sprintf(" · herdr %s %s closed", action.Kind, action.Target)
}
