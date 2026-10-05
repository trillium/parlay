// The fresh-clone handoff gap: `handoff` is a beads-store wrapper from the
// author's federation (the same family as task/inbox), NOT a command this repo
// ships. ResolveCurrentHandoff deliberately collapses "nothing open" and "no
// store at all" into "", so the --submit/--park error has to re-derive which
// one happened — otherwise a reader with no `handoff` on PATH is told to run
// `handoff create`, a command they do not have.
//
// Each test pins PATH to a directory containing ONLY a stub, so the suite is
// hermetic: it does not depend on whether the box running it happens to have
// the real federation store installed (which is exactly the variable under
// test).
package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// pinHandoffStore puts exactly one `handoff` on PATH: present-and-empty (the
// store exists, reports nothing open) when present is true, absent otherwise.
func pinHandoffStore(t *testing.T, present bool) {
	t.Helper()
	dir := t.TempDir()
	if present {
		script := "#!/bin/sh\nexit 1\n" // resolvable, but reports nothing open
		if err := os.WriteFile(filepath.Join(dir, "handoff"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// runCapturingExitAndStderr is runCapturingExit plus stderr capture. The
// --submit/--park refusal is an httpc.Die, which writes to stderr — asserting
// on its text needs both streams.
func runCapturingExitAndStderr(t *testing.T, fn func()) (logs string, code int, exited bool) {
	t.Helper()
	trapExit(t)
	logs = captureStderr(t, func() {
		code, exited = testsupport.Capture(fn)
	})
	return
}

// The store-absent branch must NOT tell the reader to create a handoff, and
// must name the invocation that actually works with no store at all.
func TestSubmitWithoutStoreExplainsInsteadOfPrescribingHandoffCreate(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")
	pinHandoffStore(t, false)

	logs, code, exited := runCapturingExitAndStderr(t, func() {
		CmdIdentity([]string{"--submit", "--dry"})
	})

	if !exited || code != 2 {
		t.Fatalf("expected a usage exit(2), got code=%d exited=%v\n%s", code, exited, logs)
	}
	if strings.Contains(logs, "handoff create") {
		t.Errorf("with no store installed, the error must not prescribe `handoff create`:\n%s", logs)
	}
	for _, want := range []string{"no `handoff` store is installed", "identity --submit <handoff-id>"} {
		if !strings.Contains(logs, want) {
			t.Errorf("expected %q in the error, got:\n%s", want, logs)
		}
	}
}

// The store-present branch keeps its original advice: when a store exists and
// genuinely has nothing open, `handoff create` IS the right instruction.
func TestSubmitWithStoreButNothingOpenStillPrescribesHandoffCreate(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")
	pinHandoffStore(t, true)

	logs, code, exited := runCapturingExitAndStderr(t, func() {
		CmdIdentity([]string{"--submit", "--dry"})
	})

	if !exited || code != 2 {
		t.Fatalf("expected a usage exit(2), got code=%d exited=%v\n%s", code, exited, logs)
	}
	if !strings.Contains(logs, "handoff create") {
		t.Errorf("with a store installed, the error must still say `handoff create`:\n%s", logs)
	}
	if strings.Contains(logs, "no `handoff` store is installed") {
		t.Errorf("store IS installed, so the absent-store message must not fire:\n%s", logs)
	}
}

// The echoed flag must be the one the reader typed: --park is a different
// command from --submit, and a message suggesting the wrong one is the same
// defect class as the one being fixed here.
func TestPinFlagsWithoutStoreEchoedBackVerbatim(t *testing.T) {
	for _, flag := range []string{"--submit", "--park", "--handoff", "--dismiss-handoff"} {
		t.Run(flag, func(t *testing.T) {
			home := freshHome(t)
			seedAgent(t, home, "worker", seedOpts{})
			t.Setenv("PARLAY_AGENT_ID", "worker")
			pinHandoffStore(t, false)

			logs, _, _ := runCapturingExitAndStderr(t, func() {
				CmdIdentity([]string{flag, "--dry"})
			})

			want := "parlay identity " + flag + " <handoff-id>"
			if !strings.Contains(logs, want) {
				t.Errorf("expected the %s invocation echoed back verbatim (%q), got:\n%s", flag, want, logs)
			}
			if strings.Contains(logs, "handoff create") {
				t.Errorf("with no store installed, the error must not prescribe `handoff create`:\n%s", logs)
			}
		})
	}
}

// The claim brief tells an agent to run `handoff show <id>` when it sees a
// pointer. Same class of problem, same reasoning: the pointer line itself is a
// frozen format (see pinHandoffPointer), so the brief's instruction is left
// alone — but this test records WHY, so a future reader does not "fix" the
// pointer and break the three packages that assert its exact bytes.
func TestHandoffPointerFormatStaysStoreIndependent(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")
	withFakeContextReset(t)

	// Pin the pointer with NO store installed, then assert the frozen text.
	pinHandoffStore(t, false)
	_, _, _ = runCapturingExitAndStderr(t, func() {
		CmdIdentity([]string{"--handoff", "handoff-frozen", "--dry"})
	})
	raw, err := os.ReadFile(filepath.Join(home, "worker", "identity.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "> 📎 Handoff: handoff-frozen — run `handoff show handoff-frozen` for full session state"
	if !strings.Contains(string(raw), want) {
		t.Errorf("pointer format must not vary with store presence, got:\n%s", raw)
	}
}
