// Tests for the SPAWN-time bead binding (beads-required mode): --register
// recording `bead:`, the bead-over-task resolution order, and --submit closing
// the bead while --park deliberately never does.
//
// Both store shell-outs are stubbed (workItemStatus, closeWorkItem) so no real
// federation store is touched — on the captain's box those wrappers exist and
// would otherwise close a live bead.
package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubCloseWorkItem swaps the close shell-out for the test's duration and
// returns a pointer to the slice of ids it was asked to close.
func stubCloseWorkItem(t *testing.T, err error) *[]string {
	t.Helper()
	var closed []string
	orig := closeWorkItem
	closeWorkItem = func(id string) error {
		closed = append(closed, id)
		return err
	}
	t.Cleanup(func() { closeWorkItem = orig })
	return &closed
}

// recordingContextReset puts shims for BOTH "context-reset" and "reincarnate"
// on PATH. ContextResetCmd() prefers "context-reset" when it is on PATH, so
// stubbing only "reincarnate" leaves the real binary reachable on the
// captain's box — which would close the herdr pane and SIGHUP the test
// process. Both names must be intercepted.
func recordingContextReset(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "reset-args")
	script := "#!/bin/sh\necho \"$@\" >> " + argsFile +
		"\necho \"${PARLAY_PINNED_HANDOFF-<unset>}\" >> " + argsFile + ".env\nexit 0\n"
	for _, name := range []string{"reincarnate", "context-reset"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

// resetArgs returns the recorded reset invocations ("" when it never ran).
func resetArgs(t *testing.T, argsFile string) string {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// resetPin returns the PARLAY_PINNED_HANDOFF the reset actually saw — the
// transport the pinned id travels on, so an argv assertion alone would no
// longer prove the id reached the script ("" when the reset never ran,
// "<unset>" when it ran without the variable).
func resetPin(t *testing.T, argsFile string) string {
	t.Helper()
	data, err := os.ReadFile(argsFile + ".env")
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestRegisterRecordsBoundBead(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})

	logs, code, exited := runCapturingExit(t, func() {
		CmdIdentity([]string{"--register", "--agent", "worker", "--bead", "task-oyaj"})
	})
	// A --bead missing from MemValueFlags would make args.Parse die EXIT_USAGE
	// here (robots-6xq7's shape), writing no frontmatter at all — so this
	// assertion is the regression pin for store.go's flag table, not padding.
	if exited {
		t.Fatalf("--register --bead exited (code %d) — is --bead in MemValueFlags? logs: %s", code, logs)
	}
	fm := ReadFrontmatter(filepath.Join(home, "worker", "identity.md"))
	if got := fm.Get(BeadKey); got != "task-oyaj" {
		t.Fatalf("bead binding = %q, want task-oyaj", got)
	}
	// The rest of the launch spec, and the body, must survive.
	if got := fm.Get("name"); got != "worker" {
		t.Errorf("name = %q, want worker (register must not clobber unrelated fields)", got)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "worker", "identity.md"))
	if !strings.Contains(string(raw), "a durable fact") {
		t.Errorf("expected identity body preserved, got:\n%s", raw)
	}
}

func TestBoundWorkItemPrefersBeadOverTask(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	file := filepath.Join(home, "worker", "identity.md")

	// Claim-time binding only.
	if err := BindWorkItem("worker", "robots-2x2n"); err != nil {
		t.Fatal(err)
	}
	if got := BoundWorkItem(file); got != "robots-2x2n" {
		t.Fatalf("with only a task binding: got %q, want robots-2x2n", got)
	}

	// Spawn-time binding wins once present.
	fm := ReadFrontmatter(file)
	fm.Set(BeadKey, "task-oyaj")
	if err := WriteFrontmatter(file, fm); err != nil {
		t.Fatal(err)
	}
	if got := BoundWorkItem(file); got != "task-oyaj" {
		t.Fatalf("with both bindings: got %q, want task-oyaj", got)
	}

	// And the closed-item oracle asks about THAT id, not the task.
	var asked []string
	stubWorkItemStatus(t, func(id string) (string, error) {
		asked = append(asked, id)
		return "open", nil
	})
	item, closed := BoundWorkItemClosed(file)
	if closed || item != "task-oyaj" {
		t.Fatalf("BoundWorkItemClosed = (%q, %v), want (task-oyaj, false)", item, closed)
	}
	if len(asked) != 1 || asked[0] != "task-oyaj" {
		t.Fatalf("status was resolved for %v, want [task-oyaj]", asked)
	}
}

