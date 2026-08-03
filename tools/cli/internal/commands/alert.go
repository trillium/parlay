package commands

import (
	"fmt"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

type alertResponse struct {
	OK        bool   `json:"ok,omitempty"`
	Channels  int    `json:"channels,omitempty"`
	Delivered int    `json:"delivered,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Alert ports commands.ts's cmdAlert.
func Alert(argv []string) {
	if helpWanted("alert", argv) {
		return
	}
	r := args.Parse("alert", argv, nil, []string{"--agent"})
	agent, _ := r.String("--agent")
	text := strings.TrimSpace(strings.Join(r.Positionals, " "))
	if text == "" {
		httpc.Die("parlay alert: message text required", config.ExitUsage)
		return
	}

	body := map[string]any{"text": text}
	if agent != "" {
		body["agents"] = []string{agent}
	}

	resp := httpc.PostJSON[alertResponse]("/api/chat/alert", body)
	if resp.Error != "" {
		httpc.Die(fmt.Sprintf("alert failed: %s", resp.Error), config.ExitRuntime)
		return
	}
	fmt.Printf("alert sent to %d channel(s), delivered to %d live poller(s)\n", resp.Channels, resp.Delivered)
	format.NextStep("parlay subscribers")
}
