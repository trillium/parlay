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

// The listener takeover guard (`singleton.go`) matches an agent ID in this
// HOST's process table — it cannot tell which server or state dir a candidate
// listener belongs to. Verified 2026-10-05: two isolated instances on
// different servers, same agent id, and the second `listen` SIGTERMed the
// first's listener, leaving it registered but deaf. Both enrolling verbs
// therefore have to say so where a reader actually sees it, and both have to
// name the opt-out (`PARLAY_LISTEN_NO_SINGLETON=1`); "one listener per
// channel" is the same words the bug shipped with.
func TestListenerHelpStatesTheTakeoverIsHostWide(t *testing.T) {
	for _, verb := range []string{"listen", "monitor"} {
		text, ok := Lookup(verb)
		if !ok {
			t.Fatalf("no help entry for %q", verb)
		}
		for _, want := range []string{"HOST-WIDE", "PARLAY_LISTEN_NO_SINGLETON=1"} {
			if !strings.Contains(text, want) {
				t.Errorf("parlay %s --help does not say %q — a reader running two instances on\none host cannot learn that this verb evicts the other instance's listener:\n%s", verb, want, text)
			}
		}
		if strings.Contains(text, "one channel keeps exactly one reader") {
			t.Errorf("parlay %s --help still scopes the takeover to a channel, which reads as\nper-instance; it is per agent ID across every instance on the host", verb)
		}
	}
}

// The relay-backed enroll path has its own cross-instance failure mode that the
// takeover wording does not cover: the relay is one process per user on a
// host-wide runtime dir and binds ONE -server for life, so two instances share
// it. Without this in the help, a second instance's `listen` enrolls into a
// relay polling the FIRST instance's server and comes up registered-but-deaf
// with no error anywhere — a strictly worse failure than the eviction the
// SCOPE paragraph above does describe.
func TestListenerHelpNamesTheRelayServerBinding(t *testing.T) {
	for _, verb := range []string{"listen", "monitor"} {
		text, ok := Lookup(verb)
		if !ok {
			t.Fatalf("no help entry for %q", verb)
		}
		for _, want := range []string{"PARLAY_RELAY_RUNTIME", "--legacy-poll", "PARLAY_SERVER"} {
			if !strings.Contains(text, want) {
				t.Errorf("parlay %s --help does not name %q — a reader running two instances\ncannot learn that the relay they would enroll through is bound to one\nserver and may not be the one this CLI is pointed at:\n%s", verb, want, text)
			}
		}
		if !strings.Contains(text, "preflight") {
			t.Errorf("parlay %s --help does not mention the preflight that refuses a\nserver-mismatched relay before anything is registered:\n%s", verb, text)
		}
	}
}
