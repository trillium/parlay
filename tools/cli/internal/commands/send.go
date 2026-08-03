package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

type sendResponse struct {
	OK    bool   `json:"ok,omitempty"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

// Send ports commands.ts's cmdSend. docs/scope-go-cli.md §5 item 1: the
// target agent is parsed from ANY unrecognized --foo token
// (`send --mayor "msg"` -> target "mayor"), which no generic flag parser
// expresses — this hand-rolls the exact loop from commands.ts rather than
// calling internal/args.Parse.
func Send(argv []string) {
	if helpWanted("send", argv) {
		return
	}

	var target, fromOverride string
	var positionals []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--from":
			i++
			if i < len(argv) {
				fromOverride = argv[i]
			}
		case strings.HasPrefix(a, "--"):
			// Any other unrecognized --flag is the target agent id.
			target = a[2:]
		default:
			positionals = append(positionals, a)
		}
	}

	// No args at all -> list targetable agents.
	if target == "" && len(positionals) == 0 {
		agentsList := httpc.GetJSON[[]wire.AgentInfo]("/api/chat/agents")
		if len(agentsList) == 0 {
			fmt.Println("0 agents registered — no one to send to.")
		} else {
			fmt.Printf("%d agent(s) you can message:\n", len(agentsList))
			for _, a := range agentsList {
				fmt.Printf("  send --%-22s # → %s\n", a.ID, a.Name)
			}
		}
		return
	}

	text := strings.TrimSpace(strings.Join(positionals, " "))
	if text == "" {
		t := target
		if t == "" {
			t = "<agent-id>"
		}
		httpc.Die(fmt.Sprintf("parlay send: message text required (e.g. send --%s \"your message\")", t), config.ExitUsage)
		return
	}
	if target == "" {
		httpc.Die(`parlay send: no target agent — use send --<agent-id> "msg" or bare send to list agents`, config.ExitUsage)
		return
	}

	from := strings.TrimSpace(fromOverride)
	if from == "" {
		from = strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
	}

	body := map[string]any{"text": text, "toAgent": target}
	if from != "" {
		body["from"] = from
	}

	r := httpc.PostJSON[sendResponse]("/api/chat/send", body)
	if r.Error != "" {
		httpc.Die(fmt.Sprintf("send failed: %s", r.Error), config.ExitRuntime)
		return
	}
	fromSuffix := ""
	if from != "" {
		fromSuffix = fmt.Sprintf(" (from %s)", from)
	}
	fmt.Printf("sent to %s%s — id %s\n", target, fromSuffix, r.ID)
	format.NextStep("parlay history 5")
}
