package format

import (
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/wire"
)

func TestTruncateShortTextUnchanged(t *testing.T) {
	if got := Truncate("hello", 0); got != "hello" {
		t.Errorf("Truncate() = %q", got)
	}
}

func TestTruncateCollapsesNewlines(t *testing.T) {
	got := Truncate("line one\nline two", 0)
	if got != "line one ⏎ line two" {
		t.Errorf("Truncate() = %q", got)
	}
}

func TestTruncateClipsLongText(t *testing.T) {
	text := strings.Repeat("a", 150)
	got := Truncate(text, 100)
	want := strings.Repeat("a", 100) + "… (+50 chars)"
	if got != want {
		t.Errorf("Truncate() = %q, want %q", got, want)
	}
}

func TestTruncateCountsUTF16CodeUnitsNotRunes(t *testing.T) {
	// "😀" is one Go rune but two UTF-16 code units (a surrogate pair), like
	// the TS original's JS string .length. 99 "a"s + the emoji is 100 runes
	// but 101 UTF-16 units, so it must clip against max=100 the same way the
	// TS original's truncate() would.
	text := strings.Repeat("a", 99) + "😀"
	got := Truncate(text, 100)
	if !strings.HasSuffix(got, "(+1 chars)") {
		t.Errorf("Truncate() = %q, want a clip with suffix (+1 chars)", got)
	}
}

func TestPadEndCountsUTF16CodeUnitsNotRunes(t *testing.T) {
	// "😀" is one Go rune but two UTF-16 code units, so padEnd(4) in JS pads
	// with two spaces, not three.
	got := PadEnd("😀", 4)
	want := "😀" + "  "
	if got != want {
		t.Errorf("PadEnd() = %q, want %q", got, want)
	}
}

func TestWhoAgentUsesChannel(t *testing.T) {
	m := wire.ChatMessage{Role: "agent", Channel: "mayor"}
	if got := Who(m); got != "mayor" {
		t.Errorf("Who() = %q, want mayor", got)
	}
}

func TestWhoAgentNoChannelFallsBack(t *testing.T) {
	m := wire.ChatMessage{Role: "agent"}
	if got := Who(m); got != "agent" {
		t.Errorf("Who() = %q, want agent", got)
	}
}

func TestWhoAlert(t *testing.T) {
	m := wire.ChatMessage{Role: "user", Type: "alert"}
	if got := Who(m); got != "alert" {
		t.Errorf("Who() = %q, want alert", got)
	}
}

func TestWhoDefaultsToYou(t *testing.T) {
	m := wire.ChatMessage{Role: "user"}
	if got := Who(m); got != "you" {
		t.Errorf("Who() = %q, want you", got)
	}
}

func TestFmtMsgTruncatedForm(t *testing.T) {
	m := wire.ChatMessage{ID: "1", Role: "user", Ts: "2026-08-01T12:34:56Z", Text: "hi there"}
	got := FmtMsg(m, false)
	if !strings.HasPrefix(got, "[12:34:56] you") {
		t.Errorf("FmtMsg() = %q", got)
	}
	if !strings.Contains(got, "hi there") {
		t.Errorf("FmtMsg() = %q, want text included", got)
	}
}

func TestFmtMsgFullForm(t *testing.T) {
	m := wire.ChatMessage{ID: "42", Role: "agent", Channel: "mayor", Ts: "2026-08-01T12:34:56Z", Text: "status update"}
	got := FmtMsg(m, true)
	if !strings.Contains(got, "id=42") || !strings.Contains(got, "channel=mayor") {
		t.Errorf("FmtMsg(full) = %q", got)
	}
	if !strings.Contains(got, "\n  status update") {
		t.Errorf("FmtMsg(full) = %q, want text on its own indented line", got)
	}
}

func TestFmtMsgShortTimestamp(t *testing.T) {
	m := wire.ChatMessage{Role: "user", Ts: "short", Text: "x"}
	got := FmtMsg(m, false)
	if !strings.HasPrefix(got, "[] you") {
		t.Errorf("FmtMsg() with short ts = %q, want empty bracketed ts", got)
	}
}
