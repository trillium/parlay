// Who is listening: the one fact the ledger cannot carry.
//
// A queued hop and a queued hop that nobody will ever collect are the same
// row, so "delivery queued but never picked up" has no cause inside the
// ledger. The server has the cause — it sees every poll request — and reports
// it beside the ledger: per channel, when anything last asked for messages,
// and how many long-polls are parked right now. This file joins those facts
// onto the rows, so a waiting input can say whether anything is listening.
//
// One rule, two renderings. classifyListener is the only place the
// classification is decided; the row's WHY and the block under the table both
// read it, so they cannot disagree about who is listening. When the server
// does not report listener activity at all, every row is left exactly as it
// was and the block prints that absence: no report is never read as "nothing
// is listening".
package commands

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// channelListener mirrors the server's per-channel poll activity.
type channelListener struct {
	Channel       string `json:"channel"`
	LastPollTs    string `json:"lastPollTs,omitempty"`
	ActivePollers int    `json:"activePollers"`
}

// The two derived states that mean "durably held, waiting for a listener".
// Named here, beside the note that keys off them, rather than repeated as
// literals at both ends of the join.
const (
	stateQueued         = "queued"
	stateQueuedUnpicked = "queued (unpicked)"
)

// waitingOnListener reports whether an input ended up waiting for something to
// collect it — the only rows for which "is anything listening?" is a useful
// question.
func (r inputRow) waitingOnListener() bool {
	return r.State == stateQueued || r.State == stateQueuedUnpicked
}

// defaultPollHold is the long-poll hold assumed when a server does not report
// its own. A listener that is attached cannot be quieter than the hold: its
// next request arrives when the previous one returns.
const defaultPollHold = 25 * time.Second

// maxListenerRows bounds the block. A fleet can have many channels; the rows
// are sorted and the count omitted is printed rather than dropped silently.
const maxListenerRows = 8

// listenerState is the classification both surfaces print. Kind is one of
// never, parked, quiet, attached, unreadable.
type listenerState struct {
	Kind   string
	Age    time.Duration
	HasAge bool
}

// classifyListener decides what the server's report means for one channel.
// `known` is whether the channel appears in the report at all: a channel that
// never polled is absent from it, which is a fact, not missing data.
func classifyListener(fact channelListener, known bool, hold time.Duration, now time.Time) listenerState {
	st := listenerState{}
	if known {
		if t, ok := parseInputTs(fact.LastPollTs); ok {
			// A server whose clock runs ahead must not read as silent for
			// hours: a poll stamped in the future is age zero, not negative.
			age := now.Sub(t)
			if age < 0 {
				age = 0
			}
			st.Age, st.HasAge = age, true
		} else if fact.LastPollTs != "" {
			st.Kind = "unreadable"
		}
	}
	// A parked poller outranks everything: something is attached right now,
	// whatever the timestamps say.
	if fact.ActivePollers > 0 {
		st.Kind = "parked"
		return st
	}
	if st.Kind != "" {
		return st
	}
	if !known || fact.LastPollTs == "" {
		st.Kind = "never"
		return st
	}
	if st.Age > 2*hold {
		st.Kind = "quiet"
		return st
	}
	st.Kind = "attached"
	return st
}

// pollHold is the window a live listener cannot be quieter than.
func (p inputPage) pollHold() time.Duration {
	if p.PollHoldMs > 0 {
		return time.Duration(p.PollHoldMs) * time.Millisecond
	}
	return defaultPollHold
}

// listenerFor finds a channel's entry. known=false is a real answer: the
// server reports every channel it has ever been polled on, so a channel that
// is missing from a present report has never been polled.
func (p inputPage) listenerFor(channel string) (channelListener, bool) {
	for _, l := range p.Listeners {
		if l.Channel == channel {
			return l, true
		}
	}
	return channelListener{}, false
}

