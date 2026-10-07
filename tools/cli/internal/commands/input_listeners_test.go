// The listener half of `parlay input`: does a waiting row have anything
// listening to it at all? These tests pin the classification table, the two
// renderings that read it, and — just as importantly — the states where the
// clause must NOT appear, because a note attached to a delivered row or to a
// server that reports nothing would be a view that lies.
package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestClassifyListener(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) channelListener {
		return channelListener{Channel: "c0", LastPollTs: now.Add(-d).Format(time.RFC3339Nano)}
	}
	cases := []struct {
		name  string
		fact  channelListener
		known bool
		want  string
	}{
		{"a channel the server never saw polled", channelListener{}, false, "never"},
		{"a listed channel with no poll stamp", channelListener{Channel: "c0"}, true, "never"},
		{"a poll just now", ago(2 * time.Second), true, "attached"},
		{"a poll inside the hold", ago(20 * time.Second), true, "attached"},
		{"quiet for more than two holds", ago(51 * time.Second), true, "quiet"},
		{"a parked poller outranks a stale stamp", channelListener{
			Channel: "c0", LastPollTs: now.Add(-time.Hour).Format(time.RFC3339Nano), ActivePollers: 1,
		}, true, "parked"},
		{"an unreadable stamp is not a stale one", channelListener{Channel: "c0", LastPollTs: "yesterday"}, true, "unreadable"},
		{"a server clock ahead of ours is age zero, not negative", ago(-time.Hour), true, "attached"},
	}
	for _, tc := range cases {
		if got := classifyListener(tc.fact, tc.known, defaultPollHold, now); got.Kind != tc.want {
			t.Errorf("%s: classifyListener = %q, want %q", tc.name, got.Kind, tc.want)
		}
	}
}

// The hold is the server's to state: if it holds polls for one second, a poll
// three seconds old is quiet, where the coded default would still call it
// attached.
func TestListenerQuietWindowComesFromTheServer(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	page := inputPage{Listeners: []channelListener{{
		Channel: "c0", LastPollTs: now.Add(-3 * time.Second).Format(time.RFC3339Nano),
	}}}
	if got := classifyListener(page.Listeners[0], true, defaultPollHold, now).Kind; got != "attached" {
		t.Errorf("with the default hold = %q, want attached", got)
	}
	page.PollHoldMs = 1000
	if got := classifyListener(page.Listeners[0], true, page.pollHold(), now).Kind; got != "quiet" {
		t.Errorf("with the server's 1s hold = %q, want quiet", got)
	}
}

func TestListenerNoteOnlyAppliesToWaitingRows(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	page := inputPage{Listeners: []channelListener{}}
	waiting := inputRow{ID: "m1", Channel: "c0", State: stateQueued, At: now}
	if note := page.listenerNote(waiting, now); !strings.Contains(note, "no listener has asked") {
		t.Errorf("a waiting row on a never-polled channel = %q, want the never-polled note", note)
	}
	for _, row := range []inputRow{
		{ID: "m2", Channel: "c0", State: "delivered", At: now},
		{ID: "m3", Channel: "c0", State: "refused", At: now},
		{ID: "m4", State: stateQueued, At: now}, // no channel: nothing to ask about
	} {
		if note := page.listenerNote(row, now); note != "" {
			t.Errorf("row %s (%s) got a listener note %q, want none", row.ID, row.State, note)
		}
	}
	// A server that does not report listener activity leaves every row alone.
	silent := inputPage{}
	if note := silent.listenerNote(waiting, now); note != "" {
		t.Errorf("an unreporting server produced %q, want no note", note)
	}
}

