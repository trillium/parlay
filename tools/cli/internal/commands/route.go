// parlay route — the deterministic-first routing verb (#128 §34–§37; model
// and rationale in docs/routing.md, engine in internal/routing).
//
// Surface: decide (evaluate + record), why (dry-run trace), propose
// (record an external inference result for a needs-inference decision),
// confirm/correct (the feedback that hardens and un-hardens learned
// routes), and rule add/retire (authored rules + tombstones).
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
	case "confirm":
		routeConfirm(rest)
	case "correct":
		routeCorrect(rest)
	case "rule":
		routeRule(rest)
	default:
		httpc.Die("parlay route: subcommand required: decide | why | propose | confirm | correct | rule (see 'parlay route --help')", config.ExitUsage)
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

// routeFeedbackRef resolves the ledger event a confirm/correct refers to and
// extracts the signal evidence accrues to. Confirm and correct both accept a
// decision id OR a proposal id — the hardening loop's common case is
// confirming what an inference proposal suggested, not what a rule decided.
func routeFeedbackRef(st *routing.Store, eventID string) (routing.Event, error) {
	ev, ok, err := st.FindEvent(eventID)
	if err != nil {
		return routing.Event{}, err
	}
	if !ok {
		return routing.Event{}, fmt.Errorf("no event %q in the ledger (ids come from 'route decide' / 'route propose')", eventID)
	}
	if (ev.Kind != routing.EventDecision && ev.Kind != routing.EventProposal) || ev.Result == nil {
		return routing.Event{}, fmt.Errorf("%s is a %s event — feedback attaches to decisions and proposals", eventID, ev.Kind)
	}
	if ev.Result.Signal == "" {
		return routing.Event{}, fmt.Errorf("%s has no lead signal — an unkeyed input accrues no evidence and cannot harden (docs/routing.md §3)", eventID)
	}
	return ev, nil
}

// routeConfirmRun applies a confirmation of the referenced event's
// (signal → target) and records it on the ledger.
func routeConfirmRun(st *routing.Store, eventID string, auth routing.Authority, now string) (routing.Event, routing.Evidence, error) {
	ref, err := routeFeedbackRef(st, eventID)
	if err != nil {
		return routing.Event{}, routing.Evidence{}, err
	}
	if ref.Result.Target == "" {
		return routing.Event{}, routing.Evidence{}, fmt.Errorf("%s decided no target — nothing to confirm; teach with: route correct %s --target <t>", eventID, eventID)
	}
	rs, err := st.LoadRuleset()
	if err != nil {
		return routing.Event{}, routing.Evidence{}, err
	}
	e := rs.RecordConfirmation(ref.Result.Signal, ref.Result.Target, auth, eventID)
	updated := *e
	if err := st.SaveRuleset(rs); err != nil {
		return routing.Event{}, routing.Evidence{}, err
	}
	ev := routing.Event{
		ID:        routing.NewEventID(),
		Kind:      routing.EventConfirm,
		Time:      now,
		Decision:  eventID,
		Authority: string(auth),
		Target:    ref.Result.Target,
		Entry:     updated.ID,
	}
	if err := st.AppendEvent(ev); err != nil {
		return routing.Event{}, routing.Evidence{}, err
	}
	return ev, updated, nil
}

