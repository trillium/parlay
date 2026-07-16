package main

import (
	"reflect"
	"testing"
)

// TestInterpreterMatchesRunAction is the equivalence oracle for Step 1/2: for every
// builtin command and a battery of representative inputs (including the resolve-miss
// fall-through paths), the manifest interpreter must produce the EXACT same
// (handled, []Action) result the hand-written runAction switch produces. Once this
// passes, each runAction case can be deleted knowing the data path is proven equal.
//
// submit is excluded here — it is a stateful handler (emit.kind=="handler"), not a
// sequence; its equivalence is covered by the engine-level submit tests.
func TestInterpreterMatchesRunAction(t *testing.T) {
	man := embeddedManifest()
	manByID := map[string]CommandManifest{}
	for _, c := range man.Commands {
		manByID[c.ID] = c
	}
	specByID := map[string]commandSpec{}
	for _, s := range builtins {
		specByID[s.id] = s
	}

	tabs := []Tab{{ID: "marcus", Name: "Marcus Webb"}, {ID: "cato", Name: "Cato"}}

	cases := []struct {
		name string
		id   string
		text string
		tabs []Tab
	}{
		{"clear natural trailing", "clear", "hello world change inside input", nil},
		{"clear variant whole", "clear", "change inside in input", nil},
		{"clear upper punct", "clear", "CHANGE INSIDE INPUT!!!", nil},
		{"stop-speech strips tail", "stop-speech", "quiet down spoken pause", nil},
		{"stop-speech strips to empty", "stop-speech", "spoken pause", nil},
		{"flag-speech plain", "flag-speech", "flag speech", nil},
		{"flag-speech comma sep", "flag-speech", "flag, speech", nil},
		{"switch-tab hit", "switch-tab", "switch to marcus", tabs},
		{"switch-tab miss fallthrough", "switch-tab", "switch to nobody", tabs},
		{"switch-tab go-to hit", "switch-tab", "go to cato", tabs},
		{"archive-tab hit", "archive-tab", "archive marcus", tabs},
		{"archive-tab miss fallthrough", "archive-tab", "archive nobody", tabs},
		{"channel-list", "channel-list", "channel list", pickerTabs()},
		{"channel-list nicknames", "channel-list", "list channels", pickerTabs()},
		{"next-tab", "next-tab", "next tab", nil},
		{"prev-tab", "prev-tab", "previous agent", nil},
		{"go-to-page multiword", "go-to-page", "open my dashboard", nil},
		{"go-to-page single", "go-to-page", "workspace status", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, ok := specByID[c.id]
			if !ok {
				t.Fatalf("no builtin spec for %q", c.id)
			}
			cmd, ok := manByID[c.id]
			if !ok {
				t.Fatalf("no manifest command for %q", c.id)
			}
			if cmd.Emit.Kind != "sequence" {
				t.Fatalf("%q is not a sequence command", c.id)
			}

			// Match using the command's phrases (spec and manifest share phrases).
			m := firstMatch(compilePhrases(spec.phrases, spec.mode), c.text)
			if m == nil {
				t.Fatalf("input %q did not match any %q phrase", c.text, c.id)
			}

			var oldOut actionList
			handledOld := runAction(spec, m, c.tabs, &oldOut)

			var newOut actionList
			handledNew := (&Engine{}).interpretSequence(&cmd.Emit, m, spec.mode, c.tabs, &newOut)

			if handledOld != handledNew {
				t.Fatalf("handled mismatch: runAction=%v interpreter=%v", handledOld, handledNew)
			}
			if !reflect.DeepEqual(oldOut.items, newOut.items) {
				t.Fatalf("action mismatch for %q %q:\n runAction=%+v\n interp   =%+v",
					c.id, c.text, oldOut.items, newOut.items)
			}
		})
	}
}

// TestEmbeddedManifestValid proves the //go:embed default parses and validates
// against the closed registries, and that it encodes exactly the ten builtins.
func TestEmbeddedManifestValid(t *testing.T) {
	man := embeddedManifest()
	if man.Schema != manifestSchema {
		t.Fatalf("schema = %q", man.Schema)
	}
	if len(man.Commands) != len(builtins) {
		t.Fatalf("embedded manifest has %d commands, builtins has %d", len(man.Commands), len(builtins))
	}
	specByID := map[string]commandSpec{}
	for _, s := range builtins {
		specByID[s.id] = s
	}
	for _, c := range man.Commands {
		s, ok := specByID[c.ID]
		if !ok {
			t.Fatalf("manifest command %q has no matching builtin", c.ID)
		}
		if c.Priority != s.priority {
			t.Fatalf("%q priority = %d, want %d", c.ID, c.Priority, s.priority)
		}
		if MatchMode(c.Mode) != s.mode {
			t.Fatalf("%q mode = %q, want %q", c.ID, c.Mode, s.mode)
		}
		if !reflect.DeepEqual(c.Phrases, s.phrases) {
			t.Fatalf("%q phrases = %v, want %v", c.ID, c.Phrases, s.phrases)
		}
	}
}
