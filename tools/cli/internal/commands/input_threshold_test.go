package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The threshold is only "visible in the view" if the view prints it, so this
// asserts on the rendered bytes rather than on the helper alone.
func TestThresholdIsVisibleInTheRenderedView(t *testing.T) {
	rows := []inputRow{{
		ID: "ri-1", State: "delivered", Source: "remote-input", At: time.Now(),
	}}
	cases := []struct {
		name     string
		min      *float64
		want     string
		notThere string
	}{
		{"disabled", nil, "threshold: none", "hold below confidence"},
		{"enabled", fl(0.6), "threshold: hold below confidence 0.60", "threshold: none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			renderInputRows(&b, rows, inputStats{MinConfidence: tc.min}, 40)
			out := b.String()
			if !strings.Contains(out, tc.want) {
				t.Errorf("view does not state the threshold (%q missing):\n%s", tc.want, out)
			}
			if strings.Contains(out, tc.notThere) {
				t.Errorf("view contradicts itself (%q present):\n%s", tc.notThere, out)
			}
			if !strings.Contains(out, "Legend:") {
				t.Errorf("view lost its legend:\n%s", out)
			}
		})
	}
}

// A window with no reported confidence must not read as a confident one.
func TestConfidenceNoteSaysNotReportedRatherThanConfident(t *testing.T) {
	note := inputConfidenceNote([]inputRow{{ID: "m1", State: "delivered"}})
	if !strings.Contains(note, "not reported") || !strings.Contains(note, "is not \"confident\"") {
		t.Errorf("note = %q, want it to say confidence was not reported", note)
	}
	note = inputConfidenceNote([]inputRow{{ID: "ri-1", State: "delivered", Confidence: fl(0.9)}})
	if !strings.Contains(note, "reported for 1 of 1") {
		t.Errorf("note = %q, want it to count the reported confidence", note)
	}
}
