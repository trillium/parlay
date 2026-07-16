package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// actions_test.go — verb constructors emit the exact JSON wire shape the client
// dispatcher parses. Each verb is marshalled and asserted for field presence and
// omitempty absence, since the TS discriminated-union relies on that shape.

// marshal serializes an Action and returns the raw JSON string.
func marshal(t *testing.T, a Action) string {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal %s: %v", a.Verb, err)
	}
	return string(b)
}

// contains asserts substr is present in the marshalled JSON.
func contains(t *testing.T, js, substr string) {
	t.Helper()
	if !strings.Contains(js, substr) {
		t.Errorf("json %s: expected to contain %q", js, substr)
	}
}

// absent asserts substr is NOT present (omitempty verification).
func absent(t *testing.T, js, substr string) {
	t.Helper()
	if strings.Contains(js, substr) {
		t.Errorf("json %s: expected NOT to contain %q", js, substr)
	}
}

func TestIntpStrp(t *testing.T) {
	t.Parallel()
	if p := intp(5); p == nil || *p != 5 {
		t.Errorf("intp(5): got %v want *5", p)
	}
	if p := strp("x"); p == nil || *p != "x" {
		t.Errorf("strp(x): got %v want *x", p)
	}
}

func TestActSetTextShape(t *testing.T) {
	t.Parallel()
	js := marshal(t, actSetText("hello world"))
	contains(t, js, `"verb":"setText"`)
	contains(t, js, `"text":"hello world"`)
	absent(t, js, "requireTail")
	absent(t, js, "channel")
}

func TestActSetTextEmptyStringPresent(t *testing.T) {
	t.Parallel()
	// Text is a *string, so an empty string still serializes (pointer non-nil).
	// This matters: stop-speech on a whole-buffer trigger must send text:"".
	js := marshal(t, actSetText(""))
	contains(t, js, `"text":""`)
}

func TestActClearShape(t *testing.T) {
	t.Parallel()
	js := marshal(t, actClear())
	contains(t, js, `"verb":"clear"`)
	// clear carries no args; the empty args object must not leak fields.
	absent(t, js, "text")
	absent(t, js, "channel")
	absent(t, js, "reason")
}

func TestActSubmitNowShape(t *testing.T) {
	t.Parallel()
	js := marshal(t, actSubmitNow("remaining text", "bravely"))
	contains(t, js, `"verb":"submitNow"`)
	contains(t, js, `"text":"remaining text"`)
	contains(t, js, `"requireTail":"bravely"`)
}

func TestActSubmitNowEmptyTailOmitted(t *testing.T) {
	t.Parallel()
	// requireTail has no pointer; omitempty drops it when empty.
	js := marshal(t, actSubmitNow("", ""))
	contains(t, js, `"text":""`) // text is *string, empty still present
	absent(t, js, "requireTail")
}

func TestActArmCancelTimerShape(t *testing.T) {
	t.Parallel()
	arm := marshal(t, actArmTimer("submit", 1000))
	contains(t, arm, `"verb":"armTimer"`)
	contains(t, arm, `"timerId":"submit"`)
	contains(t, arm, `"fireInMs":1000`)

	cancel := marshal(t, actCancelTimer("submit"))
	contains(t, cancel, `"verb":"cancelTimer"`)
	contains(t, cancel, `"timerId":"submit"`)
	absent(t, cancel, "fireInMs")
}

func TestActHintShape(t *testing.T) {
	t.Parallel()
	show := marshal(t, actShowHint("submit-countdown", "auto-sending in 1s…", "info"))
	contains(t, show, `"verb":"showHint"`)
	contains(t, show, `"id":"submit-countdown"`)
	contains(t, show, `"kind":"info"`)
	contains(t, show, `auto-sending in 1s`)

	clear := marshal(t, actClearHint("submit-countdown"))
	contains(t, clear, `"verb":"clearHint"`)
	contains(t, clear, `"id":"submit-countdown"`)
	absent(t, clear, "kind")
	absent(t, clear, "text")
}

func TestActNoopShape(t *testing.T) {
	t.Parallel()
	js := marshal(t, actNoop("stale-request-version"))
	contains(t, js, `"verb":"noop"`)
	contains(t, js, `"reason":"stale-request-version"`)
}

