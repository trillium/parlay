// Model half of `parlay input`: the wire types, and the rules that turn a set
// of ledger hops into one operator-facing state per input. It is kept apart
// from the rendering so a test can assert the derivation without a terminal,
// and so the live view and a replay of the same id cannot drift — both go
// through deriveInputRow.
package commands

import (
	"fmt"
	"sort"
	"time"
)

// inputEvent mirrors the server's inputlog.Event wire shape.
type inputEvent struct {
	Seq        uint64   `json:"seq"`
	Ts         string   `json:"ts"`
	InputID    string   `json:"inputId"`
	Stage      string   `json:"stage"`
	Class      string   `json:"class"`
	Source     string   `json:"source,omitempty"`
	Channel    string   `json:"channel,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	Threshold  *float64 `json:"threshold,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Detail     string   `json:"detail,omitempty"`
}

// inputStats is the ledger's own honesty report — an empty view and a shedding
// ledger cannot look alike. MinConfidence is the hold threshold, nil when off.
type inputStats struct {
	Retained      uint64   `json:"retained"`
	Written       uint64   `json:"written"`
	Dropped       uint64   `json:"dropped"`
	Rejected      uint64   `json:"rejected"`
	Queue         int      `json:"queue"`
	MinConfidence *float64 `json:"minConfidence,omitempty"`
}

type inputPage struct {
	Events []inputEvent `json:"events"`
	Stats  inputStats   `json:"stats"`
}

// defaultStaleAfter is how long a queued input may sit before this view calls
// it unpicked — comfortably above the server's 25s long-poll timeout.
const defaultStaleAfter = 60 * time.Second

// inputRow is one input, derived from every hop the ledger recorded for it.
type inputRow struct {
	ID         string
	State      string
	Why        string
	Source     string
	Channel    string
	At         time.Time
	Latency    time.Duration
	HasLatency bool
	Confidence *float64
	Threshold  *float64
}

// inputRows groups hops by input and derives one state per input, newest
// input first — the operator is almost always asking about what just
// happened.
func inputRows(events []inputEvent, now time.Time, stale time.Duration) []inputRow {
	byID := map[string][]inputEvent{}
	var order []string
	for _, e := range events {
		if _, seen := byID[e.InputID]; !seen {
			order = append(order, e.InputID)
		}
		byID[e.InputID] = append(byID[e.InputID], e)
	}
	rows := make([]inputRow, 0, len(order))
	for _, id := range order {
		hops := byID[id]
		sort.SliceStable(hops, func(i, j int) bool { return hops[i].Seq < hops[j].Seq })
		rows = append(rows, deriveInputRow(id, hops, now, stale))
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].At.After(rows[j].At) })
	return rows
}

// deriveInputRow names the last thing that actually happened to an input. A
// hop's absence is the answer to "where did it stop", so the result never
// assumes a success the ledger did not record.
func deriveInputRow(id string, hops []inputEvent, now time.Time, stale time.Duration) inputRow {
	row := inputRow{ID: id}
	if len(hops) > 0 {
		row.At, _ = parseInputTs(hops[0].Ts)
		row.Source, row.Channel = hops[0].Source, hops[0].Channel
	}
	var lastSeq uint64
	for _, e := range hops {
		if e.Seq >= lastSeq {
			lastSeq = e.Seq
			if e.Source != "" {
				row.Source = e.Source
			}
			if e.Channel != "" {
				row.Channel = e.Channel
			}
		}
		if e.Confidence != nil {
			row.Confidence, row.Threshold = e.Confidence, e.Threshold
		}
	}
	setLatency := func(e inputEvent) {
		t, ok := parseInputTs(e.Ts)
		if !ok {
			return
		}
		if !row.At.IsZero() && t.After(row.At) {
			row.Latency, row.HasLatency = t.Sub(row.At), true
			return
		}
		row.At = t
	}
	// A failure class anywhere in the input's history is the answer, even if a
	// later hop looks healthy: a refusal is not undone by being retried.
	for _, e := range hops {
		switch e.Class {
		case "refused":
			row.State, row.Why = "refused", e.Reason
		case "recogniser_error":
			row.State, row.Why = "recogniser error", e.Reason
		case "no_match":
			row.State, row.Why = "no match", e.Reason
		case "low_confidence":
			row.State, row.Why = "low confidence", confidenceWhy(e)
		case "held":
			// The action, not the judgement: a submission measured below the
			// threshold was actually stopped. Keying a hold off the class alone
			// would call a routed submission held.
			row.State, row.Why = "held", confidenceWhy(e)
		case "superseded":
			row.State, row.Why = "superseded", e.Reason
		case "unpicked":
			row.State, row.Why = "queued (unpicked)", e.Reason
		}
	}
	if row.State != "" {
		return row
	}
	switch lastStage(hops) {
	case "delivered":
		row.State = "delivered"
		for _, e := range hops {
			if e.Stage == "delivered" {
				setLatency(e)
			}
		}
	case "queued":
		row.State = "queued"
		for _, e := range hops {
			if e.Stage == "queued" {
				setLatency(e)
			}
		}
		if age := now.Sub(row.At); stale > 0 && age > stale {
			row.State, row.Why = "queued (unpicked)", "no listener picked it up in "+humanAge(age)
		}
	case "received", "interpreted", "routed":
		row.State = lastStage(hops)
		row.Why = "no later hop recorded"
		if age := now.Sub(row.At); stale > 0 && age > stale {
			row.Why = "stopped after " + row.State + " (" + humanAge(age) + ")"
		}
	default:
		row.State, row.Why = "unknown", "no recognised stage"
	}
	return row
}

func lastStage(hops []inputEvent) string {
	stage := ""
	for _, e := range hops {
		if e.Stage != "" {
			stage = e.Stage
		}
	}
	return stage
}

// confidenceWhy renders a hold so the threshold behind it is visible rather
// than implied.
func confidenceWhy(e inputEvent) string {
	if e.Confidence == nil || e.Threshold == nil {
		if e.Reason != "" {
			return e.Reason
		}
		return "held by policy"
	}
	return fmt.Sprintf("confidence %.2f below threshold %.2f", *e.Confidence, *e.Threshold)
}

// inputRowFor derives a replay's row through the same rules the live view
// uses.
func inputRowFor(hops []inputEvent, now time.Time, stale time.Duration) inputRow {
	if len(hops) == 0 {
		return inputRow{}
	}
	return deriveInputRow(hops[0].InputID, hops, now, stale)
}

// Timestamp, duration and replay-outcome rendering live in input_timefmt.go.
