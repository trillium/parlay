// parlay route — the deterministic-first routing verb (#128 §34–§37; model
// and rationale in docs/routing.md, engine in internal/routing).
//
// Unit 2 surface: decide (evaluate + record), why (dry-run trace), propose
// (record an external inference result for a needs-inference decision).
// Feedback (confirm/correct) and rule management land with the hardening
// unit; the ledger and store they need are already written here.
//
// Exit codes are semantic, following the merge-gate precedent: 0 act,
// 3 confirm-required, 4 refused, 5 needs-inference, 1 runtime, 2 usage —
// so a caller branching on non-zero fails closed.
package commands

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/routing"
)

// Semantic exits for route decide/propose. 3 and 4 deliberately mirror
// nothing else: 0 is the ONLY "go ahead" — every other code is a stop.
const (
	routeExitConfirm        = 3
	routeExitRefuse         = 4
	routeExitNeedsInference = 5
)

func routeStore() *routing.Store {
	return routing.NewStore(filepath.Join(config.StateHome(), "routing"))
}

func routeNow() string { return time.Now().UTC().Format(time.RFC3339) }

// Route is `parlay route`'s entry point.
func Route(argv []string) {
	if helpWanted("route", argv) {
		return
	}
	sub := ""
	rest := []string{}
	if len(argv) > 0 {
		sub, rest = argv[0], argv[1:]
	}
	switch sub {
	case "decide":
		routeDecide(rest)
	case "why":
		routeWhy(rest)
	case "propose":
		routePropose(rest)
	default:
		httpc.Die("parlay route: subcommand required: decide | why | propose (see 'parlay route --help')", config.ExitUsage)
	}
}

// routeExitFor maps an outcome to its semantic exit code.
func routeExitFor(o routing.Outcome) int {
	switch o {
	case routing.OutcomeAct:
		return config.ExitOK
	case routing.OutcomeConfirm:
		return routeExitConfirm
	case routing.OutcomeRefuse:
		return routeExitRefuse
	case routing.OutcomeNeedsInference:
		return routeExitNeedsInference
	default:
		// An outcome this map does not know is a bug, not a routable answer.
		return config.ExitRuntime
	}
}

// splitTargets parses a --targets value: comma-separated, blanks dropped.
func splitTargets(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// routeDecideRun is the recorded evaluation: load, evaluate, append to the
// ledger. Factored so tests exercise it without os.Exit.
func routeDecideRun(st *routing.Store, input string, roster []string, now string) (routing.Event, error) {
	pol, err := st.LoadPolicy()
	if err != nil {
		return routing.Event{}, err
	}
	rs, err := st.LoadRuleset()
	if err != nil {
		return routing.Event{}, err
	}
	res := routing.Evaluate(input, roster, rs, pol)
	ev := routing.Event{
		ID:     routing.NewEventID(),
		Kind:   routing.EventDecision,
		Time:   now,
		Input:  input,
		Result: &res,
	}
	if err := st.AppendEvent(ev); err != nil {
		return routing.Event{}, err
	}
	return ev, nil
}

func routeDecide(argv []string) {
	r := args.Parse("route decide", argv, []string{"--json"}, []string{"--targets"})
	input := strings.TrimSpace(strings.Join(r.Positionals, " "))
	if input == "" {
		httpc.Die(`parlay route decide: need input text, e.g. route decide "parlay, auth is broken" [--targets a,b]`, config.ExitUsage)
		return
	}
	targets, _ := r.String("--targets")
	ev, err := routeDecideRun(routeStore(), input, splitTargets(targets), routeNow())
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route decide: %v", err), config.ExitRuntime)
		return
	}
	if r.Bool("--json") {
		printRouteJSON(ev)
	} else {
		fmt.Println(renderRouteEvent(ev))
	}
	httpc.Exit(routeExitFor(ev.Result.Outcome))
}

func routeWhy(argv []string) {
	r := args.Parse("route why", argv, []string{"--json"}, []string{"--targets"})
	input := strings.TrimSpace(strings.Join(r.Positionals, " "))
	if input == "" {
		httpc.Die(`parlay route why: need input text, e.g. route why "parlay, auth is broken" [--targets a,b]`, config.ExitUsage)
		return
	}
	st := routeStore()
	pol, err := st.LoadPolicy()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route why: %v", err), config.ExitRuntime)
		return
	}
	rs, err := st.LoadRuleset()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route why: %v", err), config.ExitRuntime)
		return
	}
	targets, _ := r.String("--targets")
	res := routing.Evaluate(input, splitTargets(targets), rs, pol)
	if r.Bool("--json") {
		printRouteJSON(res)
		return
	}
	fmt.Println(renderRouteResult(res))
	fmt.Println("trace:")
	for i, step := range res.Trace {
		fmt.Printf("  %d. %s\n", i+1, step)
	}
	fmt.Println("(dry run — nothing recorded; 'route decide' records)")
}

