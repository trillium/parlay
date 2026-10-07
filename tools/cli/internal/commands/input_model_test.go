package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The point of this view is that each failure on the input seam gets its own
// honest name. A test per class, because a view that showed "ok" for any of
// them would be worse than no view at all.
func TestDeriveInputRowNamesEveryFailureClass(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	at := func(sec int) string { return now.Add(-time.Duration(sec) * time.Second).Format(time.RFC3339Nano) }

	cases := []struct {
		name      string
		hops      []inputEvent
		wantState string
		wantWhy   string
	}{
		{
			name:      "refused carries its reason",
			hops:      []inputEvent{{Seq: 1, Ts: at(5), Stage: "interpreted", Class: "refused", Reason: "empty-input"}},
			wantState: "refused", wantWhy: "empty-input",
		},
		{
			name:      "recogniser error is not a low-confidence transcript",
			hops:      []inputEvent{{Seq: 1, Ts: at(5), Stage: "interpreted", Class: "recogniser_error", Reason: "no-speech"}},
			wantState: "recogniser error", wantWhy: "no-speech",
		},
		{
			// A named failure must never render as the absence of a later hop:
			// this read "routed — stopped after routed" until it was fixed.
			name:      "no match is named, not reported as where it stopped",
			hops:      []inputEvent{{Seq: 1, Ts: at(5), Stage: "received", Class: "ok"}, {Seq: 2, Ts: at(5), Stage: "routed", Class: "no_match", Reason: "target-not-matched"}},
			wantState: "no match", wantWhy: "target-not-matched",
		},
		{
			name: "a low-confidence measurement names the threshold",
			hops: []inputEvent{{Seq: 1, Ts: at(5), Stage: "interpreted", Class: "low_confidence",
				Reason: "below-threshold", Confidence: fl(0.35), Threshold: fl(0.80)}},
			wantState: "low confidence", wantWhy: "confidence 0.35 below threshold 0.80",
		},
		{
			// The held hop is the action and wins over the measurement that
			// preceded it, which is the order the intake records them in.
			name: "a hold is named held, not low confidence",
			hops: []inputEvent{
				{Seq: 1, Ts: at(9), Stage: "received", Class: "ok"},
				{Seq: 2, Ts: at(9), Stage: "interpreted", Class: "low_confidence",
					Reason: "below-threshold", Confidence: fl(0.35), Threshold: fl(0.80)},
				{Seq: 3, Ts: at(9), Stage: "held", Class: "held",
					Reason: "below-threshold", Confidence: fl(0.35), Threshold: fl(0.80)},
			},
			wantState: "held", wantWhy: "confidence 0.35 below threshold 0.80",
		},
		{
			name:      "held without numbers still says why",
			hops:      []inputEvent{{Seq: 1, Ts: at(5), Stage: "held", Class: "held"}},
			wantState: "held", wantWhy: "held by policy",
		},
		{
			// The eval relay's producer (docs/input-seam.md, the eval door): a
			// composer snapshot the engine dropped because a newer one had
			// already replaced it. It is a named state, never a health report.
			name: "superseded",
			hops: []inputEvent{
				{Seq: 1, Ts: at(5), Stage: "superseded", Class: "superseded",
					Reason: "superseded-by-newer-version", Source: "eval",
					Detail: "stream=eval-dev-1-main v=3 engine=stale-request-version"},
			},
			wantState: "superseded", wantWhy: "superseded-by-newer-version",
		},
		{
			name:      "a queued input past the window is unpicked, not queued",
			hops:      []inputEvent{{Seq: 1, Ts: at(300), Stage: "queued", Class: "ok"}},
			wantState: "queued (unpicked)", wantWhy: "no listener picked it up in 5m00s",
		},
		{
			name:      "a received-only input stopped where it stopped",
			hops:      []inputEvent{{Seq: 1, Ts: at(300), Stage: "received", Class: "ok"}},
			wantState: "received", wantWhy: "stopped after received (5m00s)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := deriveInputRow("m1", c.hops, now, defaultStaleAfter)
			if got.State != c.wantState {
				t.Errorf("state = %q, want %q", got.State, c.wantState)
			}
			if got.Why != c.wantWhy {
				t.Errorf("why = %q, want %q", got.Why, c.wantWhy)
			}
			if got.State == "delivered" {
				t.Errorf("state %q reports a healthy delivery for %+v", got.State, c.hops)
			}
		})
	}
}

func TestDeriveInputRowReportsDeliveredWithLatency(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	base := now.Add(-time.Minute)
	hops := []inputEvent{
		{Seq: 1, Ts: base.Format(time.RFC3339Nano), Stage: "queued", Class: "ok", Source: "send", Channel: "c0"},
		{Seq: 2, Ts: base.Add(9 * time.Millisecond).Format(time.RFC3339Nano), Stage: "delivered", Class: "ok", Source: "poll-wake", Channel: "c0"},
	}
	got := deriveInputRow("m1", hops, now, defaultStaleAfter)
	if got.State != "delivered" {
		t.Fatalf("state = %q, want delivered", got.State)
	}
	if !got.HasLatency || got.Latency != 9*time.Millisecond {
		t.Errorf("latency = %v (has=%v), want 9ms — \"it felt slow\" and \"the recogniser took 4s\" are different bugs", got.Latency, got.HasLatency)
	}
	if got.Channel != "c0" || got.Source != "poll-wake" {
		t.Errorf("channel/source = %q/%q, want c0/poll-wake (the newest hop wins)", got.Channel, got.Source)
	}
}

