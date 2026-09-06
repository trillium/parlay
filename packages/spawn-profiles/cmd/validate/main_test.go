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

// The pi + muse-spark launch path (fleet Claude subscription down;
// verified live 2026-09-06): both the paid-pool and free forms must exist
// with kind/command pi and their exact model ids. prompt_mode "arg" pins
// pi's positional-message shape (pi takes messages as argv, unlike
// opencode's --prompt flag), so a future threading of prompt_mode cannot
// silently deliver the charter via a flag pi does not accept.
func TestPiMuseSparkProfiles(t *testing.T) {
	var f profilesFile
	if _, err := toml.DecodeFile("../../profiles.toml", &f); err != nil {
		t.Fatalf("cannot parse profiles.toml: %v", err)
	}

	wantModels := map[string]string{
		"pi-muse-spark":      "opencode-go/muse-spark-1.3-contributor",
		"pi-muse-spark-free": "opencode/muse-spark-1.3-contributor-free",
	}
	for name, wantModel := range wantModels {
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
		if p.Kind != "pi" {
			t.Errorf("profile %q kind = %q, want %q", name, p.Kind, "pi")
		}
		if p.Command != "pi" {
			t.Errorf("profile %q command = %q, want %q", name, p.Command, "pi")
		}
		if p.Model != wantModel {
			t.Errorf("profile %q model = %q, want %q", name, p.Model, wantModel)
		}
		if p.PromptMode != "arg" {
			t.Errorf("profile %q prompt_mode = %q, want %q", name, p.PromptMode, "arg")
		}
	}
}
