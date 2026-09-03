package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The Go port shells out to the fully-qualified `parlay identity` command
// rather than a bare `identity` alias. bin/parlay-spawn is inconsistent on
// this point: the ephemeral mint path already calls `parlay identity
// --mint-ephemeral` (line 164), but the named-spawn registration path calls
// bare `identity --register` (line 594) — a wrapper that only exists inside
// an already-enrolled agent's own shell session (per the STARTUP_PROMPT
// contract), not in the plain shell parlay-spawn itself typically runs from.
// Using `parlay identity` everywhere is the portable choice and matches
// docs/scope-go-spawn.md §5's framing: this binary depends on `parlay`
// (bun) being on PATH regardless. Identity itself stays out of scope.

// mintEphemeral shells to `parlay identity --mint-ephemeral`, seeding the
// on-disk identity store, and parses its tab-separated "id\tname\tcolor"
// stdout. bin/parlay-spawn lines 162–172.
func mintEphemeral(server, cwd, model string) (id, name, color string, err error) {
	args := []string{"identity", "--mint-ephemeral", "--cwd", cwd}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.Command("parlay", args...)
	cmd.Env = append(os.Environ(), "PARLAY_SERVER="+server)
	var out bytes.Buffer
	cmd.Stdout = &out
	if runErr := cmd.Run(); runErr != nil {
		return "", "", "", fmt.Errorf("--ephemeral mint failed (is the parlay CLI on PATH?): %w", runErr)
	}
	fields := strings.Split(strings.TrimRight(out.String(), "\n"), "\t")
	if len(fields) != 3 || fields[0] == "" || fields[1] == "" || fields[2] == "" {
		return "", "", "", fmt.Errorf("--ephemeral mint returned an unusable identity: %q", out.String())
	}
	return fields[0], fields[1], fields[2], nil
}

// registerIdentityOptions mirrors the flags bin/parlay-spawn forwards to
// `identity --register` (lines 594–603) so a relaunch can reconstitute the
// agent from its identity frontmatter (parlay reset's --reboot default).
type registerIdentityOptions struct {
	AgentID      string
	Name         string
	Color        string
	Cwd          string
	Model        string
	Mode         string
	Effort       string
	WorktreePath string
	ProjectPath  string
	BeadID       string
	GCSession    string
	GCCity       string
}

// registerIdentity is best-effort — a failure here does not fail the spawn,
// matching bash's `|| true` (line 602). Skipped entirely for ephemeral
// agents, whose mint step already seeded the store with `ephemeral: true`;
// re-registering here would strip that marker.
func registerIdentity(opts registerIdentityOptions) {
	args := []string{
		"identity", "--register",
		"--agent", opts.AgentID,
		"--name", opts.Name,
		"--color", opts.Color,
		"--cwd", opts.Cwd,
		"--mode", opts.Mode,
		"--yolo", "on",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	if opts.WorktreePath != "" {
		args = append(args, "--worktree", opts.WorktreePath)
	}
	if opts.ProjectPath != "" {
		args = append(args, "--project", opts.ProjectPath)
	}
	if opts.BeadID != "" {
		args = append(args, "--bead", opts.BeadID)
	}
	if opts.GCSession != "" {
		args = append(args, "--gc-session", opts.GCSession)
	}
	if opts.GCCity != "" {
		args = append(args, "--gc-city", opts.GCCity)
	}
	cmd := exec.Command("parlay", args...)
	_ = cmd.Run()
}

// registerEphemeralBead mirrors bash's ephemeral re-register (lines ~180):
// the ephemeral mint path never calls the full registerIdentity above (that
// would strip the `ephemeral: true` marker the mint itself just wrote), but
// when --bead was given it still needs one narrow re-register so the bead
// binding survives. Best-effort: a failure here warns but never fails the
// spawn, matching bash's `|| true`.
func registerEphemeralBead(agentID, beadID string) {
	cmd := exec.Command("parlay", "identity", "--register", "--agent", agentID, "--ephemeral", "--bead", beadID)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: WARNING — could not bind bead %s to ephemeral agent %s: %v\n", beadID, agentID, err)
	}
}