func TestSubmitClosesBoundBeadAndEndsWithoutRelaunch(t *testing.T) {
	argsFile := recordingContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")
	fm := ReadFrontmatter(filepath.Join(home, "worker", "identity.md"))
	fm.Set(BeadKey, "task-oyaj")
	if err := WriteFrontmatter(filepath.Join(home, "worker", "identity.md"), fm); err != nil {
		t.Fatal(err)
	}
	stubWorkItemStatus(t, func(string) (string, error) { return "open", nil })
	closed := stubCloseWorkItem(t, nil)

	logs := captureStdout(t, func() { CmdIdentity([]string{"--submit", "handoff-abc"}) })

	if len(*closed) != 1 || (*closed)[0] != "task-oyaj" {
		t.Fatalf("closed = %v, want [task-oyaj]", *closed)
	}
	if !strings.Contains(logs, "closed bound bead task-oyaj") {
		t.Errorf("expected the close to be reported, got: %s", logs)
	}
	if !strings.Contains(logs, "WITHOUT relaunch") {
		t.Errorf("closing the bead must end the lifecycle, got: %s", logs)
	}
	// The evidence that matters is the reset's ARGV, not the message beside it.
	if got := resetArgs(t, argsFile); got != "" {
		t.Errorf("reset ran with %q, want no args (--reboot must be dropped)", got)
	}
	if got := resetPin(t, argsFile); got != "handoff-abc" {
		t.Errorf("reset saw PARLAY_PINNED_HANDOFF=%q, want handoff-abc", got)
	}
	// The handoff pointer is still pinned — the state stays recoverable.
	raw, _ := os.ReadFile(filepath.Join(home, "worker", "identity.md"))
	if !strings.Contains(string(raw), "📎 Handoff: handoff-abc") {
		t.Errorf("expected handoff pinned on a bead-closing submit, got:\n%s", raw)
	}
}

func TestSubmitStillResetsWhenBeadCloseFails(t *testing.T) {
	argsFile := recordingContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")
	file := filepath.Join(home, "worker", "identity.md")
	fm := ReadFrontmatter(file)
	fm.Set(BeadKey, "task-oyaj")
	if err := WriteFrontmatter(file, fm); err != nil {
		t.Fatal(err)
	}
	stubWorkItemStatus(t, func(string) (string, error) { return "open", nil })
	stubCloseWorkItem(t, errString("store unreachable"))

	logs := captureStdout(t, func() { CmdIdentity([]string{"--submit", "handoff-abc"}) })

	if !strings.Contains(logs, "could not close bound bead task-oyaj") {
		t.Errorf("expected a loud warning on a failed close, got: %s", logs)
	}
	// Fail-open: the bead is still open, so the agent reboots as usual. A close
	// failure must never strand a legitimate context rotation.
	if strings.Contains(logs, "WITHOUT relaunch") {
		t.Errorf("a failed close must not downgrade the submit, got: %s", logs)
	}
	if got := resetArgs(t, argsFile); got != "--reboot" {
		t.Errorf("reset ran with %q, want --reboot", got)
	}
	if got := resetPin(t, argsFile); got != "handoff-abc" {
		t.Errorf("reset saw PARLAY_PINNED_HANDOFF=%q, want handoff-abc", got)
	}
}

func TestSubmitDryRunDoesNotCloseBead(t *testing.T) {
	recordingContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")
	file := filepath.Join(home, "worker", "identity.md")
	fm := ReadFrontmatter(file)
	fm.Set(BeadKey, "task-oyaj")
	if err := WriteFrontmatter(file, fm); err != nil {
		t.Fatal(err)
	}
	stubWorkItemStatus(t, func(string) (string, error) { return "open", nil })
	closed := stubCloseWorkItem(t, nil)

	logs := captureStdout(t, func() { CmdIdentity([]string{"--submit", "handoff-abc", "--dry"}) })

	if len(*closed) != 0 {
		t.Fatalf("--dry must not close anything, closed = %v", *closed)
	}
	if !strings.Contains(logs, "would close bound bead task-oyaj") {
		t.Errorf("expected the dry run to PREVIEW the close, got: %s", logs)
	}
}