// routeCorrectRun records that the referenced event's input belonged at
// rightTarget: a correction against the decided target (when there was one)
// plus a confirmation of the right one. On a needs-inference decision it is
// pure teaching — only the confirmation side applies.
func routeCorrectRun(st *routing.Store, eventID, rightTarget string, auth routing.Authority, now string) (routing.Event, *routing.Evidence, routing.Evidence, error) {
	ref, err := routeFeedbackRef(st, eventID)
	if err != nil {
		return routing.Event{}, nil, routing.Evidence{}, err
	}
	if rightTarget == ref.Result.Target {
		return routing.Event{}, nil, routing.Evidence{}, fmt.Errorf("%s already decided %q — a correction to the same target is a confirm; run: route confirm %s", eventID, rightTarget, eventID)
	}
	rs, err := st.LoadRuleset()
	if err != nil {
		return routing.Event{}, nil, routing.Evidence{}, err
	}
	demoted, taught := rs.RecordCorrection(ref.Result.Signal, ref.Result.Target, rightTarget, auth, eventID)
	var demotedCopy *routing.Evidence
	if demoted != nil {
		c := *demoted
		demotedCopy = &c
	}
	taughtCopy := *taught
	if err := st.SaveRuleset(rs); err != nil {
		return routing.Event{}, nil, routing.Evidence{}, err
	}
	ev := routing.Event{
		ID:        routing.NewEventID(),
		Kind:      routing.EventCorrect,
		Time:      now,
		Decision:  eventID,
		Authority: string(auth),
		Target:    rightTarget,
		Entry:     taughtCopy.ID,
	}
	if err := st.AppendEvent(ev); err != nil {
		return routing.Event{}, nil, routing.Evidence{}, err
	}
	return ev, demotedCopy, taughtCopy, nil
}

func routeAuthorityFlag(r args.Result, cmd string) routing.Authority {
	raw, _ := r.String("--authority")
	auth, err := routing.ParseAuthority(raw)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route %s: %v", cmd, err), config.ExitUsage)
	}
	return auth
}

func routeConfirm(argv []string) {
	r := args.Parse("route confirm", argv, []string{"--json"}, []string{"--authority"})
	if len(r.Positionals) != 1 {
		httpc.Die("parlay route confirm: usage: route confirm <decision-or-proposal-id> [--authority captain|agent]", config.ExitUsage)
		return
	}
	auth := routeAuthorityFlag(r, "confirm")
	st := routeStore()
	ev, evidence, err := routeConfirmRun(st, r.Positionals[0], auth, routeNow())
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route confirm: %v", err), config.ExitRuntime)
		return
	}
	pol, polErr := st.LoadPolicy()
	if polErr != nil {
		pol = routing.DefaultPolicy()
	}
	if r.Bool("--json") {
		printRouteJSON(struct {
			Event    routing.Event    `json:"event"`
			Evidence routing.Evidence `json:"evidence"`
		}{ev, evidence})
		return
	}
	fmt.Printf("confirm %s recorded (%s) for %q → %s\n", ev.ID, ev.Authority, evidence.Signal, evidence.Target)
	fmt.Println(renderEvidenceLine(evidence, pol))
	if auth != routing.AuthorityCaptain {
		fmt.Println("note: agent feedback is recorded for observability only — it never counts toward hardening")
	}
}

func routeCorrect(argv []string) {
	r := args.Parse("route correct", argv, []string{"--json"}, []string{"--target", "--authority"})
	target, haveTarget := r.String("--target")
	if len(r.Positionals) != 1 || !haveTarget || target == "" {
		httpc.Die("parlay route correct: usage: route correct <decision-or-proposal-id> --target <t> [--authority captain|agent]", config.ExitUsage)
		return
	}
	auth := routeAuthorityFlag(r, "correct")
	st := routeStore()
	ev, demoted, taught, err := routeCorrectRun(st, r.Positionals[0], target, auth, routeNow())
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route correct: %v", err), config.ExitRuntime)
		return
	}
	pol, polErr := st.LoadPolicy()
	if polErr != nil {
		pol = routing.DefaultPolicy()
	}
	if r.Bool("--json") {
		printRouteJSON(struct {
			Event   routing.Event     `json:"event"`
			Demoted *routing.Evidence `json:"demoted,omitempty"`
			Taught  routing.Evidence  `json:"taught"`
		}{ev, demoted, taught})
		return
	}
	fmt.Printf("correct %s recorded (%s): %q belongs at %s\n", ev.ID, ev.Authority, taught.Signal, taught.Target)
	if demoted != nil {
		fmt.Println("demoted: " + renderEvidenceLine(*demoted, pol))
	}
	fmt.Println("taught:  " + renderEvidenceLine(taught, pol))
	if auth != routing.AuthorityCaptain {
		fmt.Println("note: agent feedback is recorded for observability only — it never counts toward hardening")
	}
}

