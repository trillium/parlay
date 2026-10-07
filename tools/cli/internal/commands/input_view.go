package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// `parlay input` — the operator-facing view of the input seam: what came in,
// through which door, whether it was delivered, queued, refused or held, and
// — under the table — which hop of the seam is slow (input_latency.go).
// A pure reader of GET /api/chat/input-events (the server's internal/inputlog
// ledger) that keeps no state of its own, so the view and a replay of the same
// id cannot disagree. See docs/input-seam.md for the vocabulary, and for the
// caveat it prints rather than hides: no push stream yet, so --watch polls.
// Input is the verb: `parlay input [--input <id>] [--watch] [--json]`.
func Input(argv []string) {
	if helpWanted("input", argv) {
		return
	}
	r := args.Parse("input", argv, []string{"--watch", "--json"},
		[]string{"--input", "--limit", "--interval", "--stale-after"})
	if len(r.Positionals) > 0 {
		httpc.Die(fmt.Sprintf("parlay input: unexpected argument %q — input ids go through --input <id>", r.Positionals[0]), config.ExitUsage)
	}
	limit, err := strconv.Atoi(optOr(r, "--limit", "40"))
	if err != nil || limit <= 0 {
		httpc.Die("parlay input: --limit must be a positive integer", config.ExitUsage)
	}
	staleSecs, err := strconv.Atoi(optOr(r, "--stale-after", strconv.Itoa(int(defaultStaleAfter/time.Second))))
	if err != nil || staleSecs < 0 {
		httpc.Die("parlay input: --stale-after must be a non-negative number of seconds", config.ExitUsage)
	}
	stale := time.Duration(staleSecs) * time.Second

	if id, ok := r.String("--input"); ok && id != "" {
		page := fetchInputPage("?inputId=" + url.QueryEscape(id))
		if r.Bool("--json") {
			printJSON(page)
			return
		}
		renderInputReplay(os.Stdout, id, page.Events, time.Now(), stale)
		return
	}
	if r.Bool("--watch") {
		interval, err := strconv.Atoi(optOr(r, "--interval", "2"))
		if err != nil || interval < 1 {
			httpc.Die("parlay input: --interval must be at least 1 second", config.ExitUsage)
		}
		watchInputs(limit, time.Duration(interval)*time.Second, r.Bool("--json"))
		return
	}
	page := fetchInputPage("?limit=" + strconv.Itoa(limit))
	if r.Bool("--json") {
		// The stage latencies are derived here, beside the table, so the
		// snapshot and the JSON cannot disagree about which hop is slow.
		spans, skipped := inputLatencies(page.Events)
		wire := make([]stageLatencyWire, 0, len(spans))
		for _, s := range spans {
			wire = append(wire, s.wire())
		}
		printJSON(inputLatencyPage{inputPage: page, StageLatency: wire, Excluded: skipped})
		return
	}
	renderInputRows(os.Stdout, inputRows(page.Events, time.Now(), stale), page.Stats, limit)
	renderInputLatency(os.Stdout, page.Events)
}

func optOr(r args.Result, flag, def string) string {
	if v, ok := r.String(flag); ok && v != "" {
		return v
	}
	return def
}

// fetchInputPage reads the ledger. An older server without the route is its
// own state, not a crash, following `parlay commands`: "cannot read" and
// "nothing happened" are different facts.
func fetchInputPage(query string) inputPage {
	page, ok := httpc.TryGetJSON[inputPage]("/api/chat/input-events"+query, httpc.DefaultTimeout)
	if !ok {
		httpc.Die("cannot read the input ledger — the server is unreachable, or it predates GET /api/chat/input-events", config.ExitRuntime)
	}
	return page
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		httpc.Die("parlay input: cannot encode JSON — "+err.Error(), config.ExitRuntime)
	}
	fmt.Println(string(b))
}

