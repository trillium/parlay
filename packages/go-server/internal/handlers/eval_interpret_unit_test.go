package handlers

import (
	"strings"
	"testing"
)

// The pure half of the eval door's new producers: which verb counts as a
// picker miss, and that neither detail can be grown without bound by ids the
// caller supplies.
func TestEvalPickerNoMatchReadsOnlyTheVerb(t *testing.T) {
	cases := []struct {
		name    string
		actions []interface{}
		want    string
	}{
		{"nil", nil, ""},
		{"not an object", []interface{}{"pickerHint"}, ""},
		{"a real action", []interface{}{map[string]interface{}{"verb": "switchTab"}}, ""},
		{"noop", []interface{}{map[string]interface{}{"verb": "noop"}}, ""},
		{"channel hint", []interface{}{map[string]interface{}{"verb": "pickerHint"}}, reasonChannelNotMatched},
		{"sender hint", []interface{}{map[string]interface{}{"verb": "senderPickerHint"}}, reasonSenderNotMatched},
		{"hint among others", []interface{}{
			map[string]interface{}{"verb": "clear"},
			map[string]interface{}{"verb": "pickerHint", "args": map[string]interface{}{"text": "no"}},
		}, reasonChannelNotMatched},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := evalPickerNoMatch(tc.actions)
			if got != tc.want {
				t.Errorf("evalPickerNoMatch(%+v) = %q, want %q", tc.actions, got, tc.want)
			}
			if (tc.want != "") != ok {
				t.Errorf("evalPickerNoMatch(%+v) ok = %v, want %v", tc.actions, ok, tc.want != "")
			}
		})
	}
}

// evalFiredNamesACommand is the rule that keeps the engine's own overloaded
// `fired` field from being read as a command: in a picker mode it carries the
// MODE name, because the picker bypasses command matching.
func TestEvalFiredNamesACommand(t *testing.T) {
	cases := []struct {
		mode, fired string
		want        bool
	}{
		{"", "submit", true},
		{"", "clear", true},
		{"", "", false},
		{modeChannelSelect, modeChannelSelect, false},
		{modeSenderSelect, modeSenderSelect, false},
		// An engine that (hypothetically) fired a real command id from a
		// picker stream is still reported as a command: the rule keys off
		// the mode NAME, not off the mode being a picker.
		{modeChannelSelect, "clear", true},
		{modeSenderSelect, "switch-tab", true},
		// A picker mode name in a normal eval is a command id like any
		// other; nothing here guesses.
		{"", modeChannelSelect, true},
	}
	for _, tc := range cases {
		if got := evalFiredNamesACommand(tc.mode, tc.fired); got != tc.want {
			t.Errorf("evalFiredNamesACommand(%q, %q) = %v, want %v", tc.mode, tc.fired, got, tc.want)
		}
	}
}

func TestEvalInterpretDetailsAreBounded(t *testing.T) {
	d := evalFiredCommandDetail(strings.Repeat("é", 400), 7, strings.Repeat("c", 400))
	if !strings.Contains(d, "v=7") {
		t.Fatalf("detail = %q, want the version", d)
	}
	if n := len([]rune(d)); n > evalDetailStreamCap+evalDetailCommandCap+64 {
		t.Errorf("detail is %d runes, which is unbounded against caller-supplied ids: %q", n, d)
	}
	m := evalPickerNoMatchDetail(strings.Repeat("é", 400), strings.Repeat("m", 400), 1, true, 2)
	if !strings.Contains(m, "v=2") || !strings.Contains(m, "candidates=1") {
		t.Fatalf("detail = %q, want the version and candidate count", m)
	}
	if n := len([]rune(m)); n > evalDetailStreamCap+evalDetailModeCap+64 {
		t.Errorf("detail is %d runes, which is unbounded against caller-supplied ids: %q", n, m)
	}
}
