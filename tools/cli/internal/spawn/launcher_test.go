package spawn

import (
	"slices"
	"testing"
)

// herdr 0.8.2 relocates nothing by env, so PARLAY_HERDR_SESSION -> an explicit
// `--session` on every call is the only way to point this pipeline at a
// non-default herdr session (and therefore the only way to prove a real spawn
// works without driving the operator's own working session).
func TestHerdrCommandScopesToTheNamedSession(t *testing.T) {
	t.Setenv("PARLAY_HERDR_SESSION", "fm-lab-example")

	cmd := herdrCommand("agent", "get", "a1")
	want := []string{"herdr", "--session", "fm-lab-example", "agent", "get", "a1"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("argv = %q, want %q", cmd.Args, want)
	}
}

// Unset is the normal case and must be byte-identical to the pre-flag
// behaviour: no --session, so herdr resolves its default session as before.
func TestHerdrCommandUnscopedWhenUnset(t *testing.T) {
	t.Setenv("PARLAY_HERDR_SESSION", "")

	cmd := herdrCommand("tab", "close", "t1")
	want := []string{"herdr", "tab", "close", "t1"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("argv = %q, want %q", cmd.Args, want)
	}
}

// The session flag must precede the subcommand. `agent start` ends in
// `-- [AGENT_ARG]...` and herdr hands everything after that separator to the
// agent's own executable, so a trailing --session would scope nothing and
// instead launch `claude --session <name>` — the same misreading of `--` that
// broke this path in the first place (task-p5wvc).
func TestHerdrCommandSessionFlagPrecedesTheAgentArgSeparator(t *testing.T) {
	t.Setenv("PARLAY_HERDR_SESSION", "fm-lab-example")

	cmd := herdrCommand("agent", "start", "a1", "--kind", "claude", "--pane", "p1",
		"--", "--dangerously-skip-permissions")

	sep := slices.Index(cmd.Args, "--")
	flag := slices.Index(cmd.Args, "--session")
	if sep < 0 {
		t.Fatalf("argv lost the agent-arg separator: %q", cmd.Args)
	}
	if flag < 0 {
		t.Fatalf("argv is not scoped to the lab session: %q", cmd.Args)
	}
	if flag > sep {
		t.Fatalf("--session lands after %q, so it would be passed to the agent instead of herdr: %q", "--", cmd.Args)
	}
	if cmd.Args[len(cmd.Args)-1] != "--dangerously-skip-permissions" {
		t.Fatalf("trailing agent argv was disturbed: %q", cmd.Args)
	}
}
