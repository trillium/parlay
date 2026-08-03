package commands

import (
	"fmt"
	"strconv"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// History ports commands.ts's cmdHistory.
func History(argv []string) {
	if helpWanted("history", argv) {
		return
	}
	r := args.Parse("history", argv, []string{"--full"}, nil)

	n := 20
	if len(r.Positionals) > 0 {
		v, err := strconv.Atoi(r.Positionals[0])
		if err != nil || v <= 0 {
			httpc.Die("parlay history: N must be a positive number", config.ExitUsage)
			return
		}
		n = v
	}
	full := r.Bool("--full")

	msgs := httpc.GetJSON[[]wire.ChatMessage](fmt.Sprintf("/api/chat/history?limit=%d", n))
	if len(msgs) == 0 {
		fmt.Println("0 messages.")
	} else {
		for _, m := range msgs {
			fmt.Println(format.FmtMsg(m, full))
		}
		if !full {
			fmt.Printf("(%d message(s), text truncated at %d chars — use --full)\n", len(msgs), config.TruncateAt)
		}
	}
	format.NextStep("parlay send <text...>")
}