func TestActTabNavSpeechShapes(t *testing.T) {
	t.Parallel()
	switchTab := marshal(t, actSwitchTab("marcus"))
	contains(t, switchTab, `"verb":"switchTab"`)
	contains(t, switchTab, `"channel":"marcus"`)

	archive := marshal(t, actArchiveTab("cato"))
	contains(t, archive, `"verb":"archiveTab"`)
	contains(t, archive, `"channel":"cato"`)

	next := marshal(t, actNextTab())
	contains(t, next, `"verb":"nextTab"`)
	absent(t, next, "channel")

	prev := marshal(t, actPrevTab())
	contains(t, prev, `"verb":"prevTab"`)
	absent(t, prev, "channel")

	nav := marshal(t, actNavigate("/status/"))
	contains(t, nav, `"verb":"navigate"`)
	contains(t, nav, `"url":"/status/"`)

	stop := marshal(t, actStopSpeech())
	contains(t, stop, `"verb":"stopSpeech"`)
	flag := marshal(t, actFlagSpeech())
	contains(t, flag, `"verb":"flagSpeech"`)
}

// ── Picker verbs — the payload the frontend renders its modal from ────────────

func TestActOpenChannelPickerShape(t *testing.T) {
	t.Parallel()
	channels := []PickerChannel{
		{Index: 1, ID: "main", Label: "main", Nickname: "main"},
		{Index: 2, ID: "cato", Label: "Cato", Nickname: ""},
	}
	js := marshal(t, actOpenChannelPicker(pickerPrompt, channels))
	contains(t, js, `"verb":"openChannelPicker"`)
	contains(t, js, `"prompt":"`+pickerPrompt+`"`)
	contains(t, js, `"index":1`)
	contains(t, js, `"id":"main"`)
	contains(t, js, `"label":"main"`)
	contains(t, js, `"index":2`)
	contains(t, js, `"id":"cato"`)
	// Empty nickname on cato still serializes (PickerChannel.Nickname has no
	// omitempty) — the frontend distinguishes "no nickname" positionally.
	contains(t, js, `"nickname":""`)
}

func TestActOpenChannelPickerRoundTrips(t *testing.T) {
	t.Parallel()
	// Full marshal → unmarshal round trip proves the picker payload is stable.
	channels := []PickerChannel{{Index: 3, ID: "x", Label: "X", Nickname: "ex"}}
	js := marshal(t, actOpenChannelPicker("prompt here", channels))
	var back Action
	if err := json.Unmarshal([]byte(js), &back); err != nil {
		t.Fatalf("round-trip unmarshal failed: %v", err)
	}
	if back.Verb != "openChannelPicker" {
		t.Errorf("verb round-trip: got %q", back.Verb)
	}
	if len(back.Args.Channels) != 1 {
		t.Fatalf("channels round-trip: got %d want 1", len(back.Args.Channels))
	}
	c := back.Args.Channels[0]
	if c.Index != 3 || c.ID != "x" || c.Label != "X" || c.Nickname != "ex" {
		t.Errorf("channel round-trip mismatch: %+v", c)
	}
	if back.Args.Prompt != "prompt here" {
		t.Errorf("prompt round-trip: got %q", back.Args.Prompt)
	}
}

func TestActCloseChannelPickerShape(t *testing.T) {
	t.Parallel()
	js := marshal(t, actCloseChannelPicker())
	contains(t, js, `"verb":"closeChannelPicker"`)
	absent(t, js, "channels")
	absent(t, js, "prompt")
}

func TestActPickerHintShape(t *testing.T) {
	t.Parallel()
	js := marshal(t, actPickerHint(`No channel matched "zxqp" — try again`))
	contains(t, js, `"verb":"pickerHint"`)
	// Text reuses the *string field per contract.
	contains(t, js, `No channel matched`)
	absent(t, js, "channels")
}

func TestActOpenChannelPickerEmptyChannels(t *testing.T) {
	t.Parallel()
	// With no tabs, channels is empty; omitempty drops the array entirely.
	js := marshal(t, actOpenChannelPicker("prompt", nil))
	contains(t, js, `"verb":"openChannelPicker"`)
	contains(t, js, `"prompt":"prompt"`)
	absent(t, js, "channels")
}