// The row, joined: the unpicked reason first, then who is (not) listening —
// the two halves of "it is queued and nobody took it".
func TestRenderInputRowsSaysWhetherAnythingIsListening(t *testing.T) {
	now := time.Now()
	page := inputPage{
		PollHoldMs: 25000,
		Listeners: []channelListener{{
			Channel: "agent-b", LastPollTs: now.Add(-20 * time.Minute).Format(time.RFC3339Nano),
		}},
	}
	rows := []inputRow{
		{ID: "m1", Channel: "agent-a", State: stateQueuedUnpicked, Why: "no listener picked it up in 2m00s", At: now.Add(-2 * time.Minute)},
		{ID: "m2", Channel: "agent-b", State: stateQueuedUnpicked, Why: "no listener picked it up in 2m00s", At: now.Add(-2 * time.Minute)},
	}
	var buf bytes.Buffer
	renderInputRows(&buf, withListenerFacts(rows, page, now), inputStats{Retained: 2, Written: 2}, 40)
	out := buf.String()
	if !strings.Contains(out, "no listener has asked for this channel since the server started") {
		t.Errorf("a queued row on a never-polled channel does not say so:\n%s", out)
	}
	if !strings.Contains(out, "nothing is polling this channel (last poll 20m00s ago)") {
		t.Errorf("a queued row on a quiet channel does not say so:\n%s", out)
	}
	if !strings.Contains(out, "no listener picked it up in 2m00s — ") {
		t.Errorf("the listener clause replaced the unpicked reason instead of joining it:\n%s", out)
	}
}

func TestRenderInputListenersStates(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	// Not reported: the absence is printed, never read as silence.
	var silent bytes.Buffer
	renderInputListeners(&silent, inputPage{}, now)
	if !strings.Contains(silent.String(), "does not report listener activity") {
		t.Errorf("an unreporting server is not named:\n%s", silent.String())
	}
	if strings.Contains(silent.String(), "nothing has polled any channel") {
		t.Errorf("an unreporting server was read as 'nothing is listening':\n%s", silent.String())
	}

	// Reported and empty: nothing is listening, said plainly, with the window.
	var empty bytes.Buffer
	renderInputListeners(&empty, inputPage{Listeners: []channelListener{}}, now)
	if !strings.Contains(empty.String(), "nothing has polled any channel since the server started") {
		t.Errorf("an empty listener list is not named:\n%s", empty.String())
	}
	for _, want := range []string{"once per 25.0s", "more than 50.0s"} {
		if !strings.Contains(empty.String(), want) {
			t.Errorf("the hold window behind the claim is missing %q:\n%s", want, empty.String())
		}
	}

	// Parked and quiet, sorted by channel, with the hold line.
	var buf bytes.Buffer
	renderInputListeners(&buf, inputPage{PollHoldMs: 25000, Listeners: []channelListener{
		{Channel: "zz", LastPollTs: now.Add(-time.Second).Format(time.RFC3339Nano), ActivePollers: 2},
		{Channel: "aa", LastPollTs: now.Add(-4 * time.Minute).Format(time.RFC3339Nano)},
	}}, now)
	out := buf.String()
	if strings.Index(out, "aa") > strings.Index(out, "zz") {
		t.Errorf("channels are not sorted:\n%s", out)
	}
	for _, want := range []string{"parked pollers 2", "a listener is waiting on it right now", "nothing is polling it", "last poll 4m00s ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listener block is missing %q:\n%s", want, out)
		}
	}
}

func TestRenderInputListenersCapsAndCountsWhatItHid(t *testing.T) {
	now := time.Now()
	var page inputPage
	for _, ch := range []string{"c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8", "c9"} {
		page.Listeners = append(page.Listeners, channelListener{Channel: ch, LastPollTs: now.Format(time.RFC3339Nano)})
	}
	var buf bytes.Buffer
	renderInputListeners(&buf, page, now)
	if !strings.Contains(buf.String(), "(+2 more channel(s) not shown)") {
		t.Errorf("the block hid channels without saying so:\n%s", buf.String())
	}
}

// A replay's Outcome must carry the same clause the table does: two answers to
// one question is a defect.
func TestReplayOutcomeCarriesTheListenerClause(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	page := inputPage{
		PollHoldMs: 25000,
		Events: []inputEvent{{
			Seq: 1, Ts: now.Add(-3 * time.Minute).Format(time.RFC3339Nano), InputID: "m1",
			Stage: "queued", Class: "ok", Source: "send", Channel: "agent-a",
		}},
		Listeners: []channelListener{}, // reported, and nothing has polled
	}
	var buf bytes.Buffer
	renderInputReplay(&buf, "m1", page, now, defaultStaleAfter)
	out := buf.String()
	for _, want := range []string{"Outcome: QUEUED (UNPICKED)", "no listener has asked for this channel"} {
		if !strings.Contains(out, want) {
			t.Errorf("replay outcome is missing %q:\n%s", want, out)
		}
	}
}
