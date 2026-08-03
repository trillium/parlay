package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

func subChannels(data wire.SubscribersInfo) []string {
	if data.Poll == nil || len(data.Poll.Channels) == 0 {
		return nil
	}
	out := make([]string, len(data.Poll.Channels))
	for i, c := range data.Poll.Channels {
		if c.Channel == "" {
			out[i] = "(global)"
		} else {
			out[i] = c.Channel
		}
	}
	return out
}

// Subscribers ports commands.ts's cmdSubscribers.
func Subscribers(argv []string) {
	if helpWanted("subscribers", argv) {
		return
	}
	r := args.Parse("subscribers", argv, []string{"--full"}, nil)
	data := httpc.GetJSON[wire.SubscribersInfo]("/api/chat/subscribers")

	if r.Bool("--full") {
		b, _ := json.MarshalIndent(data, "", "  ")
		fmt.Println(string(b))
		fmt.Fprintln(os.Stderr, "\nNext: parlay agents")
		return
	}

	channels := strings.Join(subChannels(data), ", ")
	clients, pollers, registered := 0, 0, 0
	if data.Parlay != nil {
		clients = data.Parlay.Clients
	}
	if data.Poll != nil {
		pollers = data.Poll.Count
	}
	if data.Registered != nil {
		registered = data.Registered.Count
	}

	fmt.Printf("panel clients: %d\n", clients)
	if channels != "" {
		fmt.Printf("pollers: %d (%s)\n", pollers, channels)
	} else {
		fmt.Printf("pollers: %d\n", pollers)
	}
	fmt.Printf("registered agents: %d\n", registered)
	// presence_broadcasts !== undefined in TS; Go's zero value can't
	// distinguish "absent" from "present but 0", so a genuinely-zero count
	// is treated as absent here — a live server's count is never legitimately
	// interesting at exactly 0 in a way this display needs to call out.
	if data.PresenceBroadcasts != 0 {
		fmt.Printf("presence broadcasts: %d\n", data.PresenceBroadcasts)
	}
	format.NextStep("parlay agents")
}
