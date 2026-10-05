package help

import (
	"strings"
	"testing"
)

func TestUsageSubstitutesServer(t *testing.T) {
	got := Usage("http://example.test:1234")
	if !strings.Contains(got, "http://example.test:1234") {
		t.Errorf("Usage() missing server URL: %q", got)
	}
	if strings.Contains(got, "{{SERVER}}") {
		t.Errorf("Usage() left the placeholder unsubstituted: %q", got)
	}
}

func TestLookupKnownCommand(t *testing.T) {
	text, ok := Lookup("status")
	if !ok {
		t.Fatal("expected help text for status")
	}
	if !strings.Contains(text, "parlay status") {
		t.Errorf("Lookup(status) = %q", text)
	}
}

func TestLookupUnknownCommand(t *testing.T) {
	if _, ok := Lookup("does-not-exist"); ok {
		t.Error("expected ok=false for an unregistered command")
	}
}

// `handoff` is a beads-store wrapper from the author's federation, NOT a
// command this repo installs. Help text is static, so it can never probe
// PATH: the honest form is to show the store-present recipe AND say plainly
// that the store does not ship here. This gate enforces exactly that — a help
// entry may name the command only if the same entry carries the caveat — so a
// new entry that teaches the command without qualifying it fails the build.
func TestHelpNamingHandoffCreateAlsoCarriesTheNotInstalledCaveat(t *testing.T) {
	const caveat = "NOT something this repo installs"
	for verb, text := range HELP {
		if !strings.Contains(text, "handoff create") && !strings.Contains(text, "handoff show") {
			continue
		}
		if !strings.Contains(text, caveat) {
			t.Errorf("parlay %s --help names a `handoff` subcommand but never says %q — a reader on a\nplain clone would run a command this repo does not install:\n%s", verb, caveat, text)
		}
	}
	if strings.Contains(Usage("http://example.test:1234"), "handoff create") {
		t.Error("parlay help's usage block still says `for 'handoff create'`")
	}
}
