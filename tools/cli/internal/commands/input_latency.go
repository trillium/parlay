// Latency per stage: which hop of the seam is slow.
//
// One number per input ("that message took 25s") answers the wrong question
// during an incident. "It felt slow" and "the recogniser took four seconds"
// end the same way — the operator waited — so the seam needs the delay
// attributed to a transition between hops, not to the input as a whole. This
// file derives one distribution per hop-to-hop transition over whatever
// window the ledger returned, so the slow hop is named instead of averaged
// away.
//
// It is derived from the same hops the table is built from: no new wire
// field, no server work, nothing on the delivery path. Its honesty rules are
// the seam's usual ones — a percentile without its sample count, a transition
// with no row read as a fast hop, or an unmeasurable timestamp read as zero
// would each be a view that lies.
package commands

import (
	"fmt"
	"io"
	"sort"
	"time"
)

const (
	// endToEndTransition names the whole-path row. It cannot collide with a
	// hop name: the ledger's stage vocabulary is closed (internal/inputlog).
	endToEndTransition = "end to end"
	// maxLatencyRows bounds the block. Transitions share one small alphabet,
	// but a malformed ledger could still produce many; the slowest medians
	// are the ones worth the space, and the count omitted is printed.
	maxLatencyRows = 6
)

// stageLatency is one transition's distribution over the window. Percentiles
// are nearest-rank, and Max is carried separately because with a handful of
// samples p95 IS the max — saying both is more honest than implying a smooth
// distribution that the sample size cannot support.
type stageLatency struct {
	Transition string
	Count      int
	P50, P95   time.Duration
	Max        time.Duration
}

// stageLatencyWire is what --json prints: integer milliseconds, named `Ms`,
// because a time.Duration marshals as nanoseconds and a field called p50Ms
// carrying nanoseconds would be a lie a script cannot see.
type stageLatencyWire struct {
	Transition string `json:"transition"`
	Count      int    `json:"count"`
	P50Ms      int64  `json:"p50Ms"`
	P95Ms      int64  `json:"p95Ms"`
	MaxMs      int64  `json:"maxMs"`
}

func (s stageLatency) wire() stageLatencyWire {
	return stageLatencyWire{
		Transition: s.Transition,
		Count:      s.Count,
		P50Ms:      s.P50.Milliseconds(),
		P95Ms:      s.P95.Milliseconds(),
		MaxMs:      s.Max.Milliseconds(),
	}
}

// inputLatencyPage is the snapshot's --json shape: the ledger page it would
// have printed before, plus the stage latencies derived from it. The ledger
// page is embedded, so events and stats stay exactly where a script already
// reads them and this stays an additive change.
type inputLatencyPage struct {
	inputPage
	StageLatency []stageLatencyWire `json:"stageLatency,omitempty"`
	Excluded     int                `json:"excludedHopPairs,omitempty"`
}

// inputLatencies derives one span per observed transition. Hops are grouped by
// input and ordered by seq; each consecutive pair contributes one sample to
// "<from>→<to>", and an input's first-to-last span contributes to the
// end-to-end row. A pair whose timestamps do not parse, or that runs
// backwards, is COUNTED rather than treated as zero: an unmeasurable hop is a
// fact about the ledger, not a fast one.
func inputLatencies(events []inputEvent) ([]stageLatency, int) {
	byID := map[string][]inputEvent{}
	var order []string
	for _, e := range events {
		if _, seen := byID[e.InputID]; !seen {
			order = append(order, e.InputID)
		}
		byID[e.InputID] = append(byID[e.InputID], e)
	}
	samples := map[string][]time.Duration{}
	skipped := 0
	for _, id := range order {
		hops := byID[id]
		sort.SliceStable(hops, func(i, j int) bool { return hops[i].Seq < hops[j].Seq })
		// The whole path is only timed when every hop on it could be timed:
		// stitching an end-to-end number out of a subset would be a
		// measurement of something no input actually did.
		allMeasurable := len(hops) > 1
		for i := 1; i < len(hops); i++ {
			prev, okPrev := parseInputTs(hops[i-1].Ts)
			cur, okCur := parseInputTs(hops[i].Ts)
			if !okPrev || !okCur || cur.Before(prev) {
				skipped++
				allMeasurable = false
				continue
			}
			key := stageOrUnknown(hops[i-1].Stage) + "→" + stageOrUnknown(hops[i].Stage)
			samples[key] = append(samples[key], cur.Sub(prev))
		}
		// An input that made one hop has not been through a transition, and
		// one that is still queued has no end to measure to.
		if allMeasurable {
			start, _ := parseInputTs(hops[0].Ts)
			end, _ := parseInputTs(hops[len(hops)-1].Ts)
			if !end.Before(start) {
				samples[endToEndTransition] = append(samples[endToEndTransition], end.Sub(start))
			}
		}
	}
	spans := make([]stageLatency, 0, len(samples))
	for key, ds := range samples {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		spans = append(spans, stageLatency{
			Transition: key,
			Count:      len(ds),
			P50:        nearestRank(ds, 50),
			P95:        nearestRank(ds, 95),
			Max:        ds[len(ds)-1],
		})
	}
	sortLatencySpans(spans)
	return spans, skipped
}

