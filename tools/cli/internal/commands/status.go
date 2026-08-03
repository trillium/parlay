package commands

import (
	"fmt"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// Status is the bare `parlay` panel/fleet snapshot: subscribers, agents, and
// the last 3 messages. Ported from commands.ts's cmdStatus — not to be
// confused with `parlay status <verb>` (the fold §3.6 keyed status verb,
// a separate command owned by a different ticket).
func Status() {
	subs := httpc.GetJSON[wire.SubscribersInfo]("/api/chat/subscribers")
	agents := httpc.GetJSON[[]wire.AgentInfo]("/api/chat/agents")
	recent := httpc.GetJSON[[]wire.ChatMessage]("/api/chat/history?limit=3")

	clients := 0
	if subs.Parlay != nil {
		clients = subs.Parlay.Clients
	}
	pollers := 0
	if subs.Poll != nil {
		pollers = subs.Poll.Count
	}

	fmt.Printf("parlay @ %s\n", config.ServerURL())
	fmt.Printf("subscribers: %d panel client(s), %d poller(s)\n", clients, pollers)

	if len(agents) == 0 {
		fmt.Println("agents: 0 registered")
	} else {
		ids := make([]string, len(agents))
		for i, a := range agents {
			ids[i] = a.ID
		}
		fmt.Printf("agents (%d): %s\n", len(agents), strings.Join(ids, ", "))
	}

	if len(recent) == 0 {
		fmt.Println("messages: 0 messages")
	} else {
		fmt.Printf("last %d message(s):\n", len(recent))
		for _, m := range recent {
			fmt.Printf("  %s\n", format.FmtMsg(m, false))
		}
	}

	format.NextStep("parlay history 20")
}
