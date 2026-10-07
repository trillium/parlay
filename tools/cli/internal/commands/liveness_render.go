// Rendering for `parlay liveness`: one table plus the notes that explain it,
// and the --json envelope. Kept beside (not inside) liveness.go so each file
// stays narrow enough to read in one screen.
package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/liveness"
)

// Column widths. Fixed rather than computed: a column that widens to fit one
// long agent id makes every other row unreadable, and the id is the one field
// an operator copies out.
const (
	livenessAgentCol     = 20
	livenessStateCol     = 9
	livenessHeartbeatCol = 22
	livenessSilenceCol   = 10
)

// renderLiveness prints the fleet view in a fixed order: the context (where
// the answers came from), every source with what it did or did not answer, the
// table, the notes for the rows that need one, and the summary line.
func renderLiveness(g livenessGather, a livenessArgs) {
	if a.asJSON {
		renderLivenessJSON(g, a)
		return
	}
	fmt.Printf("parlay liveness — %d agent(s) in the fleet · silence window %s\n", len(g.Rows), liveness.Short(g.Window))
	fmt.Printf("server          %s\n", g.Server)
	fmt.Printf("relay runtime   %s\n", g.Runtime)
	fmt.Println("sources")
	for _, s := range g.Sources {
		fmt.Printf("  %s %s — %s\n", format.PadEnd(s.Name, 22), s.State, s.Detail)
	}

	shown := make([]livenessRow, 0, len(g.Rows))
	for _, r := range g.Rows {
		if livenessWants(r.Verdict, a.silentOnly) {
			shown = append(shown, r)
		}
	}
	if len(shown) == 0 {
		fmt.Println()
		if a.silentOnly {
			fmt.Printf("no agent is silent beyond %s, and none is missing a heartbeat record\n", liveness.Short(g.Window))
			return
		}
		fmt.Println("no agents to report — the server lists none and this host has no agent homes")
		return
	}

	fmt.Println()
	fmt.Printf("%s %s %s %s %s\n",
		format.PadEnd("AGENT", livenessAgentCol),
		format.PadEnd("STATE", livenessStateCol),
		format.PadEnd("HEARTBEAT", livenessHeartbeatCol),
		format.PadEnd("SILENT", livenessSilenceCol),
		"LAST OBSERVED ACTIVITY")
	for _, r := range shown {
		v := r.Verdict
		fmt.Printf("%s %s %s %s %s\n",
			format.PadEnd(r.Agent, livenessAgentCol),
			format.PadEnd(v.State, livenessStateCol),
			format.PadEnd(heartbeatCell(v), livenessHeartbeatCol),
			format.PadEnd(silenceCell(v), livenessSilenceCol),
			lastCell(v))
	}

	if notes := livenessNotes(shown, g.Window); len(notes) > 0 {
		fmt.Println()
		fmt.Println("notes")
		for _, n := range notes {
			fmt.Printf("  %s %s %s\n", format.PadEnd(n.agent, livenessAgentCol), format.PadEnd(n.aspect, 10), n.text)
		}
	}

	fmt.Println()
	silent, absent := livenessCounts(g.Rows)
	fmt.Printf("%d of %d silent beyond %s · %d with no heartbeat record (never observed or no presence row)\n",
		silent, len(g.Rows), liveness.Short(g.Window), absent)
	fmt.Println("next  parlay explain <agent-id> for one agent's whole story · parlay timeline --agent <id> for its history")
}

// heartbeatCell is the server's own channel record, in its own vocabulary.
// "never observed" and "no row" are printed as themselves so an absent
// heartbeat is never read as an expired one.
func heartbeatCell(v liveness.Verdict) string {
	switch v.Heartbeat {
	case liveness.HeartbeatFresh:
		return "fresh (" + liveness.Short(v.HeartbeatFor) + " ago)"
	case liveness.HeartbeatExpired:
		return "expired (" + liveness.Short(v.HeartbeatFor) + " ago)"
	case liveness.HeartbeatNeverObserved:
		return "never observed"
	case liveness.HeartbeatNoRow:
		return "no row"
	default:
		return "unknown"
	}
}

// silenceCell is the measured answer over every dated record. "unknown" means
// no dated record exists — never "0", and never "healthy".
func silenceCell(v liveness.Verdict) string {
	switch v.Silence {
	case liveness.SilenceFresh:
		return "no"
	case liveness.SilenceExpired:
		return liveness.Short(v.SilentFor)
	default:
		return "unknown"
	}
}

func lastCell(v liveness.Verdict) string {
	if !v.LastKnown {
		return "no dated record"
	}
	return v.LastDetail + " " + liveness.Short(v.LastFor) + " ago"
}

// livenessNote is one line of the notes section: which agent, which aspect of
// its verdict, and the sentence that explains it.
type livenessNote struct {
	agent, aspect, text string
}

// livenessNotes explains the rows that need it, and only those: a fresh, live
// agent with a fresh heartbeat gets no note, because a notes section that
// restates every row is a section an operator stops reading.
func livenessNotes(rows []livenessRow, window time.Duration) []livenessNote {
	var out []livenessNote
	add := func(agent, aspect, text string) {
		if strings.TrimSpace(text) != "" {
			out = append(out, livenessNote{agent: agent, aspect: aspect, text: text})
		}
	}
	for _, r := range rows {
		v := r.Verdict
		if v.StateNote != "" {
			add(r.Agent, "state", v.StateNote)
		}
		switch v.Heartbeat {
		case liveness.HeartbeatExpired, liveness.HeartbeatNeverObserved, liveness.HeartbeatNoRow, liveness.HeartbeatUnknown:
			add(r.Agent, "heartbeat", v.HeartbeatNote)
		}
		if v.Silence == liveness.SilenceUnknown {
			add(r.Agent, "silence", v.SilenceNote)
		}
		if v.Silence == liveness.SilenceExpired && !r.LocalHome && v.State != liveness.StateLive {
			add(r.Agent, "home", "no agent home for this id on this host, so its status file could not be consulted either — run this where the agent runs to see local activity")
		}
	}
	return out
}

// livenessCounts is the summary pair: measurably silent agents, and agents
// with no heartbeat record at all. They are different facts and are counted
// separately.
func livenessCounts(rows []livenessRow) (silent, absent int) {
	for _, r := range rows {
		if r.Verdict.Silence == liveness.SilenceExpired {
			silent++
		}
		if r.Verdict.Heartbeat == liveness.HeartbeatNeverObserved || r.Verdict.Heartbeat == liveness.HeartbeatNoRow {
			absent++
		}
	}
	return silent, absent
}
