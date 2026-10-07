// The fresh-clone half of `parlay claim`. Both halves of claim's printed
// brief — the no-work EXIT PROCEDURE and the work brief's recovery
// instruction — used to name `handoff` subcommands unconditionally, and
// `handoff` is a beads-store wrapper from the author's federation, NOT a
// command this repo ships (see internal/resolvehandoff's package comment).
//
// The no-work case is the worse of the two: step 2's id comes FROM step 1, so
// on a plain clone the entire exit procedure dead-ended at a command that does
// not exist — and that brief exists precisely to stop an agent from lingering
// on a pane with no work. It is also read by an agent at the moment it is
// least able to work around a missing tool.
//
// Every test here pins PATH to a directory containing ONLY a stub (or nothing),
// so the suite asserts the branch it names rather than the branch the box
// running it happens to have.
package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noStoreHandoffBrief runs a claim that finds no work, with the `handoff`
// store absent, and returns the printed brief.
func noStoreHandoffBrief(t *testing.T, taskID string) string {
	t.Helper()
	newClaimServer(t)
	stubTask(t, claimTask{}, errors.New(`Error fetching robots-aaa: no issue found matching "robots-aaa"`))
	noWorkAgent(t, "stranded")
	pinHandoffStore(t, false)
	out, _, exited := runNoWorkClaim(t, taskID)
	if !exited {
		t.Fatalf("expected a non-zero exit for an unresolvable ticket; brief:\n%s", out)
	}
	return out
}

// The exit procedure must not name a command this machine does not have, and
// must still give the agent a complete way out — including the id, which the
// store recipe used to supply as a by-product of step 1.
func TestNoWorkBriefWithoutStoreGivesAPortableExit(t *testing.T) {
	out := noStoreHandoffBrief(t, "robots-aaa")

	for _, unwanted := range []string{"handoff create", "handoff show", "<the-handoff-id-from-step-1>"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("with no `handoff` store installed, the no-work brief must not name %q:\n%s", unwanted, out)
		}
	}
	for _, want := range []string{
		"parlay drawdown 20",               // the portable stand-in for the bead
		"identity --park <any-handoff-id>", // …and the exit still works, because --park pins any id
		"WITHOUT a\nrestart",               // the reason this brief exists is unchanged
		"identity --submit",                // still named as the WRONG exit
		"identity --complete",              // …and so is this one
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no-work brief (no store) missing %q\n---\n%s", want, out)
		}
	}
	// It must say WHY the store recipe is gone, or the substituted steps look
	// arbitrary and the agent may reach for the bead anyway.
	if !strings.Contains(out, "not with parlay") {
		t.Errorf("no-work brief (no store) should say the store does not ship with parlay:\n%s", out)
	}
}

// The work brief's recovery instruction must not aim at `handoff show` where
// that command is absent. The pointer itself is a frozen on-disk format and
// still rides along in the folded identity body — only the instruction changes.
func TestClaimBriefWithoutStoreDoesNotPrescribeHandoffShow(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{ID: "task-77", Title: "Resume work"}, nil)
	t.Setenv("PARLAY_AGENT_ID", "returner")
	pinHandoffStore(t, false)

	dir := filepath.Join(os.Getenv("PARLAY_AGENT_HOME"), "returner")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	identityMD := "# Identity — returner\n\n> 📎 Handoff: handoff-abc — run `handoff show handoff-abc` for full session state\n\n- [2026-08-04] I am the returner, mid-migration.\n"
	if err := os.WriteFile(filepath.Join(dir, "identity.md"), []byte(identityMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratchpad.md"), []byte("# Scratchpad — returner\n\n- left off at step 3 of 5.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { Claim([]string{"task-77"}) })

	// The frozen pointer still appears (it is the durable on-disk format read
	// by the next session on the same box)…
	if !strings.Contains(out, "📎 Handoff: handoff-abc") {
		t.Errorf("the pinned pointer must still be folded into the brief:\n%s", out)
	}
	// …but the brief must not INSTRUCT the agent to run it. Assert on the
	// instruction SENTENCE, not the bare substring: the pointer line itself
	// contains the words "run `handoff show <id>`", and that text is frozen by
	// design (identity.pinHandoffPointer), so matching it would be asserting
	// against the artifact rather than the instruction.
	const instr = "pointer appears above, run `handoff show"
	if strings.Contains(out, instr) {
		t.Errorf("with no store installed, the brief must not tell the agent to run `handoff show`:\n%s", out)
	}
	if !strings.Contains(out, "Identity and Scratchpad bodies folded in") {
		t.Errorf("brief (no store) should say the folded bodies ARE the recovered state:\n%s", out)
	}
}

// Guard the direction of the fix: the store-PRESENT branch must keep the
// captain's recipe intact. A "fix" that made both branches portable would
// break the only environment where the recipe is correct.
func TestNoWorkBriefWithStoreKeepsTheBeadRecipe(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{}, errors.New(`Error fetching robots-aaa: no issue found matching "robots-aaa"`))
	noWorkAgent(t, "stranded")
	pinHandoffStore(t, true)

	out, _, _ := runNoWorkClaim(t, "robots-aaa")

	if !strings.Contains(out, "handoff create") ||
		!strings.Contains(out, "identity --park <the-handoff-id-from-step-1>") {
		t.Errorf("with the store installed, the bead recipe must be unchanged:\n%s", out)
	}
	if strings.Contains(out, "parlay drawdown 20") {
		t.Errorf("with the store installed, the portable substitute is noise:\n%s", out)
	}
}
