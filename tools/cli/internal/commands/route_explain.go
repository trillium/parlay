// parlay route explain / rules — the observability half of #128's routing
// requirement: it must be possible to ask why a message routed the way it
// did, and whether the answer came from a rule or an inference. explain
// answers for one recorded decision; rules answers for the table itself.
package commands

import (
	"fmt"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/routing"
)

// routeExplainRun gathers one ledger event plus everything that references
// it: proposals recorded for it and the feedback that hardened or corrected
// it — the complete story of one decision, straight off the append-only
// ledger.
func routeExplainRun(st *routing.Store, id string) (routing.Event, []routing.Event, error) {
	ev, ok, err := st.FindEvent(id)
	if err != nil {
		return routing.Event{}, nil, err
	}
	if !ok {
		return routing.Event{}, nil, fmt.Errorf("no event %q in the ledger (ids come from 'route decide' / 'route propose')", id)
	}
	events, err := st.Events()
	if err != nil {
		return routing.Event{}, nil, err
	}
	var related []routing.Event
	for _, e := range events {
		if e.Decision == id {
			related = append(related, e)
		}
	}
	return ev, related, nil
}

// routeAnsweredBy renders the rule-or-inference line #128 asks for.
func routeAnsweredBy(res routing.Result) string {
	switch res.Basis {
	case routing.BasisRule:
		return fmt.Sprintf("a deterministic rule (%s) — no inference involved", res.Source)
	case routing.BasisHardened:
		return fmt.Sprintf("learned evidence for signal %q, hardened from captain feedback — deterministic at decision time", res.Source)
	case routing.BasisInference:
		return "an external inference proposal — not a rule"
	case routing.BasisNone:
		return "nothing — no deterministic answer existed at decision time"
	default:
		return string(res.Basis)
	}
}

func routeExplain(argv []string) {
	r := args.Parse("route explain", argv, []string{"--json"}, nil)
	if len(r.Positionals) != 1 {
		httpc.Die("parlay route explain: usage: route explain <ledger-id>", config.ExitUsage)
		return
	}
	st := routeStore()
	ev, related, err := routeExplainRun(st, r.Positionals[0])
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route explain: %v", err), config.ExitRuntime)
		return
	}
	if r.Bool("--json") {
		printRouteJSON(struct {
			Event   routing.Event   `json:"event"`
			Related []routing.Event `json:"related,omitempty"`
		}{ev, related})
		return
	}
	fmt.Println(renderRouteExplain(ev, related))
}

func renderRouteExplain(ev routing.Event, related []routing.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s · %s\n", ev.Kind, ev.ID, ev.Time)
	if ev.Input != "" {
		fmt.Fprintf(&b, "input: %q\n", ev.Input)
	}
	if ev.Decision != "" {
		fmt.Fprintf(&b, "for decision: %s\n", ev.Decision)
	}
	if ev.Result != nil {
		fmt.Fprintf(&b, "%s\n", renderRouteResult(*ev.Result))
		fmt.Fprintf(&b, "answered by: %s\n", routeAnsweredBy(*ev.Result))
		if len(ev.Result.Trace) > 0 {
			fmt.Fprintf(&b, "trace:\n")
			for i, step := range ev.Result.Trace {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, step)
			}
		}
	}
	if len(related) > 0 {
		fmt.Fprintf(&b, "follow-ups on this event:\n")
		for _, e := range related {
			switch e.Kind {
			case routing.EventProposal:
				out := "?"
				if e.Result != nil {
					out = fmt.Sprintf("%s → %s at %.3f", e.Result.Outcome, e.Result.Target, e.Result.Confidence)
				}
				fmt.Fprintf(&b, "  %s %s (%s): %s\n", e.Kind, e.ID, e.Time, out)
			case routing.EventConfirm:
				fmt.Fprintf(&b, "  %s %s (%s): %s confirmed → %s [evidence %s]\n", e.Kind, e.ID, e.Time, e.Authority, e.Target, e.Entry)
			case routing.EventCorrect:
				fmt.Fprintf(&b, "  %s %s (%s): %s corrected to → %s [evidence %s]\n", e.Kind, e.ID, e.Time, e.Authority, e.Target, e.Entry)
			default:
				fmt.Fprintf(&b, "  %s %s (%s)\n", e.Kind, e.ID, e.Time)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func routeRules(argv []string) {
	r := args.Parse("route rules", argv, []string{"--json"}, nil)
	if len(r.Positionals) > 0 {
		httpc.Die("parlay route rules: takes no arguments (--json for machine-readable)", config.ExitUsage)
		return
	}
	st := routeStore()
	rs, err := st.LoadRuleset()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route rules: %v", err), config.ExitRuntime)
		return
	}
	pol, err := st.LoadPolicy()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route rules: %v", err), config.ExitRuntime)
		return
	}
	if r.Bool("--json") {
		printRouteJSON(struct {
			Policy  routing.Policy  `json:"policy"`
			Ruleset routing.Ruleset `json:"ruleset"`
		}{pol, rs})
		return
	}
	fmt.Println(renderRouteRules(rs, pol, st.Dir()))
}

func renderRouteRules(rs routing.Ruleset, pol routing.Policy, dir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "policy: act ≥ %.2f · refuse < %.2f (%s)\n", pol.ActThreshold, pol.RefuseThreshold, dir)
	fmt.Fprintf(&b, "authored rules (%d):\n", len(rs.Rules))
	if len(rs.Rules) == 0 {
		fmt.Fprintf(&b, "  none — 'route rule add --key <k> --target <t>' authors one\n")
	}
	for _, rule := range rs.Rules {
		fmt.Fprintf(&b, "  %s: %q → %s%s", rule.ID, rule.Key, rule.Target, retiredTag(rule.Retired))
		if rule.Note != "" {
			fmt.Fprintf(&b, " · note: %s", rule.Note)
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "learned evidence (%d):\n", len(rs.Learned))
	if len(rs.Learned) == 0 {
		fmt.Fprintf(&b, "  none — captain confirms/corrects on decisions teach these\n")
	}
	for _, e := range rs.Learned {
		fmt.Fprintf(&b, "  %s%s\n", renderEvidenceLine(e, pol), retiredTag(e.Retired))
		if len(e.Provenance) > 0 {
			fmt.Fprintf(&b, "    provenance: %s\n", strings.Join(e.Provenance, ", "))
		}
		if e.Note != "" {
			fmt.Fprintf(&b, "    note: %s\n", e.Note)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func retiredTag(retired bool) string {
	if retired {
		return " [RETIRED — never matches, history preserved]"
	}
	return ""
}
