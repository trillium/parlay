// `parlay say` / `parlay reply` — reply to YOUR OWN channel. Routes off
// PARLAY_AGENT_ID (`parlay spawn` sets it), so no url/id/name/color/JSON —
// just the text. The server keeps the agent's registered name/color. Text
// comes from args, or stdin when no args are given (so long/multi-line
// replies pipe in).
//
// Ported from packages/cli/src/commands-identity/say.ts.
package identity

import (
	"fmt"
	"os"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/sayguard"
)

type replyResponse struct {
	OK    bool   `json:"ok,omitempty"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

// CmdSay is `parlay say`/`parlay reply`'s entry point.
func CmdSay(argv []string) {
	if help.Wanted("say", argv) {
		return
	}
	res := args.Parse("say", argv, nil, []string{"--agent"})
	agent := strings.TrimSpace(optString(res, "--agent"))
	if agent == "" {
		agent = strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
	}
	if agent == "" {
		httpc.Die("parlay say: no agent identity — run inside a parlay-spawned agent (it sets PARLAY_AGENT_ID) or pass --agent <id>", config.ExitUsage)
		return
	}
	text := strings.TrimSpace(strings.Join(res.Positionals, " "))
	if text == "" && !stdinIsTTY() {
		text = readStdin()
	}
	if text == "" {
		httpc.Die("parlay say: message text required (as arguments or piped on stdin)", config.ExitUsage)
		return
	}
	// Loud (stderr) warning if this send lands inside an unsubmitted handoff window.
	sayguard.WarnIfUnsubmittedHandoff(agent)
	r := httpc.PostJSON[replyResponse]("/api/chat/reply", map[string]string{"text": text, "agent": agent})
	if r.Error != "" {
		httpc.Die(fmt.Sprintf("say failed: %s", r.Error), config.ExitRuntime)
		return
	}
	fmt.Printf("said as %s (id %s)\n", agent, r.ID)
}
