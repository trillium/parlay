// Timestamp and duration rendering for `parlay input`. Split from the model
// so the derivation rules stay in one readable file, and because these are
// pure presentation: nothing here decides what an input's state is, only how
// a time or a gap is printed.
package commands

import (
	"fmt"
	"strings"
	"time"
)

// replayOutcome is the one-line verdict a replay ends on: the derived state,
// the total time from the first hop to the last, and why.
func replayOutcome(r inputRow, lastHop, now time.Time) string {
	total := ""
	if !r.At.IsZero() && !lastHop.IsZero() && lastHop.After(r.At) {
		total = " in " + humanAge(lastHop.Sub(r.At))
	}
	if r.Why != "" {
		return fmt.Sprintf("%s%s — %s", strings.ToUpper(r.State), total, r.Why)
	}
	return strings.ToUpper(r.State) + total
}

func parseInputTs(ts string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, false
	}
	return t.Local(), true
}

// humanAge renders a duration at the coarsest useful precision.
func humanAge(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func inputStamp(ts string) string {
	if t, ok := parseInputTs(ts); ok {
		return t.Format("2006-01-02T15:04:05.000")
	}
	return ts
}

func inputClock(ts string) string {
	if t, ok := parseInputTs(ts); ok {
		return t.Format("15:04:05.000")
	}
	return ts
}