func TestDeriveInputRowKeepsAnUnreportedConfidenceDistinctFromZero(t *testing.T) {
	now := time.Now()
	unreported := deriveInputRow("m1", []inputEvent{{Seq: 1, Ts: now.Format(time.RFC3339Nano), Stage: "queued", Class: "ok"}}, now, defaultStaleAfter)
	if unreported.Confidence != nil {
		t.Fatalf("confidence = %v, want absent — an unreported confidence is not 0.0", *unreported.Confidence)
	}
	reported := deriveInputRow("m2", []inputEvent{{Seq: 1, Ts: now.Format(time.RFC3339Nano), Stage: "queued", Class: "ok", Confidence: fl(0)}}, now, defaultStaleAfter)
	if reported.Confidence == nil {
		t.Fatal("a reported confidence of 0 came back absent")
	}
}

func TestInputRowsGroupsByInputNewestFirst(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []inputEvent{
		{Seq: 1, Ts: now.Add(-30 * time.Second).Format(time.RFC3339Nano), InputID: "old", Stage: "queued", Class: "ok"},
		{Seq: 2, Ts: now.Add(-10 * time.Second).Format(time.RFC3339Nano), InputID: "new", Stage: "queued", Class: "ok"},
		{Seq: 3, Ts: now.Add(-10*time.Second + time.Millisecond).Format(time.RFC3339Nano), InputID: "new", Stage: "delivered", Class: "ok"},
	}
	rows := inputRows(events, now, defaultStaleAfter)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (one per input)", len(rows))
	}
	if rows[0].ID != "new" || rows[0].State != "delivered" {
		t.Errorf("first row = %s/%s, want new/delivered (newest input first)", rows[0].ID, rows[0].State)
	}
	if rows[1].ID != "old" || rows[1].State != "queued" {
		t.Errorf("second row = %s/%s, want old/queued", rows[1].ID, rows[1].State)
	}
}

func TestRenderInputReplayShowsEveryHopAndWhereItStopped(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	base := now.Add(-time.Minute)
	events := []inputEvent{
		{Seq: 1, Ts: base.Format(time.RFC3339Nano), InputID: "m1", Stage: "queued", Class: "ok", Source: "send", Channel: "c0"},
		{Seq: 2, Ts: base.Add(4 * time.Second).Format(time.RFC3339Nano), InputID: "m1", Stage: "delivered", Class: "ok", Source: "poll-backlog", Channel: "c0"},
	}
	var buf bytes.Buffer
	renderInputReplay(&buf, "m1", events, now, defaultStaleAfter)
	out := buf.String()
	for _, want := range []string{"REPLAY m1", "queued", "delivered", "poll-backlog", "+4.0s", "Outcome: DELIVERED in 4.0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("replay output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderInputReplayOnAnUnknownIDDoesNotClaimItNeverExisted(t *testing.T) {
	var buf bytes.Buffer
	renderInputReplay(&buf, "m-ghost", nil, time.Now(), defaultStaleAfter)
	out := buf.String()
	if strings.Contains(out, "DELIVERED") || strings.Contains(out, "Outcome: OK") {
		t.Errorf("an unknown id reported a healthy outcome:\n%s", out)
	}
	if !strings.Contains(out, "not proof the input never existed") {
		t.Errorf("unknown-id replay must state what the absence does and does not mean:\n%s", out)
	}
}

func TestRenderInputRowsStatesTheConfidencePostureOfTheWindow(t *testing.T) {
	now := time.Now()
	rows := []inputRow{{ID: "m1", State: "queued", At: now}}
	var buf bytes.Buffer
	renderInputRows(&buf, rows, inputStats{Retained: 1, Written: 1}, 40)
	out := buf.String()
	if !strings.Contains(out, "not reported by any surface in this window") {
		t.Errorf("view does not state that confidence was unreported:\n%s", out)
	}
	if !strings.Contains(out, "ledger: 1 retained, 1 written, 0 dropped, 0 rejected") {
		t.Errorf("view hides the ledger's own counters, so a shedding observer would look like a quiet night:\n%s", out)
	}

	withConfidence := []inputRow{{ID: "m2", State: "held", At: now, Confidence: fl(0.4), Threshold: fl(0.8)}}
	buf.Reset()
	renderInputRows(&buf, withConfidence, inputStats{Retained: 1}, 40)
	if !strings.Contains(buf.String(), "reported for 1 of 1") {
		t.Errorf("a reported confidence was not reported as such:\n%s", buf.String())
	}
}

func TestRenderInputRowsOnAnEmptyLedgerDoesNotClaimInputIsFlowing(t *testing.T) {
	var buf bytes.Buffer
	renderInputRows(&buf, nil, inputStats{}, 40)
	out := buf.String()
	if !strings.Contains(out, "Nothing recorded") || !strings.Contains(out, "not evidence that input is flowing") {
		t.Errorf("empty view is not honest about what empty means:\n%s", out)
	}
}

func TestHumanAgeIsReadableAtEveryScale(t *testing.T) {
	cases := map[time.Duration]string{
		9 * time.Millisecond:                 "9ms",
		4500 * time.Millisecond:              "4.5s",
		2*time.Minute + 7*time.Second:        "2m07s",
		3*time.Hour + 12*time.Minute:         "3h12m",
		-time.Duration(9) * time.Millisecond: "9ms",
	}
	for d, want := range cases {
		if got := humanAge(d); got != want {
			t.Errorf("humanAge(%v) = %q, want %q", d, got, want)
		}
	}
}

func fl(v float64) *float64 { return &v }
