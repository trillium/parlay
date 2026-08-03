package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// Agents ports commands.ts's cmdAgents.
func Agents(argv []string) {
	if helpWanted("agents", argv) {
		return
	}
	r := args.Parse("agents", argv, []string{"--full"}, nil)
	agentsList := httpc.GetJSON[[]wire.AgentInfo]("/api/chat/agents")

	if r.Bool("--full") {
		b, _ := json.MarshalIndent(agentsList, "", "  ")
		fmt.Println(string(b))
		fmt.Fprintln(os.Stderr, "\nNext: parlay alert --agent <id> <text...>")
		return
	}

	if len(agentsList) == 0 {
		fmt.Println("0 agents registered.")
	} else {
		fmt.Printf("%d agent(s):\n", len(agentsList))
		for _, a := range agentsList {
			fmt.Printf("%-20s %-20s %s\n", a.ID, a.Name, a.Color)
		}
	}
	format.NextStep("parlay alert --agent <id> <text...>")
}
