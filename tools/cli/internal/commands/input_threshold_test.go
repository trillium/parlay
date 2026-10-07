package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

// TestConfidenceClauseSeparatesTheFourFacts is the honesty table for the one
// sentence the view uses to describe a measurement. The trap it guards is the
// oldest one here: an unreported confidence must never be rendered as though
// it were a number, and a measured-below must keep the wording the held rows
// already used so one fact does not acquire two spellings.
func TestConfidenceClauseSeparatesTheFourFacts(t *testing.T) {
	cases := []struct {
		name      string
		conf, thr *float64
		want      string
	}{
		{"unreported is empty, never zero", nil, nil, ""},
		{"unreported stays empty even with a threshold", nil, fl(0.8), ""},
		{"reported with no threshold says so", fl(0.93), nil, "confidence 0.93 (no threshold set)"},
		{"above names the margin", fl(0.93), fl(0.60), "confidence 0.93 above threshold 0.60 (margin 0.33)"},
		{"below keeps the held rows' wording", fl(0.35), fl(0.80), "confidence 0.35 below threshold 0.80"},
		{"level to the printed precision is not called above", fl(0.60), fl(0.60), "confidence 0.60 at threshold 0.60"},
		{"a margin under half a hundredth reads as level", fl(0.603), fl(0.60), "confidence 0.60 at threshold 0.60"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := confidenceClause(tc.conf, tc.thr); got != tc.want {
				t.Errorf("confidenceClause = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConfidenceColumnShowsAReportedConfidenceForHealthyInputs is the point of
// this iteration: a dictation that was DELIVERED still has to show what the
// recogniser said about it, or the only visible confidence is the one that
// failed and the operator can never see what nearly did.
func TestConfidenceColumnShowsAReportedConfidenceForHealthyInputs(t *testing.T) {
	now := time.Now()
	rows := []inputRow{
		{ID: "ri-1", State: "delivered", Source: "remote-input", At: now, Confidence: fl(0.62), Threshold: fl(0.60)},
		{ID: "ri-2", State: "delivered", Source: "remote-input", At: now.Add(-time.Second)},
	}
	var buf bytes.Buffer
	renderInputRows(&buf, rows, inputStats{Retained: 2, MinConfidence: fl(0.60)}, 40)
	out := buf.String()
	for _, want := range []string{"CONF", "0.62", "Threshold margin: the closest input in this window is ri-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("view does not print %q:\n%s", want, out)
		}
	}
	// A reported confidence is a column, not a verdict: it must not turn a
	// delivered row's WHY into a failure note.
	var delivered []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "delivered") {
			delivered = append(delivered, l)
		}
	}
	if len(delivered) != 2 {
		t.Fatalf("want two delivered rows, got %d:\n%s", len(delivered), out)
	}
	if !strings.HasSuffix(strings.TrimRight(delivered[0], " "), "—") {
		t.Errorf("a healthy row's WHY changed: %q", delivered[0])
	}
	// Where a surface reported nothing, the cell must say so rather than show
	// a zero, which would read as a measured, terrible confidence.
	if !strings.Contains(delivered[1], "-") || strings.Contains(delivered[1], "0.00") {
		t.Errorf("an unreported confidence is not rendered as absence: %q", delivered[1])
	}
}

// The CONF column is a column: its value must start where its header does, or
// the table cannot be read across — and the header/row format strings are two
// different literals that can drift apart.
func TestConfidenceColumnAlignsWithItsHeader(t *testing.T) {
	rows := []inputRow{{ID: "ri-1", State: "delivered", Source: "remote-input", At: time.Now(), Confidence: fl(0.93), Threshold: fl(0.60)}}
	var buf bytes.Buffer
	renderInputRows(&buf, rows, inputStats{Retained: 1, MinConfidence: fl(0.60)}, 40)
	out := buf.String()
	header := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "STATE") {
			header = l
			break
		}
	}
	if !strings.Contains(header, "CONF") {
		t.Fatalf("no CONF column in:\n%s", out)
	}
	headerAt := utf8.RuneCountInString(header[:strings.Index(header, "CONF")])
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "delivered") && strings.Contains(l, "0.93") {
			// Columns are RUNE offsets: a row before this one carries an em
			// dash for an unmeasured latency, and its 3 bytes would offset a
			// byte comparison by two while the table is still aligned.
			got := utf8.RuneCountInString(l[:strings.Index(l, "0.93")])
			if got != headerAt {
				t.Errorf("CONF value at column %d, header at %d:\n%s", got, headerAt, l)
			}
			return
		}
	}
	t.Fatalf("no delivered row carrying the confidence:\n%s", out)
}
