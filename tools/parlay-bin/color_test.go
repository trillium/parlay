package main

import "testing"

// Golden values cross-checked against packages/cli/src/identity-ephemeral.ts's
// colorFromId (the JS implementation of the same FNV-1a algorithm) — see
// docs/scope-go-spawn.md §5's warning that all three implementations
// (JS/bash/Go) must stay bit-identical.
func TestColorFromIdMatchesJSImplementation(t *testing.T) {
	cases := map[string]string{
		"code-reviewer": "#283648",
		"fix-a":         "#5d35a7",
		"add-b":         "#a3ac4c",
		"a":             "#545134",
		"batch-agent-9": "#48b829",
	}
	for id, want := range cases {
		if got := colorFromId(id); got != want {
			t.Errorf("colorFromId(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestColorFromIdDeterministic(t *testing.T) {
	id := "some-agent-id"
	first := colorFromId(id)
	for i := 0; i < 5; i++ {
		if got := colorFromId(id); got != first {
			t.Fatalf("colorFromId(%q) not deterministic: %q vs %q", id, got, first)
		}
	}
}
