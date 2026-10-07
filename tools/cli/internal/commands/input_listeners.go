// Who is listening: the one fact the ledger cannot carry.
//
// A queued hop and a queued hop that nobody will ever collect are the same
// row, so "delivery queued but never picked up" has no cause inside the
// ledger. The server has the cause — it sees every poll request — and reports
// it beside the ledger: per channel, when anything last asked for messages,
// how many long-polls are parked right now, and whether that last ask carried
// a backlog cursor. This file joins those facts onto the rows, so a waiting
// input can say whether anything is listening, and — when the answer is
// "something is, and it still did not take it" — which mechanism made that
// possible.
//
// One rule, two renderings. classifyListener is the only place the
// classification is decided, and listenerNote is the only place the clause is
// phrased; the row's WHY and the block under the table both read them, so they
// cannot disagree about who is listening. When the server does not report
// listener activity at all, every row is left exactly as it was and the block
// prints that absence: no report is never read as "nothing is listening".
package commands

import (
	"fmt"
	"time"
)

// channelListener mirrors the server's per-channel poll activity.
type channelListener struct {
	Channel       string `json:"channel"`
	LastPollTs    string `json:"lastPollTs,omitempty"`
	ActivePollers int    `json:"activePollers"`

	// LastPollCursored is whether that last poll asked for the retained
	// backlog. Nil means the server does not report it (it predates the
	// field), which is a different fact from "that poll carried no cursor" —
	// reading them alike would call every listener on an older server blind.
	LastPollCursored *bool `json:"lastPollCursored"`
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

// listenerState is the classification both surfaces print. Kind is one of
// never, parked, quiet, attached, unreadable; At is the poll's own timestamp
// when it parsed, which is what lets a row say whether the input was already
// waiting when the listener asked.
type listenerState struct {
	Kind   string
	Age    time.Duration
	HasAge bool
	At     time.Time
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
			st.Age, st.HasAge, st.At = age, true, t
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

// missedTheBacklog answers the one question that turns "attached and not
// taking it" into a named mechanism: could the listener's last ask have
// returned this input at all?
//
// No. handlePoll consults the retained store ONLY for a request that carried
// `after=`; every other poll parks on the live broker and so can only ever see
// a message published while it waited. So when the server reports that the
// last poll carried no cursor AND the input was already queued by then, this
// listener was structurally unable to collect it — not slow, not ignoring it.
//
// It returns false when the answer is not provable, never when it is merely
// inconvenient: an unreporting server (nil), a cursored poll, a poll whose
// timestamp did not parse, and an input newer than the poll all fall through,
// because for those the honest answer is "this is not the reason".
func missedTheBacklog(fact channelListener, row inputRow, st listenerState) bool {
	if fact.LastPollCursored == nil || *fact.LastPollCursored {
		return false
	}
	return st.HasAge && !row.At.IsZero() && !row.At.After(st.At)
}

// listenerNote is the clause appended to a waiting row's WHY. It returns ""
// whenever the question does not apply — the row was delivered, it has no
// channel, or the server does not report listeners at all.
func (p inputPage) listenerNote(row inputRow, now time.Time) string {
	if p.Listeners == nil || row.Channel == "" || !row.waitingOnListener() {
		return ""
	}
	fact, known := p.listenerFor(row.Channel)
	st := classifyListener(fact, known, p.pollHold(), now)
	switch st.Kind {
	case "parked":
		if missedTheBacklog(fact, row, st) {
			return "a listener is parked on this channel and has not taken it — its last poll carried no backlog cursor, so it could not have returned this input"
		}
		return "a listener is parked on this channel and has not taken it"
	case "never":
		return "no listener has asked for this channel since the server started"
	case "unreadable":
		return "the server's last poll time for this channel is unreadable"
	case "quiet":
		return fmt.Sprintf("nothing is polling this channel (last poll %s ago)", humanAge(st.Age))
	default:
		base := fmt.Sprintf("a listener is attached (last poll %s ago) and has not taken it", humanAge(st.Age))
		if missedTheBacklog(fact, row, st) {
			base += " — its last poll carried no backlog cursor, so it could not have returned this input"
		}
		return base
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
