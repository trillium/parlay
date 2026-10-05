// `identity --launch` reconstitutes an agent from its launch spec and hands
// the respawned process a RECOVERY PROMPT — the charter it wakes with after a
// context reset. It used to name `handoff show <that-id>` unconditionally,
// which is a command from the author's federation that this repo does not
// install.
//
// This surface is worse than a diagnostic the reader can dismiss: it is read
// by a live agent at exactly the moment it has least capacity to discover that
// the command does not exist. The state it wants IS the handoff body, so a
// missing command reads as amnesia — and an agent that believes it has lost
// its state goes looking for work to redo rather than resuming.
//
// Tests pin PATH to a directory containing only a stub so each asserts the
// branch it names.
package identity

import (
	"strings"
	"testing"
)

func TestRespawnRecoveryPromptWithoutStoreNamesThePortableLeg(t *testing.T) {
	pinHandoffStore(t, false)
	got := respawnRecoveryPrompt("worker")

	if strings.Contains(got, "handoff show") {
		t.Errorf("a store-less respawn must not be told to run `handoff show`:\n%s", got)
	}
	// The portable legs, and the reason the usual one is unavailable.
	for _, want := range []string{
		"worker",                             // it still knows who it is
		"run 'identity'",                     // the store-independent half of the chain
		"'scratchpad'",                       // …and the other
		"no `handoff` store on this machine", // so the substitution is not mysterious
		"parlay drawdown",                    // the portable way to get a handoff body
		"resume where you left off",          // the brief's actual purpose survives
	} {
		if !strings.Contains(got, want) {
			t.Errorf("store-less recovery prompt missing %q:\n%s", want, got)
		}
	}
}

// Guard the direction of the fix: with the store installed the captain's
// three-step chain must survive verbatim. Making both branches portable would
// be a silent regression on every machine where the recipe is correct.
func TestRespawnRecoveryPromptWithStoreKeepsTheThreeStepChain(t *testing.T) {
	pinHandoffStore(t, true)
	got := respawnRecoveryPrompt("worker")

	for _, want := range []string{
		"run 'identity'",
		"then 'handoff show <that-id>' for full state",
		"then 'scratchpad'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("store-present recovery prompt missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "no `handoff` store") {
		t.Errorf("store-present recovery prompt should not carry the absent-store caveat:\n%s", got)
	}
}
