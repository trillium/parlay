package main

import (
	"reflect"
	"testing"
)

// TestInterpreterGoldenActions pins the manifest interpreter's output for every
// builtin sequence command to the exact []Action the action constructors build —
// the same actionList the deleted runAction switch produced. The `want` lists use
// the verb constructors directly, so this asserts the interpreter maps each
// command's declarative emit onto the right verbs and args (including the
// resolve-miss fall-through paths that emit nothing).
//
// submit is excluded — it is a stateful handler, exercised by the engine-level
// submit tests (TestSubmitArmsServerTimer, TestSubmitFiresServerSideAndCallsBack).
func TestInterpreterGoldenActions(t *testing.T) {
	man := embeddedManifest()
	manByID := map[string]CommandManifest{}
	for _, c := range man.Commands {
		manByID[c.ID] = c
	}

	tabs := []Tab{{ID: "marcus", Name: "Marcus Webb"}, {ID: "cato", Name: "Cato"}}
	ptabs := pickerTabs()

	cases := []struct {
		name    string
		id      string
		text    string
		tabs    []Tab
		want    []Action
		handled bool
	}{
		{"clear natural trailing", "clear", "hello world change inside input", nil, []Action{actClear()}, true},
		{"clear variant whole", "clear", "change inside in input", nil, []Action{actClear()}, true},
		{"clear upper punct", "clear", "CHANGE INSIDE INPUT!!!", nil, []Action{actClear()}, true},
		{"stop-speech strips tail", "stop-speech", "quiet down spoken pause", nil, []Action{actStopSpeech(), actSetText("quiet down")}, true},
		{"stop-speech strips to empty", "stop-speech", "spoken pause", nil, []Action{actStopSpeech(), actSetText("")}, true},
		{"flag-speech plain", "flag-speech", "flag speech", nil, []Action{actFlagSpeech(), actClear()}, true},
		{"flag-speech comma sep", "flag-speech", "flag, speech", nil, []Action{actFlagSpeech(), actClear()}, true},
		{"switch-tab hit", "switch-tab", "switch to marcus", tabs, []Action{actSwitchTab("marcus"), actClear()}, true},
		{"switch-tab miss fallthrough", "switch-tab", "switch to nobody", tabs, nil, false},
		{"switch-tab go-to hit", "switch-tab", "go to cato", tabs, []Action{actSwitchTab("cato"), actClear()}, true},
		{"archive-tab hit", "archive-tab", "archive marcus", tabs, []Action{actArchiveTab("marcus"), actClear()}, true},
		{"archive-tab miss fallthrough", "archive-tab", "archive nobody", tabs, nil, false},
		{"channel-list", "channel-list", "channel list", ptabs, []Action{actOpenChannelPicker(pickerPrompt, buildPickerChannels(ptabs)), actClear()}, true},
		{"next-tab", "next-tab", "next tab", nil, []Action{actNextTab(), actClear()}, true},
		{"prev-tab", "prev-tab", "previous agent", nil, []Action{actPrevTab(), actClear()}, true},
		{"go-to-page multiword", "go-to-page", "open my dashboard", nil, []Action{actNavigate("/my-dashboard/"), actClear()}, true},
		{"go-to-page single", "go-to-page", "workspace status", nil, []Action{actNavigate("/status/"), actClear()}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd, ok := manByID[c.id]
			if !ok {
				t.Fatalf("no manifest command for %q", c.id)
			}
			if cmd.Emit.Kind != "sequence" {
				t.Fatalf("%q is not a sequence command", c.id)
			}
			m := firstMatch(compilePhrases(cmd.Phrases, MatchMode(cmd.Mode)), c.text)
			if m == nil {
				t.Fatalf("input %q did not match any %q phrase", c.text, c.id)
			}

			var out actionList
			handled := (&Engine{}).interpretSequence(&cmd.Emit, m, MatchMode(cmd.Mode), c.tabs, &out)

			if handled != c.handled {
				t.Fatalf("handled = %v, want %v", handled, c.handled)
			}
			if !reflect.DeepEqual(out.items, c.want) {
				t.Fatalf("actions for %q %q:\n got  %+v\n want %+v", c.id, c.text, out.items, c.want)
			}
		})
	}
}

// TestEmbeddedManifestValid proves the //go:embed default parses and validates
// against the closed registries. The manifest is the ONLY command source — there
// is no compiled mirror to compare against — so this asserts structural
// invariants: schema, non-empty, unique ids, and non-empty phrase lists.
func TestEmbeddedManifestValid(t *testing.T) {
	man := embeddedManifest()
	if man.Schema != manifestSchema {
		t.Fatalf("schema = %q", man.Schema)
	}
	if len(man.Commands) == 0 {
		t.Fatal("embedded manifest has no commands")
	}
	seen := map[string]bool{}
	for _, c := range man.Commands {
		if seen[c.ID] {
			t.Fatalf("duplicate command id %q", c.ID)
		}
		seen[c.ID] = true
		if len(c.Phrases) == 0 {
			t.Fatalf("command %q has no phrases", c.ID)
		}
	}
}