// routeProposeRun records an external inference result against a recorded
// needs-inference decision. It refuses to attach a proposal to anything
// else: inference is an escalation for inputs the deterministic layer could
// not answer, never an override of an answer it gave (#128 §36).
func routeProposeRun(st *routing.Store, decisionID, target string, confidence float64, now string) (routing.Event, error) {
	pol, err := st.LoadPolicy()
	if err != nil {
		return routing.Event{}, err
	}
	dec, ok, err := st.FindEvent(decisionID)
	if err != nil {
		return routing.Event{}, err
	}
	if !ok {
		return routing.Event{}, fmt.Errorf("no decision %q in the ledger (ids come from 'route decide')", decisionID)
	}
	if dec.Kind != routing.EventDecision || dec.Result == nil {
		return routing.Event{}, fmt.Errorf("%s is a %s event, not a decision", decisionID, dec.Kind)
	}
	if dec.Result.Outcome != routing.OutcomeNeedsInference {
		return routing.Event{}, fmt.Errorf("decision %s did not need inference (outcome %s) — a deterministic answer is never overridden by a proposal", decisionID, dec.Result.Outcome)
	}
	res := routing.ClassifyInference(target, confidence, dec.Result.Signal, pol)
	ev := routing.Event{
		ID:       routing.NewEventID(),
		Kind:     routing.EventProposal,
		Time:     now,
		Decision: decisionID,
		Result:   &res,
	}
	if err := st.AppendEvent(ev); err != nil {
		return routing.Event{}, err
	}
	return ev, nil
}

func routePropose(argv []string) {
	r := args.Parse("route propose", argv, []string{"--json"}, []string{"--decision", "--target", "--confidence"})
	decisionID, haveDecision := r.String("--decision")
	target, haveTarget := r.String("--target")
	confStr, haveConf := r.String("--confidence")
	if !haveDecision || !haveTarget || !haveConf || len(r.Positionals) > 0 {
		httpc.Die("parlay route propose: usage: route propose --decision <id> --target <t> --confidence <0..1>", config.ExitUsage)
		return
	}
	conf, err := strconv.ParseFloat(confStr, 64)
	if err != nil || conf != conf || conf < 0 || conf > 1 {
		httpc.Die(fmt.Sprintf("parlay route propose: --confidence %q is not a number in [0,1]", confStr), config.ExitUsage)
		return
	}
	ev, err := routeProposeRun(routeStore(), decisionID, target, conf, routeNow())
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route propose: %v", err), config.ExitRuntime)
		return
	}
	if r.Bool("--json") {
		printRouteJSON(ev)
	} else {
		fmt.Println(renderRouteEvent(ev))
	}
	httpc.Exit(routeExitFor(ev.Result.Outcome))
}

// renderRouteResult renders one evaluation's headline lines.
func renderRouteResult(res routing.Result) string {
	var b strings.Builder
	switch res.Outcome {
	case routing.OutcomeAct:
		fmt.Fprintf(&b, "act → %s\n", res.Target)
	case routing.OutcomeConfirm:
		fmt.Fprintf(&b, "confirm required → proposing %s\n", res.Target)
	case routing.OutcomeRefuse:
		fmt.Fprintf(&b, "refuse (confidence too low to propose %s)\n", res.Target)
	case routing.OutcomeNeedsInference:
		fmt.Fprintf(&b, "needs-inference (no deterministic answer)\n")
	}
	fmt.Fprintf(&b, "basis: %s", res.Basis)
	if res.Source != "" {
		fmt.Fprintf(&b, " (source %s)", res.Source)
	}
	fmt.Fprintf(&b, " · confidence %.3f · signal %q", res.Confidence, res.Signal)
	for _, c := range res.Candidates {
		fmt.Fprintf(&b, "\ncandidate: %s (confidence %.3f, %s %s)", c.Target, c.Confidence, c.Basis, c.Source)
	}
	return b.String()
}

// renderRouteEvent renders a recorded decision/proposal with its ledger id
// and, for needs-inference, the exact follow-up command.
func renderRouteEvent(ev routing.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s", ev.Kind, ev.ID, renderRouteResult(*ev.Result))
	if ev.Decision != "" {
		fmt.Fprintf(&b, "\nfor decision: %s", ev.Decision)
	}
	if ev.Result.Outcome == routing.OutcomeNeedsInference {
		fmt.Fprintf(&b, "\nrun inference, then: parlay route propose --decision %s --target <t> --confidence <0..1>", ev.ID)
	}
	return b.String()
}

func printRouteJSON(v any) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route: rendering JSON: %v", err), config.ExitRuntime)
		return
	}
	fmt.Println(string(out))
}
