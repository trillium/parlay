// `parlay listen` (alias `parlay agent-up`) — one-call agent
// self-enrollment. Ported from packages/cli/src/listen.ts.
//
// Collapses three previously separate agent-driven steps into one atomic,
// idempotent call:
//  1. add-self-to-agent-registry: POST /api/chat/register-agent (identity +
//     optional --caps), so the tab/registry entry exists under this id.
//  2. Announce "listening" on the agent's own channel via /api/chat/reply.
//  3. Hand off into the poll loop — the SAME relay-backed monitor as
//     `parlay monitor`, reused (not duplicated) via runMonitor.
package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
)

// runMonitor is the monitor invocation CmdListen hands off to after
// register+announce. Package-level var (not a hardcoded call to CmdMonitor)
// so tests can substitute a fake and assert on the args it was handed
// without spawning a real process or blocking forever in the poll loop —
// the Go analogue of listen.ts's injectable MonitorDeps.runMonitor.
var runMonitor = CmdMonitor

// ensureSingleListener is the robots-fgyz singleton guard, injectable for the
// same reason as runMonitor: the real one reads the live process table and
// signals real pids, which no unit test may do.
var ensureSingleListener = reapDuplicateListeners

// setSpawnAccount is the --account persistence hook: writing the default
// ccjuggler spawn account to config.toml BEFORE any network call so a spawn's
// token resolution cannot watch one account while config.toml holds another.
// Injectable for the same reason as runMonitor and ensureSingleListener — the
// real one writes the operator's ~/.parlay/config.toml, which no unit test
// may touch.
var setSpawnAccount = config.SetSpawnAccount

type registerAgentResponse struct {
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
}

type listenReplyResponse struct {
	OK    bool   `json:"ok,omitempty"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

// CmdListen is `parlay listen`/`parlay agent-up`'s entry point.
func CmdListen(argv []string) {
	if help.Wanted("listen", argv) {
		return
	}
	res := args.Parse("listen", argv, []string{"--legacy-poll", "--notify-safe"}, []string{"--agent", "--name", "--color", "--caps", "--account"})

	agentRaw, _ := res.String("--agent")
	agent := strings.TrimSpace(agentRaw)
	if agent == "" {
		httpc.Die("parlay listen: --agent <id> is required", config.ExitUsage)
		return
	}

	nameRaw, _ := res.String("--name")
	name := strings.TrimSpace(nameRaw)
	if name == "" {
		name = agent
	}

	colorRaw, _ := res.String("--color")
	color := strings.TrimSpace(colorRaw)
	if color == "" {
		color = identity.ColorFromID(agent)
	}

	body := map[string]any{"id": agent, "name": name, "color": color}
	// TS's `if (opts["--caps"])` is falsy for both "no flag" and "flag with
	// an empty string value" — matched here by checking capsRaw != "", not
	// just presence, so `--caps ""` silently omits caps same as omitting
	// the flag entirely, rather than dying on empty-string-is-not-JSON.
	if capsRaw, hasCaps := res.String("--caps"); hasCaps && capsRaw != "" {
		var caps any
		if err := json.Unmarshal([]byte(capsRaw), &caps); err != nil {
			httpc.Die(fmt.Sprintf("parlay listen: --caps must be valid JSON (got '%s')", capsRaw), config.ExitUsage)
			return
		}
		body["caps"] = caps
	}

	// 0. --account (optional): persist the default ccjuggler spawn account so
	// every subsequent spawn — not just this agent — comes up under it. Runs
	// BEFORE any network call and before the singleton guard signals anything:
	// an enrollment that then fails mid-way must not leave the channel's
	// account resolution and config.toml disagreeing. Matches the --caps
	// convention: an empty value is treated as the flag being absent, so
	// `--account ""` never surprises an operator by overwriting their config.
	if accRaw, hasAcc := res.String("--account"); hasAcc && strings.TrimSpace(accRaw) != "" {
		acc := strings.TrimSpace(accRaw)
		if err := setSpawnAccount(acc); err != nil {
			httpc.Die(fmt.Sprintf("parlay listen: persist default spawn account: %v", err), config.ExitRuntime)
			return
		}
		fmt.Fprintf(os.Stderr, "parlay listen: persisted default spawn account: %s\n", acc)
	}

	// 1. Singleton guard (robots-fgyz). Arming is a takeover, not an addition:
	// any other live poll loop on this agent's channel is ended first, so the
	// channel keeps exactly one reader. Runs before register/announce so a
	// duplicate is never left alive by a later failure on the HTTP path.
	ensureSingleListener(agent)

	// 2. add-self-to-agent-registry — identity + capabilities.
	fmt.Fprintf(os.Stderr, "parlay listen: registering '%s' …\n", agent)
	reg := httpc.PostJSON[registerAgentResponse]("/api/chat/register-agent", body)
	if reg.Error != "" {
		httpc.Die(fmt.Sprintf("parlay listen: register-agent failed: %s", reg.Error), config.ExitRuntime)
		return
	}

	// 3. Announce presence on the agent's own channel.
	reply := httpc.PostJSON[listenReplyResponse]("/api/chat/reply", map[string]string{
		"text": "listening — monitor armed, ready for messages.", "agent": agent,
	})
	if reply.Error != "" {
		httpc.Die(fmt.Sprintf("parlay listen: reply failed: %s", reply.Error), config.ExitRuntime)
		return
	}
	fmt.Fprintf(os.Stderr, "parlay listen: announced — arming monitor …\n")

	// 4. Hand off into the poll loop. Reuses runMonitor verbatim — same
	// mechanism as `parlay monitor --agent <id>`, so a harness Monitor{}
	// wakes on CHAT_MSG lines. Never returns on the real path (runRelayMonitor
	// calls os.Exit / runLegacyPoll loops forever).
	// --notify-safe and --legacy-poll are forwarded straight through so the
	// self-enroll path has the same notification-truncation safety `parlay
	// monitor --notify-safe` gives (robots-w9ij): claim-enrolled panel agents
	// arm their monitor via this command, and without the passthrough a long
	// captain message could blow the agent's context on delivery.
	monitorArgs := []string{"--agent", agent}
	if res.Bool("--legacy-poll") {
		monitorArgs = append(monitorArgs, "--legacy-poll")
	}
	if res.Bool("--notify-safe") {
		monitorArgs = append(monitorArgs, "--notify-safe")
	}
	runMonitor(monitorArgs)
}
