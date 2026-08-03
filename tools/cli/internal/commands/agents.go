package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// Agents ports commands.ts's cmdAgents. Fetches raw JSON (rather than
// decoding straight into []wire.AgentInfo) so --full prints the same bytes
// the server sent, not just the fields wire.AgentInfo happens to declare —
// matches TS's console.log(JSON.stringify(agents, null, 2)).
func Agents(argv []string) {
	if helpWanted("agents", argv) {
		return
	}
	r := args.Parse("agents", argv, []string{"--full"}, nil)
	raw := httpc.GetJSON[json.RawMessage]("/api/chat/agents")

	if r.Bool("--full") {
		var buf bytes.Buffer
		json.Indent(&buf, raw, "", "  ")
		fmt.Println(buf.String())
		fmt.Fprintln(os.Stderr, "\nNext: parlay alert --agent <id> <text...>")
		return
	}

	var agentsList []wire.AgentInfo
	_ = json.Unmarshal(raw, &agentsList)

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