func renderInputRows(w io.Writer, rows []inputRow, stats inputStats, limit int) {
	fmt.Fprintf(w, "INPUT SEAM — %d input(s) from the last %d retained hop(s)\n", len(rows), limit)
	// The newest seq is what a live tail's cursor (and any GAP line it
	// printed) refers to, so the snapshot window says where it ends.
	seq := ""
	if stats.NewestSeq > 0 {
		seq = fmt.Sprintf(", newest seq %d", stats.NewestSeq)
	}
	fmt.Fprintf(w, "ledger: %d retained, %d written, %d dropped, %d rejected, %d queued%s\n",
		stats.Retained, stats.Written, stats.Dropped, stats.Rejected, stats.Queue, seq)
	fmt.Fprintln(w, thresholdLine(stats.MinConfidence))
	if len(rows) == 0 {
		fmt.Fprintln(w, "Nothing recorded. An empty view means no operator input reached an intake\n"+
			"surface yet, or the ledger is newer than the activity you are looking for. It is\n"+
			"not evidence that input is flowing.")
		return
	}
	fmt.Fprintf(w, "%-16s %-*s %-12s %-10s %-9s %-8s %s\n",
		"STATE", inputIDWidth, "INPUT", "SOURCE", "CHANNEL", "WHEN", "LATENCY", "WHY")
	now := time.Now()
	for _, r := range rows {
		fmt.Fprintf(w, "%-16s %-*s %-12s %-10s %-9s %-8s %s\n",
			r.State, inputIDWidth, cell(r.ID, inputIDWidth), cell(r.Source, 12), cell(r.Channel, 10),
			humanAge(now.Sub(r.At)), latencyCell(r), whyCell(r))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, inputConfidenceNote(rows))
	fmt.Fprintln(w, inputLegend)
}

// Input-seam note: the two honesty lines about the ledger's posture, and the
// legend for the state column, live in input_threshold.go.

// cell truncates a value to exactly n characters so it cannot break the table.
// ASCII dots, not an ellipsis: a multi-byte rune would misalign %-Ns padding.
func cell(s string, n int) string {
	if s == "" {
		return "-"
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

func whyCell(r inputRow) string {
	if r.Why == "" {
		return "—"
	}
	return r.Why
}

func latencyCell(r inputRow) string {
	if !r.HasLatency {
		return "—"
	}
	return "+" + humanAge(r.Latency)
}

// renderInputReplay shows every hop one input made, in order, with the gap
// between hops and where it stopped: one id, end to end, off the ledger.
func renderInputReplay(w io.Writer, id string, events []inputEvent, now time.Time, stale time.Duration) {
	hops := make([]inputEvent, 0, len(events))
	for _, e := range events {
		if e.InputID == id {
			hops = append(hops, e)
		}
	}
	sort.SliceStable(hops, func(i, j int) bool { return hops[i].Seq < hops[j].Seq })
	if len(hops) == 0 {
		fmt.Fprintf(w, "REPLAY %s — no hops in the retained ledger.\n\n"+
			"That is not proof the input never existed: the ledger retains its newest window, and an\n"+
			"input accepted before the ledger existed has no rows. Check `parlay history` for the\n"+
			"message itself, and `parlay input` for everything the ledger does hold.\n", id)
		return
	}
	fmt.Fprintf(w, "REPLAY %s — %d hop(s)\n\n", id, len(hops))
	var prev, last time.Time
	for i, e := range hops {
		t, ok := parseInputTs(e.Ts)
		delta := "—"
		if ok && !prev.IsZero() {
			delta = "+" + humanAge(t.Sub(prev))
		}
		if ok {
			prev, last = t, t
		}
		extra := ""
		if e.Reason != "" {
			extra = " why=" + e.Reason
		}
		if e.Detail != "" {
			extra += " detail=" + e.Detail
		}
		fmt.Fprintf(w, "  #%d %-7s %s  %-11s %-18s source=%-13s channel=%s%s\n",
			i+1, delta, inputStamp(e.Ts), e.Stage, e.Class, cell(e.Source, 16), cell(e.Channel, 12), extra)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Outcome: %s\n", replayOutcome(inputRowFor(hops, now, stale), last, now))
}

// watchInputs, watchHeader and watchRowLine live in input_watch.go.
