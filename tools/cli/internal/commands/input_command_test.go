// "What became a command" — the state the eval door added to the view.
//
// The engine's `fired` field says which phrase command it read the buffer as.
// A fired command and a phrase that matched nothing leave the same visible
// trace in the panel (the box keeps its text), so the view has to name the
// difference — and it must not report a fired command as an input that
// "stopped after interpreted", which is what the generic stage fallback would
// call it.
package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDeriveInputRowNamesWhatAnInputBecame(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	hops := []inputEvent{{
		Seq: 1, Ts: now.Add(-3 * time.Second).Format(time.RFC3339Nano),
		Stage: "interpreted", Class: "ok", Source: "eval",
		Detail: "command=submit stream=eval-dev-1-main v=2",
	}}
	got := deriveInputRow(mintedID, hops, now, defaultStaleAfter)
	if got.State != "command" {
		t.Fatalf("state = %q, want command", got.State)
	}
	if got.Why != "submit" {
		t.Errorf("why = %q, want the command the engine fired", got.Why)
	}
	if strings.Contains(got.Why, "stopped") {
		t.Errorf("a fired command reads as an input that went nowhere: %q", got.Why)
	}
}

// A ledger written before the eval door recorded commands must render exactly
// as it did before: an interpreted hop with no command on it is still an input
// that stopped there.
func TestDeriveInputRowOnAnOlderLedgerDoesNotInventACommand(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	hops := []inputEvent{{
		Seq: 1, Ts: now.Add(-5 * time.Minute).Format(time.RFC3339Nano),
		Stage: "interpreted", Class: "ok", Source: "remote-input", Detail: "mode=inject",
	}}
	got := deriveInputRow(mintedID, hops, now, defaultStaleAfter)
	if got.State != "interpreted" || got.Why != "stopped after interpreted (5m00s)" {
		t.Errorf("row = %s/%s, want interpreted with where it stopped", got.State, got.Why)
	}
}

func TestCommandFromDetailReadsOnlyACommandToken(t *testing.T) {
	cases := map[string]string{
		"command=submit stream=s v=2": "submit",
		"stream=s v=2 command=clear":  "clear",
		"mode=inject":                 "",
		"":                            "",
		"command=":                    "",
		"nocommand=submit":            "",
	}
	for detail, want := range cases {
		if got := commandFromDetail(detail); got != want {
			t.Errorf("commandFromDetail(%q) = %q, want %q", detail, got, want)
		}
	}
}

func TestRenderInputRowsShowsTheCommandAndLegendsIt(t *testing.T) {
	now := time.Now()
	rows := []inputRow{
		{ID: mintedID, State: "command", Why: "submit", Source: "eval", At: now},
	}
	var buf bytes.Buffer
	renderInputRows(&buf, rows, inputStats{Retained: 1, Written: 1}, 40)
	out := buf.String()
	if !strings.Contains(out, "command") || !strings.Contains(out, "submit") {
		t.Errorf("the view does not say what the input became:\n%s", out)
	}
	if !strings.Contains(inputLegend, "command =") {
		t.Errorf("the state column names a state its legend does not:\n%s", inputLegend)
	}
}

// The eval door's other new producer: a spoken channel name the engine matched
// against the offered channels and found nothing. It renders as `no match`
// with its own reason — never as health.
func TestRenderInputReplayOfAPickerMiss(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []inputEvent{{
		Seq: 1, Ts: now.Format(time.RFC3339Nano), InputID: "in-eval-1",
		Stage: "routed", Class: "no_match", Source: "eval",
		Reason: "channel-not-matched",
		Detail: "stream=eval-dev-1-picker v=1 mode=channel-select candidates=3",
	}}
	var buf bytes.Buffer
	renderInputReplay(&buf, "in-eval-1", events, now, defaultStaleAfter)
	out := buf.String()
	for _, want := range []string{"no_match", "channel-not-matched", "candidates=3", "Outcome: NO MATCH"} {
		if !strings.Contains(out, want) {
			t.Errorf("replay of a picker miss is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Outcome: OK") || strings.Contains(out, "DELIVERED") {
		t.Errorf("a picker miss reported a healthy outcome:\n%s", out)
	}
}

func TestRenderInputReplayOfAFiredCommand(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []inputEvent{{
		Seq: 1, Ts: now.Format(time.RFC3339Nano), InputID: "in-eval-2",
		Stage: "interpreted", Class: "ok", Source: "eval",
		Detail: "command=switch-tab stream=eval-dev-1-main v=4",
	}}
	var buf bytes.Buffer
	renderInputReplay(&buf, "in-eval-2", events, now, defaultStaleAfter)
	out := buf.String()
	if !strings.Contains(out, "Outcome: COMMAND — switch-tab") {
		t.Errorf("replay does not name the command this input became:\n%s", out)
	}
}