// sortLatencySpans puts the slowest median first — during an incident the
// interesting hop is the slow one — with end to end last, because it is the
// summary rather than a stage. Ties fall back to the name so the order is
// stable for a test and for two runs over the same window.
func sortLatencySpans(spans []stageLatency) {
	sort.SliceStable(spans, func(i, j int) bool {
		endI, endJ := spans[i].Transition == endToEndTransition, spans[j].Transition == endToEndTransition
		if endI != endJ {
			return endJ
		}
		if spans[i].P50 != spans[j].P50 {
			return spans[i].P50 > spans[j].P50
		}
		return spans[i].Transition < spans[j].Transition
	})
}

// nearestRank is the definition the block advertises: the p-th percentile is
// the sample at index ceil(p/100*n) in ascending order, at least the first.
// With two samples its p50 IS the smaller of the two, which is why the row
// prints the sample count next to it.
func nearestRank(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p*len(sorted) + 99) / 100
	if idx < 1 {
		idx = 1
	}
	return sorted[idx-1]
}

func stageOrUnknown(stage string) string {
	if stage == "" {
		return "?"
	}
	return stage
}

// renderInputLatency prints the per-stage summary under the table. Silence is
// not an option: a window with nothing measurable says so and says why, so an
// empty block cannot be read as a seam with no delay in it.
func renderInputLatency(w io.Writer, events []inputEvent) {
	spans, skipped := inputLatencies(events)
	var transitions []stageLatency
	var total *stageLatency
	for i := range spans {
		if spans[i].Transition == endToEndTransition {
			total = &spans[i]
			continue
		}
		transitions = append(transitions, spans[i])
	}
	fmt.Fprintln(w)
	if len(transitions) == 0 && total == nil {
		fmt.Fprintln(w, "LATENCY BY STAGE — nothing measurable in this window: no input here made two")
		fmt.Fprintln(w, "timeable hops (a single-hop input has no transition, and an input still queued has")
		fmt.Fprintln(w, "no delivery to measure to).")
	} else {
		fmt.Fprintln(w, "LATENCY BY STAGE — hop to hop, over THIS window (nearest-rank percentiles)")
		shown := transitions
		if len(shown) > maxLatencyRows {
			shown = shown[:maxLatencyRows]
		}
		for _, s := range shown {
			latencyLine(w, s.Transition, "sample(s)", s)
		}
		if n := len(transitions) - len(shown); n > 0 {
			fmt.Fprintf(w, "  (+%d more transition(s) with a smaller median not shown)\n", n)
		}
		if total != nil {
			latencyLine(w, "end to end (first→last hop)", "input(s)", *total)
		}
		fmt.Fprintln(w, "A transition with no row is a hop that DID NOT HAPPEN — not a fast hop. received→")
		fmt.Fprintln(w, "interpreted times this intake's own handling of an already-transcribed submission;")
		fmt.Fprintln(w, "the recogniser runs on the phone and reports no duration in this repo.")
	}
	if skipped > 0 {
		fmt.Fprintf(w, "  (%d hop pair(s) excluded: a timestamp did not parse or a hop ran backwards —\n"+
			"   an unmeasurable hop is not a fast one.)\n", skipped)
	}
}

func latencyLine(w io.Writer, name, unit string, s stageLatency) {
	fmt.Fprintf(w, "  %-28s %3d %-10s p50 %-8s p95 %-8s max %s\n",
		name, s.Count, unit, humanLatency(s.P50), humanLatency(s.P95), humanLatency(s.Max))
}

// humanLatency renders a stage duration. A sub-millisecond hop is named as
// such rather than printed as a bare "0ms", which reads as "no time passed".
func humanLatency(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	return humanAge(d)
}
