package main

import (
	"slices"
	"testing"

	"github.com/BurntSushi/toml"
)

// A regression that removes or changes the "--account 2" argument from the
// claude/claude-plan profiles would still pass the field-shape checks in
// main.go — those check that args is well-formed, not what it contains.
// This pins the actual routing value.
func TestClaudeProfilesRouteToAccount2(t *testing.T) {
	var f profilesFile
	if _, err := toml.DecodeFile("../../profiles.toml", &f); err != nil {
		t.Fatalf("cannot parse profiles.toml: %v", err)
	}

	for _, name := range []string{"claude", "claude-plan"} {
		var p *profile
		for i := range f.Profile {
			if f.Profile[i].Name == name {
				p = &f.Profile[i]
				break
			}
		}
		if p == nil {
			t.Fatalf("profile %q not found in profiles.toml", name)
		}
		if !slices.Contains(p.Args, "--account") || !slices.Contains(p.Args, "2") {
			t.Errorf("profile %q args %v: expected \"--account\" \"2\"", name, p.Args)
		}
	}
}
