// The other half of "make the threshold visible": the measurements that
// cleared it. These are the rules that stop the margin line from lying, and the
// surfaces that carry the same number (a replay, the live tail). Split from
// input_threshold_test.go only because the ceiling is 250 lines per file.
package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestThresholdMarginLineNamesTheClosestInput: a threshold nobody comes near
// and a threshold that nearly stopped something read identically without this
// line, which is exactly the fact an operator setting one needs.
func TestThresholdMarginLineNamesTheClosestInput(t *testing.T) {
	now := time.Now()
	rows := []inputRow{
		{ID: "far", Confidence: fl(0.95), Threshold: fl(0.60), At: now},
		{ID: "near", Confidence: fl(0.62), Threshold: fl(0.60), At: now.Add(-time.Minute)},
		{ID: "under", Confidence: fl(0.58), Threshold: fl(0.60), At: now.Add(-2 * time.Minute)},
	}
	cases := []struct {
		name string
		rows []inputRow
		min  *float64
		want []string
	}{
		{"no threshold, no line", rows, nil, nil},
		{
			"a window that reported nothing says so",
			[]inputRow{{ID: "m1", State: "delivered", At: now}}, fl(0.6),
			[]string{"no input in this window reported a confidence"},
		},
		{
			"a confidence measured with the threshold off is not comparable",
			[]inputRow{{ID: "m1", Confidence: fl(0.62), At: now}}, fl(0.6),
			[]string{"measured with no threshold set", "none is comparable with the one in force now"},
		},
		{
			"closest from above names the margin",
			rows[:2], fl(0.6),
			[]string{"closest input in this window is near", "confidence 0.62 above threshold 0.60 (margin 0.02)"},
		},
		{
			"a closest row that missed names that instead",
			rows[2:], fl(0.6),
			[]string{"is under", "confidence 0.58 below threshold 0.60"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := thresholdMarginLine(tc.rows, tc.min)
			if tc.want == nil {
				if got != "" {
					t.Fatalf("line = %q, want no line when nothing is held", got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("line %q does not name %q", got, w)
				}
			}
		})
	}
}

// A tie must not depend on map iteration or on the caller's slice order: the
// newest input wins, then the smaller id, so two runs over one window agree.
func TestThresholdMarginLineBreaksTiesDeterministically(t *testing.T) {
	now := time.Now()
	rows := []inputRow{
		{ID: "bb-older", Confidence: fl(0.62), Threshold: fl(0.60), At: now.Add(-time.Minute)},
		{ID: "aa-newer", Confidence: fl(0.58), Threshold: fl(0.60), At: now},
	}
	if got := thresholdMarginLine(rows, fl(0.6)); !strings.Contains(got, "is aa-newer —") {
		t.Errorf("tie was not broken toward the newest input: %q", got)
	}
	same := []inputRow{
		{ID: "zz", Confidence: fl(0.62), Threshold: fl(0.60), At: now},
		{ID: "aa", Confidence: fl(0.62), Threshold: fl(0.60), At: now},
	}
	if got := thresholdMarginLine(same, fl(0.6)); !strings.Contains(got, "is aa —") {
		t.Errorf("same-instant tie was not broken by id: %q", got)
	}
}

// A replay is read to find the hop that went wrong, so a hop that carried a
// measurement has to show it on that hop rather than only in the table.
func TestReplayPrintsTheReportedConfidencePerHop(t *testing.T) {
	now := time.Now()
	page := inputPage{
		Events: []inputEvent{
			{Seq: 1, Ts: now.Format(time.RFC3339Nano), InputID: "ri-7", Stage: "queued", Class: "ok", Source: "remote-input"},
			{Seq: 2, Ts: now.Add(time.Second).Format(time.RFC3339Nano), InputID: "ri-7", Stage: "interpreted", Class: "ok",
				Source: "remote-input", Confidence: fl(0.62), Threshold: fl(0.60)},
		},
	}
	var buf bytes.Buffer
	renderInputReplay(&buf, "ri-7", page, now, defaultStaleAfter)
	out := buf.String()
	if !strings.Contains(out, "confidence 0.62 above threshold 0.60") {
		t.Errorf("replay does not show the confidence the hop carried:\n%s", out)
	}
	if strings.Count(out, "confidence ") != 1 {
		t.Errorf("replay invented a confidence on a hop that carried none:\n%s", out)
	}
}