// legacyContextReset stands in for a context-reset resolved on PATH from an
// older checkout: it understands only the flags that predate the pinned-handoff
// work and refuses anything else with exit 2, before doing any of its job. That
// refusal is invisible to the caller, which inspects only the start error — so a
// reset argv this shim rejects is a park/submit that reports success while the
// session keeps running.
func legacyContextReset(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "reset-args")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in --reboot|--dry|--cmd) ;; *) echo \"context-reset: unknown arg: $a\" >&2; exit 2 ;; esac\n" +
		"done\n" +
		"echo \"$@\" >> " + argsFile + "\n" +
		"echo \"${PARLAY_PINNED_HANDOFF-<unset>}\" >> " + argsFile + ".env\n" +
		"exit 0\n"
	for _, name := range []string{"reincarnate", "context-reset"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

func TestParkResetsThroughAContextResetThatPredatesThePin(t *testing.T) {
	argsFile := legacyContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")

	captureStdout(t, func() { CmdIdentity([]string{"--park", "handoff-abc"}) })

	if got := resetPin(t, argsFile); got == "" {
		t.Fatalf("the reset never ran to completion — an older context-reset refused this argv, and --park reported success anyway")
	} else if got != "handoff-abc" {
		t.Errorf("reset saw PARLAY_PINNED_HANDOFF=%q, want handoff-abc", got)
	}
	if got := resetArgs(t, argsFile); got != "" {
		t.Errorf("park reset ran with %q, want no args", got)
	}
}

func TestSubmitResetsThroughAContextResetThatPredatesThePin(t *testing.T) {
	argsFile := legacyContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")

	captureStdout(t, func() { CmdIdentity([]string{"--submit", "handoff-abc"}) })

	if got := resetPin(t, argsFile); got == "" {
		t.Fatalf("the reset never ran to completion — an older context-reset refused this argv, and --submit reported success anyway")
	} else if got != "handoff-abc" {
		t.Errorf("reset saw PARLAY_PINNED_HANDOFF=%q, want handoff-abc", got)
	}
	if got := resetArgs(t, argsFile); got != "--reboot" {
		t.Errorf("reset ran with %q, want --reboot", got)
	}
}

func TestParkNeverClosesBoundBead(t *testing.T) {
	argsFile := recordingContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")
	file := filepath.Join(home, "worker", "identity.md")
	fm := ReadFrontmatter(file)
	fm.Set(BeadKey, "task-oyaj")
	if err := WriteFrontmatter(file, fm); err != nil {
		t.Fatal(err)
	}
	stubWorkItemStatus(t, func(string) (string, error) { return "open", nil })
	closed := stubCloseWorkItem(t, nil)

	logs := captureStdout(t, func() { CmdIdentity([]string{"--park", "handoff-abc"}) })

	// Parking is "pause, resume later" — the whole middle exit of the
	// three-exit model. Closing the bead here would end a lifecycle the
	// operator explicitly asked to suspend, and nothing would relaunch it.
	if len(*closed) != 0 {
		t.Fatalf("--park must never close the bead, closed = %v", *closed)
	}
	if !strings.Contains(logs, "bead left OPEN") {
		t.Errorf("expected the park message to state the bead is left open, got: %s", logs)
	}
	if got := resetArgs(t, argsFile); got != "" {
		t.Errorf("park reset ran with %q, want no args", got)
	}
	if got := resetPin(t, argsFile); got != "handoff-abc" {
		t.Errorf("park reset saw PARLAY_PINNED_HANDOFF=%q, want handoff-abc", got)
	}
	// And the binding itself survives, so a later spawn still resolves it.
	if got := ReadFrontmatter(file).Get(BeadKey); got != "task-oyaj" {
		t.Errorf("bead binding = %q after --park, want task-oyaj", got)
	}
}
