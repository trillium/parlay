// --since/--until parsing, kept beside the verb that owns those flags.
package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/timeline"
)

// parseWhen resolves --since/--until. It accepts an absolute RFC3339 stamp or
// a duration back from now ("90s", "45m", "2h", "3d"), because an operator
// pasting a log line has a stamp and an operator typing at 2am has an
// intuition about how long ago it was.
func parseWhen(s string, now time.Time) (time.Time, bool, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false, "empty"
	}
	if t, ok := timeline.ParseStamp(s); ok {
		return t, true, ""
	}
	if d, ok := parseAgo(s); ok {
		return now.Add(-d), true, ""
	}
	return time.Time{}, false, fmt.Sprintf("not an RFC3339 timestamp or a duration like 90s/45m/2h/3d")
}

// parseAgo extends time.ParseDuration with a day unit, which it lacks and
// which is the one an incident window is actually measured in.
func parseAgo(s string) (time.Duration, bool) {
	if strings.HasSuffix(s, "d") {
		var days float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%f", &days); err == nil && days > 0 {
			return time.Duration(days * float64(24*time.Hour)), true
		}
		return 0, false
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}
