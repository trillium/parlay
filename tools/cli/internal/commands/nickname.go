package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

type registerAgentResponse struct {
	OK        bool     `json:"ok,omitempty"`
	ID        string   `json:"id,omitempty"`
	Nicknames []string `json:"nicknames,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// Nickname ports commands-nickname.ts's cmdNickname: metadata-only upsert of
// voice nicknames via POST /api/chat/register-agent.
func Nickname(argv []string) {
	if helpWanted("nickname", argv) {
		return
	}
	r := args.Parse("nickname", argv, []string{"--clear"}, []string{"--agent"})

	id := ""
	if v, ok := r.String("--agent"); ok {
		id = strings.TrimSpace(v)
	}
	if id == "" {
		id = strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
	}
	if id == "" {
		httpc.Die("parlay nickname: no agent — pass --agent <id> or set PARLAY_AGENT_ID", config.ExitUsage)
		return
	}

	clear := r.Bool("--clear")
	nicknames := []string{}
	if !clear {
		for _, p := range r.Positionals {
			p = strings.TrimSpace(p)
			if p != "" {
				nicknames = append(nicknames, p)
			}
		}
	}
	if !clear && len(nicknames) == 0 {
		httpc.Die("parlay nickname: give at least one nickname (or --clear)", config.ExitUsage)
		return
	}

	resp := httpc.PostJSON[registerAgentResponse]("/api/chat/register-agent", map[string]any{
		"id":        id,
		"nicknames": nicknames,
	})
	if resp.Error != "" {
		httpc.Die(fmt.Sprintf("parlay nickname: %s", resp.Error), config.ExitRuntime)
		return
	}

	if clear {
		fmt.Printf("cleared nicknames for %s\n", id)
		return
	}
	display := resp.Nicknames
	if len(display) == 0 {
		display = nicknames
	}
	fmt.Printf("%s nicknames: %s\n", id, strings.Join(display, ", "))
}
