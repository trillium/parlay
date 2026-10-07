// Input-seam recording: the three call sites that turn "an operator input
// moved" into a durable inputlog row.
//
// Every function here is non-blocking and returns nothing, because all three
// sit in request handlers whose job is to move the input. store.Store.Input
// owns the queue and the drop accounting; see internal/inputlog for why that
// is a queue at all.
//
// Nothing here is a second model of the truth. The ids recorded are the
// message ids the message store already assigned, so a replay joins the
// ledger against messages.jsonl on the id both sides already carry.
package handlers

import (
	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/store"
)

// Intake-source labels. These name the door an input came in through, which
// is a fact this server knows rather than a guess about the caller.
const (
	inputSourceSend        = "send"         // POST /api/chat/send
	inputSourceAlert       = "alert"        // POST /api/chat/alert
	inputSourcePollWake    = "poll-wake"    // GET /api/chat/poll, handed to a parked waiter
	inputSourcePollBacklog = "poll-backlog" // GET /api/chat/poll, answered from the retained store
)

// recordQueued notes that a stored message is waiting to be picked up.
//
// Only role=="user" messages are operator input; agent replies and
// system_update lines are output and belong to a different question, so they
// are deliberately not recorded here. A queued row is ClassOK — a queue is
// not a failure. "Queued and never picked up" is a later, separate judgement
// (inputlog.ClassUnpicked), made only once something has actually waited.
func recordQueued(st *store.Store, m store.ChatMessage, source string) {
	if st == nil || m.Role != "user" {
		return
	}
	st.Input.Record(inputlog.Event{
		InputID: m.ID,
		Stage:   inputlog.StageQueued,
		Class:   inputlog.ClassOK,
		Source:  source,
		Channel: m.Channel,
	})
}

// recordDelivered notes that a listener was handed the message. source is
// which poll surface did it — the wake path (a parked waiter resolved by the
// new message) and the backlog path (the retained store answered) are
// different, and a view that merged them could not tell a hot delivery from
// a drain after a reconnect.
func recordDelivered(st *store.Store, m store.ChatMessage, source string) {
	if st == nil || m.Role != "user" {
		return
	}
	st.Input.Record(inputlog.Event{
		InputID: m.ID,
		Stage:   inputlog.StageDelivered,
		Class:   inputlog.ClassOK,
		Source:  source,
		Channel: m.Channel,
	})
}

// recordRefused notes input that an intake surface declined to act on. It
// has no message id — nothing was stored — so the caller passes the
// ledger-local id it minted for this request.
func recordRefused(st *store.Store, inputID, source, reason string) {
	if st == nil {
		return
	}
	st.Input.Record(inputlog.Event{
		InputID: inputID,
		Stage:   inputlog.StageInterpreted,
		Class:   inputlog.ClassRefused,
		Reason:  reason,
		Source:  source,
	})
}
