// Latency per stage: the block that answers "is the seam slow, and where?"
// rather than "how long did that one message take".
//
// The rules this file pins are the ones that make the block trustworthy:
// each observed transition gets its own distribution (not one number for the
// whole path), the slow hop is named rather than averaged away, a missing hop
// produces NO row, and an unmeasurable timestamp is counted rather than read
// as zero.
package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// latencyHops builds one input's hops from a start time and per-hop offsets.
func latencyHops(id string, base time.Time, stages []string, offsets []time.Duration) []inputEvent {
	out := make([]inputEvent, 0, len(stages))
	for i, stage := range stages {
		out = append(out, inputEvent{
			Seq:     uint64(i + 1),
			Ts:      base.Add(offsets[i]).Format(time.RFC3339Nano),
			InputID: id, Stage: stage, Class: "ok", Source: "remote-input",
		})
	}
	return out
}

func spanNamed(t *testing.T, spans []stageLatency, transition string) stageLatency {
	t.Helper()
	for _, s := range spans {
		if s.Transition == transition {
			return s
		}
	}
	t.Fatalf("no %q span in %+v", transition, spans)
	return stageLatency{}
}

// TestInputLatenciesDerivesOneSpanPerTransition is the core claim: the delay
// is attributed to the transition between hops, so a slow recogniser boundary
// and a slow delivery boundary are different rows.
func TestInputLatenciesDerivesOneSpanPerTransition(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := latencyHops("a", base,
		[]string{"received", "interpreted", "routed", "delivered"},
		[]time.Duration{0, 4 * time.Second, 4100 * time.Millisecond, 5 * time.Second})
	events = append(events, latencyHops("b", base,
		[]string{"received", "interpreted"}, []time.Duration{0, 2 * time.Second})...)

	spans, skipped := inputLatencies(events)
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0 for well-formed hops", skipped)
	}
	got := map[string]stageLatency{}
	for _, s := range spans {
		got[s.Transition] = s
	}
	if n := got["received→interpreted"].Count; n != 2 {
		t.Errorf("received→interpreted count = %d, want 2 (one sample per input)", n)
	}
	if d := got["received→interpreted"].P50; d != 2*time.Second {
		t.Errorf("received→interpreted p50 = %s, want 2s (nearest rank over two samples)", d)
	}
	if d := got["received→interpreted"].Max; d != 4*time.Second {
		t.Errorf("received→interpreted max = %s, want the 4s recogniser hop", d)
	}
	if d := got["interpreted→routed"].Max; d != 100*time.Millisecond {
		t.Errorf("interpreted→routed = %s, want 100ms", d)
	}
	if d := got[endToEndTransition].Max; d != 5*time.Second {
		t.Errorf("end to end = %s, want a's whole 5s path", d)
	}
	if n := got[endToEndTransition].Count; n != 2 {
		t.Errorf("end to end count = %d, want 2 inputs with two measurable hops", n)
	}
}

// The slow hop is the one worth printing first: during an incident the median
// of a fast stage is noise.
func TestInputLatenciesPutsTheSlowestStageFirstAndEndToEndLast(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := latencyHops("a", base,
		[]string{"received", "interpreted", "delivered"},
		[]time.Duration{0, 50 * time.Millisecond, 25 * time.Second})
	spans, _ := inputLatencies(events)
	if len(spans) < 3 {
		t.Fatalf("want at least three spans, got %+v", spans)
	}
	if spans[0].Transition != "interpreted→delivered" {
		t.Errorf("first span = %q, want the 25s delivery wait", spans[0].Transition)
	}
	if last := spans[len(spans)-1].Transition; last != endToEndTransition {
		t.Errorf("last span = %q, want %q", last, endToEndTransition)
	}
}

// An unmeasurable hop is counted, never read as zero — a zero sub-millisecond
// hop would look like the fastest stage in the seam.
func TestInputLatenciesCountsUnmeasurableHopsInsteadOfZeroingThem(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []inputEvent{
		{Seq: 1, Ts: "not-a-time", InputID: "a", Stage: "received"},
		{Seq: 2, Ts: base.Format(time.RFC3339Nano), InputID: "a", Stage: "interpreted"},
		// A hop timestamped BEFORE its predecessor: a clock that went
		// backwards is a negative duration, which must never be printed as a
		// fast stage.
		{Seq: 3, Ts: base.Add(-time.Hour).Format(time.RFC3339Nano), InputID: "a", Stage: "delivered"},
		{Seq: 4, Ts: base.Format(time.RFC3339Nano), InputID: "b", Stage: "received"},
	}
	spans, skipped := inputLatencies(events)
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2 (one unparseable hop, one backwards hop)", skipped)
	}
	for _, s := range spans {
		if s.Max < 0 || s.P50 < 0 {
			t.Errorf("%s carries a negative duration: %+v", s.Transition, s)
		}
	}
}

