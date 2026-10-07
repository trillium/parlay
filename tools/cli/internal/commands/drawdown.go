// `parlay drawdown [N]` — generate a boilerplate handoff prompt from the
// last N chat messages, ready to paste into `handoff create`. Useful when
// context is running low and the agent needs to hand off state to a fresh
// session.
//
// Ported from packages/cli/src/commands/drawdown.ts (ticket B9).
package commands

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/resolvehandoff"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

var drawdownNewlinesRe = regexp.MustCompile(`\n+`)

// jsSlice300 mirrors JS `.slice(0, 300)` on UTF-16 code units — same
// encode/clip/decode approach as format.Truncate, minus its "(+N chars)"
// suffix, which drawdown.ts's summary line doesn't add.
func jsSlice300(s string) string {
	units := utf16.Encode([]rune(s))
	if len(units) > 300 {
		units = units[:300]
	}
	return string(utf16.Decode(units))
}

// Drawdown ports cmdDrawdown.
func Drawdown(argv []string) {
	if helpWanted("drawdown", argv) {
		return
	}
	r := args.Parse("drawdown", argv, nil, nil)

	n := 20.0
	if len(r.Positionals) > 0 {
		parsed, err := strconv.ParseFloat(r.Positionals[0], 64)
		if err != nil {
			parsed = math.NaN()
		}
		n = parsed
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
		httpc.Die("parlay drawdown: N must be a positive number", config.ExitUsage)
		return
	}
	nStr := formatGrace(n) // JS-template-literal number stringification (see guard.go)

	msgs := httpc.GetJSON[[]wire.ChatMessage](fmt.Sprintf("/api/chat/history?limit=%s", nStr))
	agentID := strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
	if agentID == "" {
		agentID = "<agent-id>"
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05") + "Z"

	summary := fmt.Sprintf("(no agent messages in last %s)", nStr)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "agent" {
			summary = drawdownNewlinesRe.ReplaceAllString(jsSlice300(msgs[i].Text), " ")
			break
		}
	}

	body := "(no messages)"
	if len(msgs) > 0 {
		lines := make([]string, len(msgs))
		for i, m := range msgs {
			lines[i] = format.FmtMsg(m, false)
		}
		body = strings.Join(lines, "\n")
	}

	// The closing recipe names `handoff create`, which only exists where the
	// author's federation store wrappers are installed. On a plain clone
	// (store absent) print the paste-anywhere alternative instead: the
	// handoff body is the portable artifact, and `identity --submit <id>`
	// works with any id once one exists.
	tail := fmt.Sprintf("---\nTo submit this handoff:\n  handoff create \"%s context handoff %s\" --description \"<paste body above>\"\n  identity --submit\n",
		agentID, now)
	if !resolvehandoff.StoreAvailable("") {
		tail = fmt.Sprintf("---\nNo `handoff` store is installed here (it ships with the author's federation, not with parlay),\n" +
			"so this body has no automatic submit step. Keep it wherever you keep notes, and when a\n" +
			"handoff id does exist, submit it with:  identity --submit <handoff-id>\n")
	}

	fmt.Printf("## Handoff — %s\n\n### What I was doing\n%s\n\n### Recent context (last %d message(s))\n```\n%s\n```\n\n### Next steps\n[fill in before submitting — what should the next session pick up?]\n\n%s",
		now, summary, len(msgs), body, tail)
	format.NextStep("identity --submit")
}
