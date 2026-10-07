// parlay liveness — the fleet-wide answer to "which agents are silent, since
// when, and is the heartbeat absent or merely expired?".
//
// `parlay explain <id>` answers that for ONE agent. Nothing answered it for
// the fleet: `parlay launch` reports live/ghost/offline but has no clock and
// no notion of last activity, and the server's presence snapshot — the only
// per-channel activity record in the fleet — was reachable only through two
// other verbs that each used one field of it. So an operator asking "who has
// gone quiet" read `parlay launch`, then `parlay subscribers --full`, then
// each agent's status file, and interleaved three shapes by hand.
//
// This verb does those reads once and classifies each agent through
// internal/liveness, which keeps the four answers the fleet already gives
// apart: registration (a row nothing removes when a listener dies), the
// process table (a probe that can fail), the server's presence row (a stamp
// can expire; an empty stamp and an absent row CANNOT — they are absence), and
// activity in records the server never sees (the agent's status file, the
// relay's delivery trail). Silence is measured over all of them, because an
// agent that is working without talking has a stale channel stamp and a fresh
// status file.
//
// Read-only, like every diagnostic here: GET routes on the server and the
// relay's read-only control socket, plus local files. It never sends, enrolls,
// retires, or writes. The relay client exposes GET routes only
// (internal/relayctl), so there is no mutation path to reach even by mistake.
//
// Go-only, no TS port (same as explain/timeline/stale): the TS CLI and the
// parity harness that diffed against it were retired in T-08.
package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
	"github.com/trillium/parlay/tools/cli/internal/liveness"
)

// ExitLivenessNothing is the one non-zero outcome: not a verdict about the
// fleet, but the admission that no source answered, so there is nothing to
// classify. Any single source answering (even with bad news) exits 0.
const ExitLivenessNothing = config.ExitRuntime

// livenessRow is one agent's line: the verdict plus the local facts the notes
// section needs to explain it.
type livenessRow struct {
	Agent     string
	Verdict   liveness.Verdict
	LocalHome bool
	// RelayNote, when set, explains a relay-process lifecycle row on this
	// channel that was deliberately NOT counted as the agent's activity (see
	// relayProcessEvent). Empty for every ordinary row.
	RelayNote string
	// rank orders the table by how much attention the row deserves; see
	// attentionRank.
	rank int
}

// livenessGather is everything the renderer is allowed to know, so each
// degraded mode is testable without standing up a fleet.
type livenessGather struct {
	Server  string
	Runtime string
	Window  time.Duration
	Now     time.Time

	Rows    []livenessRow
	Sources []sourceNote

	// answered is the exit-code predicate: did ANY source answer? A source
	// that answered with nothing counts; a source that could not be reached
	// does not.
	answered bool
}

// livenessArgs is the parsed, validated command line.
type livenessArgs struct {
	agent      string
	window     time.Duration
	silentOnly bool
	asJSON     bool
}

// Liveness implements `parlay liveness [<agent-id>] [flags]`.
func Liveness(argv []string) {
	if helpWanted("liveness", argv) {
		return
	}
	r := args.Parse("liveness", argv, []string{"--silent", "--json"}, []string{"--agent", "--silent-for"})

	a := livenessArgs{
		window:     liveness.DefaultWindow,
		silentOnly: r.Bool("--silent"),
		asJSON:     r.Bool("--json"),
	}
	if len(r.Positionals) > 1 {
		httpc.Die("parlay liveness: at most one agent id (e.g. 'parlay liveness crew-1')", config.ExitUsage)
		return
	}
	if len(r.Positionals) == 1 {
		a.agent = strings.TrimSpace(r.Positionals[0])
	}
	if v, ok := r.String("--agent"); ok {
		flagID := strings.TrimSpace(v)
		if a.agent != "" && a.agent != flagID {
			httpc.Die("parlay liveness: the agent id was given twice, as a positional and as --agent, and they differ", config.ExitUsage)
			return
		}
		a.agent = flagID
	}
	if v, ok := r.String("--silent-for"); ok {
		d, ok := parseAgo(strings.TrimSpace(v))
		if !ok || d <= 0 {
			httpc.Die("parlay liveness: --silent-for needs a duration like 90s/45m/2h/3d", config.ExitUsage)
			return
		}
		a.window = d
	}

	g := gatherLiveness(a)
	renderLiveness(g, a)
	if !g.answered {
		fmt.Fprintf(os.Stderr,
			"parlay liveness: nothing was observable — the server did not answer at %s and no relay trail or agent home exists under %s\n",
			g.Server, identity.AgentsRoot())
		httpc.Exit(ExitLivenessNothing)
	}
}

// attentionRank orders the table so the rows an operator must look at come
// first. It is deliberately about RECORDED state, not about alarm: an agent
// whose silence cannot be measured ranks below one that is measurably silent,
// because only the second is a positive finding.
func attentionRank(v liveness.Verdict) int {
	switch {
	case v.Silence == liveness.SilenceExpired:
		return 0
	case v.Heartbeat == liveness.HeartbeatNeverObserved || v.Heartbeat == liveness.HeartbeatNoRow:
		return 1
	case v.Silence == liveness.SilenceUnknown:
		return 2
	case v.State == liveness.StateGhost:
		return 3
	case v.State == liveness.StateUnknown || v.State == liveness.StateOffline:
		return 4
	default:
		return 5
	}
}

// sortRows is (attention, then how long, then id): the most-silent agent first,
// and a stable alphabetical tail so two runs over unchanged records print the
// same table.
func sortRows(rows []livenessRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.Verdict.SilentFor != b.Verdict.SilentFor {
			return a.Verdict.SilentFor > b.Verdict.SilentFor
		}
		return a.Agent < b.Agent
	})
}

// livenessWants reports whether a row survives --silent: the agents whose
// recorded activity is older than the window, plus the ones whose channel has
// no heartbeat record at all (never observed, or no presence row). Both are
// "not answering" in the only two ways the records can say it.
func livenessWants(v liveness.Verdict, silentOnly bool) bool {
	if !silentOnly {
		return true
	}
	return v.Silence == liveness.SilenceExpired ||
		v.Heartbeat == liveness.HeartbeatNeverObserved ||
		v.Heartbeat == liveness.HeartbeatNoRow
}