// A hop that did not happen must produce no row: silence here is the answer
// to "where is the delay", not an omission to fill in.
func TestInputLatenciesInventsNoRowForAHopThatDidNotHappen(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []inputEvent{{Seq: 1, Ts: base.Format(time.RFC3339Nano), InputID: "m1", Stage: "queued"}}
	spans, _ := inputLatencies(events)
	if len(spans) != 0 {
		t.Fatalf("a single-hop input produced spans: %+v", spans)
	}
}

func TestRenderInputLatencyNamesTheCaveatsAndTheSamples(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := latencyHops("a", base,
		[]string{"received", "interpreted", "delivered"},
		[]time.Duration{0, 120 * time.Millisecond, 300 * time.Millisecond})
	var buf bytes.Buffer
	renderInputLatency(&buf, events)
	out := buf.String()
	for _, want := range []string{
		"LATENCY BY STAGE",
		"received→interpreted",
		"1 sample(s)",
		"p50",
		"p95",
		"max",
		"end to end (first→last hop)",
		"DID NOT HAPPEN",
		"the recogniser runs on the phone and reports no duration in this repo",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("latency block is missing %q:\n%s", want, out)
		}
	}
}

// Silence would be the worst rendering of an empty window: it is exactly what
// a seam with no delay in it looks like.
func TestRenderInputLatencySaysWhenNothingIsMeasurable(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []inputEvent{{Seq: 1, Ts: base.Format(time.RFC3339Nano), InputID: "m1", Stage: "queued"}}
	var buf bytes.Buffer
	renderInputLatency(&buf, events)
	out := buf.String()
	if !strings.Contains(out, "nothing measurable in this window") {
		t.Errorf("an unmeasurable window printed no named state:\n%s", out)
	}
	if strings.Contains(out, "p50") {
		t.Errorf("an unmeasurable window printed a percentile:\n%s", out)
	}
}

func TestRenderInputLatencyReportsExcludedPairs(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []inputEvent{
		{Seq: 1, Ts: "bogus", InputID: "a", Stage: "received"},
		{Seq: 2, Ts: base.Format(time.RFC3339Nano), InputID: "a", Stage: "interpreted"},
	}
	var buf bytes.Buffer
	renderInputLatency(&buf, events)
	if !strings.Contains(buf.String(), "hop pair(s) excluded") {
		t.Errorf("an unmeasurable pair was dropped without a word:\n%s", buf.String())
	}
}

// The JSON half must be milliseconds under a name that says so: a
// time.Duration marshals as nanoseconds, so a field called p50Ms carrying
// nanoseconds is a lie a script cannot see.
func TestInputLatencyJSONIsMilliseconds(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := latencyHops("a", base,
		[]string{"received", "delivered"},
		[]time.Duration{0, 2500 * time.Millisecond})
	spans, _ := inputLatencies(events)
	wire := make([]stageLatencyWire, 0, len(spans))
	for _, s := range spans {
		wire = append(wire, s.wire())
	}
	b, err := json.Marshal(inputLatencyPage{
		inputPage:    inputPage{Events: events, Stats: inputStats{Retained: 2}},
		StageLatency: wire,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Events       []inputEvent       `json:"events"`
		Stats        inputStats         `json:"stats"`
		StageLatency []stageLatencyWire `json:"stageLatency"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b)
	}
	if len(decoded.Events) != 2 || decoded.Stats.Retained != 2 {
		t.Errorf("the ledger page stopped being where it was:\n%s", b)
	}
	var total int64 = -1
	for _, s := range decoded.StageLatency {
		if s.Transition == endToEndTransition {
			total = s.P50Ms
		}
	}
	if total != 2500 {
		t.Errorf("end-to-end p50Ms = %d, want 2500 (milliseconds, not nanoseconds)", total)
	}
}
