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

	var agentsList []wire.AgentInfo
	_ = json.Unmarshal(raw, &agentsList)

	// With nobody registered there is nobody to alert — the useful next
	// action is enrolling an agent, not messaging an empty registry.
	next := "parlay alert --agent <id> <text...>"
	if len(agentsList) == 0 {
		next = "parlay listen --agent <id>   (enroll an agent; alias: agent-up)"
	}

	if r.Bool("--full") {
		var buf bytes.Buffer
		json.Indent(&buf, raw, "", "  ")
		fmt.Println(buf.String())
		fmt.Fprintln(os.Stderr, "\nNext: "+next)
		return
	}

	if len(agentsList) == 0 {
		fmt.Println("0 agents registered.")
	} else {
		fmt.Printf("%d agent(s):\n", len(agentsList))
		for _, a := range agentsList {
			fmt.Printf("%s %s %s\n", format.PadEnd(a.ID, 20), format.PadEnd(a.Name, 20), a.Color)
		}
	}
	format.NextStep(next)
}
