// Rendering half of the listener join: the fleet-wide block under the table.
// It is a separate file from the classification because the two answer
// different questions — the rows answer "why is THIS input stuck", the block
// answers "is my fleet listening at all" — and neither can be inferred from
// the other. Every rule it prints comes from classifyListener and the same
// cursor fact the rows use, so a row and this block cannot disagree.
package commands

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// maxListenerRows bounds the block. A fleet can have many channels; the rows
// are sorted and the count omitted is printed rather than dropped silently.
const maxListenerRows = 8

// renderInputListeners prints who has been asking each channel for messages.
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
			cell(f.Channel, 18), f.ActivePollers, age, listenerVerdict(f, st))
	}
	if n := len(sorted) - len(shown); n > 0 {
		fmt.Fprintf(w, "  (+%d more channel(s) not shown)\n", n)
	}
	printListenerHold(w, p.pollHold())
}

// listenerVerdict phrases one classification for the block, naming the one
// mechanism the block can prove: for a channel that IS being asked, a last
// poll that carried no backlog cursor means the listener in front of a
// waiting input could not have reached it.
func listenerVerdict(f channelListener, st listenerState) string {
	base := ""
	switch st.Kind {
	case "parked":
		base = "a listener is waiting on it right now"
	case "never":
		base = "never polled since the server started"
	case "unreadable":
		base = "last poll time unreadable"
	case "quiet":
		base = "nothing is polling it"
	default:
		base = "a listener is attached"
	}
	if st.Kind == "parked" || st.Kind == "attached" {
		if f.LastPollCursored != nil && !*f.LastPollCursored {
			base += " (but its last poll carried no backlog cursor)"
		}
	}
	return base
}

// printListenerHold states the window behind the classification, so "nothing
// is polling it" is a claim with its number beside it rather than a hunch.
func printListenerHold(w io.Writer, hold time.Duration) {
	fmt.Fprintf(w, "  a listener that is attached asks at least once per %s (the server holds a\n",
		humanAge(hold))
	fmt.Fprintf(w, "  parked poll that long), so a channel quiet for more than %s has nothing polling it.\n",
		humanAge(2*hold))
}
