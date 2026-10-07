package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// mintedID is the ledger id shape that only ever appears in the view: a
// refusal is stored under an id the server mints locally and exposes on no
// wire response, so the view is its only copy.
const mintedID = "in-1791365394107585000-12"

// TestInputViewPrintsWholeIDsAndAlignsColumns pins the two things that made the
// refusal/recogniser-error rows unreplayable and the live tail unreadable:
// an id truncated to fit the column cannot be typed into --input, and a row
// whose cell width disagrees with its header drifts out of alignment.
func TestInputViewPrintsWholeIDsAndAlignsColumns(t *testing.T) {
	lat := 9 * time.Millisecond
	conf, thr := 0.25, 0.80
	now := time.Now()
	rows := []inputRow{
		{ID: mintedID, State: "refused", Why: "missing-device", Source: "remote-input", At: now},
		{ID: "m1", State: "delivered", Source: "poll-backlog", Channel: "c0",
			At: now.Add(-2 * time.Second), Latency: lat, HasLatency: true,
			Confidence: &conf, Threshold: &thr},
	}
	var buf bytes.Buffer
	renderInputRows(&buf, rows, inputStats{Retained: 2, Written: 2}, 40)
	out := buf.String()

	if !strings.Contains(out, mintedID) {
		t.Errorf("view truncates the only copy of a minted id, so it cannot be replayed:\n%s", out)
	}
	if strings.Contains(out, "in-1791365394107585...") {
		t.Errorf("id is still truncated:\n%s", out)
	}

	// Column alignment: the SOURCE value must start where the header's SOURCE
	// label does, on every row, or the table cannot be read across.
	lines := strings.Split(out, "\n")
	header := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "STATE") {
			header = l
			break
		}
	}
	if header == "" {
		t.Fatalf("no table header in:\n%s", out)
	}
	want := strings.Index(header, "SOURCE")
	for _, src := range []string{"remote-input", "poll-backlog"} {
		found := false
		for _, l := range lines {
			if strings.Contains(l, src) {
				if got := strings.Index(l, src); got != want {
					t.Errorf("row's SOURCE at column %d, header's at %d:\n%s", got, want, l)
				}
				found = true
			}
		}
		if !found {
			t.Errorf("no row carries source %q:\n%s", src, out)
		}
	}
}

// TestWatchHeaderAlignsWithWatchRows is the regression for the live tail: the
// header and the rows are printed by two different format strings, and they
// disagreed by 8 columns for every minted id.
func TestWatchHeaderAlignsWithWatchRows(t *testing.T) {
	header := watchHeader()
	row := watchRowLine(inputEvent{
		Ts: "2026-10-07T02:29:54.107Z", InputID: mintedID, Stage: "interpreted",
		Class: "recogniser_error", Source: "remote-input", Reason: "empty-transcript",
	}, "—")
	if !strings.Contains(row, mintedID) {
		t.Errorf("live tail truncates the id:\n%s", row)
	}
	for _, pair := range [][2]string{{header, row}} {
		for _, col := range []string{"STAGE", "CLASS", "SOURCE", "LATENCY"} {
			hAt := strings.Index(pair[0], col)
			rAt := strings.Index(pair[1], "interpreted")
			if col == "CLASS" {
				rAt = strings.Index(pair[1], "recogniser_error")
			} else if col == "SOURCE" {
				rAt = strings.Index(pair[1], "remote-input")
			} else if col == "LATENCY" {
				rAt = strings.Index(pair[1], "—")
			}
			if hAt < 0 || rAt != hAt {
				t.Errorf("%s: header column %d, row column %d\nheader: %q\nrow:    %q",
					col, hAt, rAt, pair[0], pair[1])
			}
		}
	}
}
