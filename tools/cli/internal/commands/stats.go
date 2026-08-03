package commands

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

type statsMsgFields struct {
	Role   string `json:"role"`
	Ts     string `json:"ts"`
	Type   string `json:"type"`
	Images []any  `json:"images"`
}

func statsBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	return fmt.Sprintf("%.1fKB", float64(n)/1024)
}

// statsTimestamp approximates JS's `new Date(ts).toLocaleString()` — an
// exact locale/timezone match isn't achievable from Go stdlib alone, and
// this is a display-only field, so a fixed reasonable format stands in.
func statsTimestamp(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("1/2/2006, 3:04:05 PM")
}

// Stats ports commands.ts's cmdStats. Fetches raw JSON (rather than decoding
// straight into wire.ChatMessage) so the per-message size estimate and the
// images/action_request checks see the same bytes the TS original's
// JSON.stringify(m).length and (msgs as any[]) casts saw, not just the
// fields wire.ChatMessage happens to declare.
func Stats(argv []string) {
	if helpWanted("stats", argv) {
		return
	}

	raw := httpc.GetJSON[[]json.RawMessage]("/api/chat/history?limit=2000")
	agentsList := httpc.GetJSON[[]wire.AgentInfo]("/api/chat/agents")

	if len(raw) == 0 {
		fmt.Println("0 messages.")
		format.NextStep("parlay history 20")
		return
	}

	var total, largest, userN, agentN, imgN, cardN int
	var oldestTs, newestTs string
	for i, r := range raw {
		size := len(r)
		total += size
		if size > largest {
			largest = size
		}
		var m statsMsgFields
		_ = json.Unmarshal(r, &m)
		switch m.Role {
		case "user":
			userN++
		case "agent":
			agentN++
		}
		if len(m.Images) > 0 {
			imgN++
		}
		if m.Type == "action_request" {
			cardN++
		}
		if i == 0 {
			oldestTs = m.Ts
		}
		if i == len(raw)-1 {
			newestTs = m.Ts
		}
	}
	avg := int(math.Round(float64(total) / float64(len(raw))))

	fmt.Printf("messages: %d  |  est. %s  |  avg %s  |  largest %s\n", len(raw), statsBytes(total), statsBytes(avg), statsBytes(largest))
	fmt.Printf("  user: %d  agent: %d  |  images: %d  action_cards: %d\n", userN, agentN, imgN, cardN)
	fmt.Printf("  oldest: %s\n", statsTimestamp(oldestTs))
	fmt.Printf("  newest: %s\n", statsTimestamp(newestTs))
	fmt.Printf("agents: %d registered\n", len(agentsList))
	format.NextStep("Ctrl+Shift+D in the panel for client-side bundle/memory breakdown")
}
