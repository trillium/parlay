// Package format is the parlay CLI's message formatting: truncation, sender
// label, and line rendering.
//
// Ported field-for-field from packages/cli/src/format.ts.
package format

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// Truncate collapses newlines to " ⏎ " and clips text to max UTF-16 code
// units — matching JS string .length/.slice() semantics from the TS
// original (format.ts's truncate()), not Go's rune count — adding a
// "(+N chars)" suffix when it clips. Pass 0 to use config.TruncateAt.
func Truncate(text string, max int) string {
	if max == 0 {
		max = config.TruncateAt
	}
	oneLine := strings.ReplaceAll(text, "\n", " ⏎ ")
	units := utf16.Encode([]rune(oneLine))
	if len(units) <= max {
		return oneLine
	}
	return fmt.Sprintf("%s… (+%d chars)", string(utf16.Decode(units[:max])), len(units)-max)
}

// PadEnd right-pads s with spaces to width UTF-16 code units, matching JS's
// String.prototype.padEnd(width) — not Go's rune count.
func PadEnd(s string, width int) string {
	n := len(utf16.Encode([]rune(s)))
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// Who returns the display label for a message's sender: the agent's channel
// for agent messages, "alert" for a broadcast alert, else "you".
func Who(m wire.ChatMessage) string {
	if m.Role == "agent" {
		if m.Channel != "" {
			return m.Channel
		}
		return "agent"
	}
	if m.Type == "alert" {
		return "alert"
	}
	return "you"
}

// FmtMsg renders one line for a message: truncated by default, or a
// multi-line detail block (timestamp, sender, id, channel, full text) when
// full is true.
func FmtMsg(m wire.ChatMessage, full bool) string {
	ts := jsSlice(m.Ts, 11, 19)
	who := Who(m)
	if full {
		channel := m.Channel
		if channel == "" {
			channel = "-"
		}
		return fmt.Sprintf("[%s] %-12s id=%s channel=%s\n  %s", ts, who, m.ID, channel, m.Text)
	}
	return fmt.Sprintf("[%s] %-12s %s", ts, who, Truncate(m.Text, 0))
}

// NextStep prints a "Next: <template>" hint line to stdout.
func NextStep(template string) {
	fmt.Printf("\nNext: %s\n", template)
}

// jsSlice mirrors JS String.prototype.slice(start, end) for non-negative
// indices: both are clamped to len(s), and start >= end yields "".
func jsSlice(s string, start, end int) string {
	if start > len(s) {
		start = len(s)
	}
	if end > len(s) {
		end = len(s)
	}
	if start >= end {
		return ""
	}
	return s[start:end]
}