func routeRule(argv []string) {
	sub := ""
	rest := []string{}
	if len(argv) > 0 {
		sub, rest = argv[0], argv[1:]
	}
	switch sub {
	case "add":
		routeRuleAdd(rest)
	case "retire":
		routeRuleRetire(rest)
	default:
		httpc.Die("parlay route rule: subcommand required: add | retire", config.ExitUsage)
	}
}

func routeRuleAdd(argv []string) {
	r := args.Parse("route rule add", argv, nil, []string{"--key", "--target", "--id", "--note"})
	key, haveKey := r.String("--key")
	target, haveTarget := r.String("--target")
	if !haveKey || !haveTarget || len(r.Positionals) > 0 {
		httpc.Die("parlay route rule add: usage: route rule add --key <k> --target <t> [--id <id>] [--note <n>]", config.ExitUsage)
		return
	}
	id, _ := r.String("--id")
	note, _ := r.String("--note")
	st := routeStore()
	rs, err := st.LoadRuleset()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route rule add: %v", err), config.ExitRuntime)
		return
	}
	rule, err := rs.AddRule(id, key, target, note)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route rule add: %v", err), config.ExitUsage)
		return
	}
	if err := st.SaveRuleset(rs); err != nil {
		httpc.Die(fmt.Sprintf("parlay route rule add: %v", err), config.ExitRuntime)
		return
	}
	fmt.Printf("rule %s: %q → %s (matches word-boundary prefixes; longest key wins)\n", rule.ID, rule.Key, rule.Target)
}

func routeRuleRetire(argv []string) {
	r := args.Parse("route rule retire", argv, nil, []string{"--note"})
	if len(r.Positionals) != 1 {
		httpc.Die("parlay route rule retire: usage: route rule retire <rule-or-evidence-id> [--note <why>]", config.ExitUsage)
		return
	}
	id := r.Positionals[0]
	note, _ := r.String("--note")
	st := routeStore()
	rs, err := st.LoadRuleset()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route rule retire: %v", err), config.ExitRuntime)
		return
	}
	kind, desc, err := rs.RetireEntry(id, note)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay route rule retire: %v", err), config.ExitRuntime)
		return
	}
	if err := st.SaveRuleset(rs); err != nil {
		httpc.Die(fmt.Sprintf("parlay route rule retire: %v", err), config.ExitRuntime)
		return
	}
	ev := routing.Event{
		ID:    routing.NewEventID(),
		Kind:  routing.EventRetire,
		Time:  routeNow(),
		Entry: id,
		Note:  note,
	}
	if err := st.AppendEvent(ev); err != nil {
		httpc.Die(fmt.Sprintf("parlay route rule retire: %v", err), config.ExitRuntime)
		return
	}
	fmt.Printf("retired %s %s (%s) — tombstoned, never matches again; history preserved\n", kind, id, desc)
}

// renderEvidenceLine renders one evidence entry's state under a policy —
// the operator's view of how close a route is to (or from) hardened.
func renderEvidenceLine(e routing.Evidence, pol routing.Policy) string {
	c := e.Confidence()
	state := "refused"
	switch {
	case c >= pol.ActThreshold:
		state = fmt.Sprintf("HARDENED (acts at ≥ %.2f)", pol.ActThreshold)
	case c >= pol.RefuseThreshold:
		state = "proposes with confirmation"
	}
	return fmt.Sprintf("evidence %s: %q → %s · %d confirms, %d corrections (%d agent events, not counted) · confidence %.3f — %s",
		e.ID, e.Signal, e.Target, e.Confirms, e.Corrections, e.AgentEvents, c, state)
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