// listenerNote is the clause appended to a waiting row's WHY. It returns ""
// whenever the question does not apply — the row was delivered, it has no
// channel, or the server does not report listeners at all.
func (p inputPage) listenerNote(row inputRow, now time.Time) string {
	if p.Listeners == nil || row.Channel == "" || !row.waitingOnListener() {
		return ""
	}
	fact, known := p.listenerFor(row.Channel)
	switch st := classifyListener(fact, known, p.pollHold(), now); st.Kind {
	case "parked":
		return "a listener is parked on this channel and has not taken it"
	case "never":
		return "no listener has asked for this channel since the server started"
	case "unreadable":
		return "the server's last poll time for this channel is unreadable"
	case "quiet":
		return fmt.Sprintf("nothing is polling this channel (last poll %s ago)", humanAge(st.Age))
	default:
		return fmt.Sprintf("a listener is attached (last poll %s ago) and has not taken it", humanAge(st.Age))
	}
}

// withListenerFacts adds the listener clause to every waiting row, in place.
func withListenerFacts(rows []inputRow, p inputPage, now time.Time) []inputRow {
	for i := range rows {
		note := p.listenerNote(rows[i], now)
		switch {
		case note == "":
		case rows[i].Why == "":
			rows[i].Why = note
		default:
			rows[i].Why += " — " + note
		}
	}
	return rows
}

// renderInputListeners prints who has been asking each channel for messages.
// Drill-down beside the table: the rows answer "why is THIS input stuck", the
// block answers "is my fleet listening at all", and neither can be inferred
// from the other.
func renderInputListeners(w io.Writer, p inputPage, now time.Time) {
	fmt.Fprintln(w)
	if p.Listeners == nil {
		fmt.Fprintln(w, "LISTENERS — this server does not report listener activity, so a waiting row")
		fmt.Fprintln(w, "cannot say whether anything is listening to it.")
		return
	}
	fmt.Fprintln(w, "LISTENERS — per channel, from the server's own poll activity (runtime facts: a")
	fmt.Fprintln(w, "restart resets them, and a channel absent here has never been polled)")
	if len(p.Listeners) == 0 {
		fmt.Fprintln(w, "  nothing has polled any channel since the server started — a waiting input is")
		fmt.Fprintln(w, "  not being ignored; there is nothing there to take it.")
		printListenerHold(w, p.pollHold())
		return
	}
	sorted := append([]channelListener(nil), p.Listeners...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Channel < sorted[j].Channel })
	shown := sorted
	if len(shown) > maxListenerRows {
		shown = shown[:maxListenerRows]
	}
	for _, f := range shown {
		st := classifyListener(f, true, p.pollHold(), now)
		age := "—"
		if st.HasAge {
			age = humanAge(st.Age) + " ago"
		}
		fmt.Fprintf(w, "  %-18s parked pollers %-3d last poll %-10s %s\n",
			cell(f.Channel, 18), f.ActivePollers, age, listenerVerdict(st))
	}
	if n := len(sorted) - len(shown); n > 0 {
		fmt.Fprintf(w, "  (+%d more channel(s) not shown)\n", n)
	}
	printListenerHold(w, p.pollHold())
}

// listenerVerdict phrases one classification for the block.
func listenerVerdict(st listenerState) string {
	switch st.Kind {
	case "parked":
		return "a listener is waiting on it right now"
	case "never":
		return "never polled since the server started"
	case "unreadable":
		return "last poll time unreadable"
	case "quiet":
		return "nothing is polling it"
	default:
		return "a listener is attached"
	}
}

// printListenerHold states the window behind the classification, so "nothing
// is polling it" is a claim with its number beside it rather than a hunch.
func printListenerHold(w io.Writer, hold time.Duration) {
	fmt.Fprintf(w, "  a listener that is attached asks at least once per %s (the server holds a\n",
		humanAge(hold))
	fmt.Fprintf(w, "  parked poll that long), so a channel quiet for more than %s has nothing polling it.\n",
		humanAge(2*hold))
}
