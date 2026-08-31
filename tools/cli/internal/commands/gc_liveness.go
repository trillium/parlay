// parlay gc-liveness — confirm-or-report startup watchdog for a gc-launched
// session (spawn-lift unit 6, epic task-4cfpv.9).
//
// The gc launcher delivers the charter exactly once, at session start,
// through the synthesised template (gctemplate's prompt.template.md /
// prompt_mode). This verb is the post-launch watchdog that replaces the
// herdr path's weakest link — the 60s timeout that re-prompts the ENTIRE
// charter (bin/parlay-spawn's `_herdr_agent_prompt_wait`, which can double
// the charter). The contract here is deliverStartupTurn's (pinned gc,
// internal/runtime/herdr/client.go): CONFIRM the startup turn happened, or
// REPORT that it did not — never re-submit the charter.
//
//   - Confirm: the agent's first turn enrolls it with the parlay server, so
//     liveness is read from the agent's own emitted effect — its channel
//     appearing in GET /api/chat/subscribers. Emitted output, never elapsed
//     time; the deadline below is a poll bound, not an assertion.
//   - Report: on timeout, steering is routed through the gc-nudge capability
//     gate (R7). On the subprocess provider that yields a typed refusal —
//     structurally, this watchdog CANNOT re-prompt a session whose provider
//     has no injection capability. On an injection-capable provider it
//     delivers a short fixed kick (never the charter) via gc's verified
//     nudge and reports gc's confirmation either way.
//
// The JSON envelope is the charter-delivery record: bin/parlay-spawn's
// watchdog appends it to the agent dir so "was the startup turn confirmed?"
// has a durable, machine-readable answer.
package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// gcLivenessKick is the fixed steering message used when the startup turn
// never fired AND the provider supports injection. Deliberately NOT the
// charter and deliberately constant: the charter went out once via the
// template, and re-prompting it is the exact double-delivery this verb
// exists to prevent.
const gcLivenessKick = "parlay liveness watchdog: your startup charter was delivered at launch but no first turn was observed — begin working from it now (it will not be re-sent)."

// gcLivenessResult is the typed --json envelope — the charter-delivery
// record. Confirmed=true: the startup turn was observed from the agent's own
// emitted registration. Otherwise Steer carries the capability-gated
// steering outcome (a refusal on the subprocess provider).
type gcLivenessResult struct {
	OK        bool           `json:"ok"`
	AgentID   string         `json:"agent_id"`
	SessionID string         `json:"session_id"`
	Confirmed bool           `json:"confirmed"`
	Via       string         `json:"via,omitempty"`
	Steer     *gcNudgeResult `json:"steer,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// gcLivenessPollInterval is how often the subscribers endpoint is polled.
var gcLivenessPollInterval = 1 * time.Second

// gcLivenessObserved asks the parlay server whether agentID's channel is
// registered. Bounded client per call — never the unbounded one
// (internal/httpc doctrine: monitor.pollOnce is its only legitimate caller).
func gcLivenessObserved(server, agentID string) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(server, "/") + "/api/chat/subscribers")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	var subs []struct {
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&subs); err != nil {
		return false
	}
	for _, s := range subs {
		if s.Channel == agentID {
			return true
		}
	}
	return false
}

// gcLivenessRun is the testable core: poll until the agent is observed or
// the deadline passes, then confirm or report.
func gcLivenessRun(agentID, sessionID, server string, timeout time.Duration) gcLivenessResult {
	res := gcLivenessResult{AgentID: agentID, SessionID: sessionID}
	deadline := time.Now().Add(timeout)
	for {
		if gcLivenessObserved(server, agentID) {
			res.OK = true
			res.Confirmed = true
			res.Via = "subscribers"
			return res
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(gcLivenessPollInterval)
	}

	// Report path: the startup turn was never observed. Steering goes
	// through the R7 capability gate — never a direct `gc session nudge`,
	// never the charter.
	steer, err := gcNudgeRun(agentID, sessionID, gcLivenessKick)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Steer = &steer
	res.OK = steer.OK
	return res
}

// GCLiveness implements `parlay gc-liveness <agent-id> --session <id>
// --server <url> [--timeout-ms N] [--json]`. Exit codes: 0 when the startup
// turn was confirmed (or a capable provider's kick was confirmed delivered);
// 1 when it was not — the --json envelope says which.
func GCLiveness(argv []string) {
	if helpWanted("gc-liveness", argv) {
		return
	}
	r := args.Parse("gc-liveness", argv, []string{"--json"}, []string{"--session", "--server", "--timeout-ms"})
	asJSON := r.Bool("--json")
	if len(r.Positionals) != 1 {
		httpc.Die("parlay gc-liveness: usage: parlay gc-liveness <agent-id> --session <gc-session-id> --server <url> [--timeout-ms N] [--json]", config.ExitUsage)
		return
	}
	agentID := r.Positionals[0]
	sessionID, _ := r.String("--session")
	server, ok := r.String("--server")
	if !ok || server == "" {
		httpc.Die("parlay gc-liveness: --server <url> is required (the spawned agent's PARLAY_SERVER)", config.ExitUsage)
		return
	}
	timeout := 60 * time.Second
	if ms, ok := r.String("--timeout-ms"); ok {
		n, err := strconv.Atoi(ms)
		if err != nil || n < 0 {
			httpc.Die(fmt.Sprintf("parlay gc-liveness: --timeout-ms must be a non-negative integer (got %q)", ms), config.ExitUsage)
			return
		}
		timeout = time.Duration(n) * time.Millisecond
	}

	res := gcLivenessRun(agentID, sessionID, server, timeout)
	if asJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
	} else if res.Confirmed {
		fmt.Printf("confirmed: agent %s observed in /api/chat/subscribers\n", agentID)
	} else if res.Steer != nil && res.Steer.Refused {
		fmt.Printf("report: startup turn not observed within the window; steering refused: %s\n", res.Steer.Reason)
	} else if res.Error != "" {
		fmt.Printf("report: startup turn not observed; steering errored: %s\n", res.Error)
	} else {
		fmt.Printf("report: startup turn not observed; kick delivery ok=%v\n", res.OK)
	}
	if !res.OK {
		os.Exit(config.ExitRuntime)
	}
}
