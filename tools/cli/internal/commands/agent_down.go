package commands

import (
	"fmt"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

type unregisterResponse struct {
	OK bool   `json:"ok,omitempty"`
	ID string `json:"id,omitempty"`
}

// AgentDown ports commands-agent-down.ts's cmdAgentDown: a thin wrapper over
// POST /api/chat/unregister, which already fails loud (non-2xx) on an
// unknown/already-gone id — httpc.PostJSON's Die surfaces that.
func AgentDown(argv []string) {
	if helpWanted("agent-down", argv) {
		return
	}
	r := args.Parse("agent-down", argv, nil, nil)
	agentID := ""
	if len(r.Positionals) > 0 {
		agentID = strings.TrimSpace(r.Positionals[0])
	}
	if agentID == "" {
		httpc.Die("parlay agent-down: agent id required", config.ExitUsage)
		return
	}
	httpc.PostJSON[unregisterResponse]("/api/chat/unregister", map[string]any{"id": agentID})
	fmt.Printf("agent %s deregistered\n", agentID)
}
