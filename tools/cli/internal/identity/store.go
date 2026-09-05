// Shared store helpers for the identity/scratchpad verbs: per-agent
// directory resolution, context.json I/O, and the context-reset command
// probe.
//
// Ported from packages/cli/src/commands-identity/store.ts.
package identity

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// MemKind selects which per-agent file (identity.md or scratchpad.md) a
// dispatch call operates on.
type MemKind string

const (
	KindScratchpad MemKind = "scratchpad"
	KindIdentity   MemKind = "identity"
)

// MemBoolFlags / MemValueFlags are the flag tables the mem dispatcher parses
// with (args.Parse's flags/valueFlags), ported verbatim from store.ts's
// MEM_BOOL_FLAGS/MEM_VALUE_FLAGS.
var (
	MemBoolFlags = []string{
		"--clear", "--path", "--dry", "--register", "--handoff", "--dismiss-handoff",
		"--submit", "--park", "--ephemeral", "--preserve", "--reap-ephemeral", "--mint-ephemeral",
	}
	MemValueFlags = []string{
		"--agent", "--complete", "--launch", "--name", "--color", "--model", "--cwd",
		"--rename", "--to", "--older-than", "--mode", "--effort", "--kind", "--yolo",
		// --worktree/--project were dropped from this table during the port
		// (they ARE in store.ts's MEM_VALUE_FLAGS). args.Parse dies with
		// EXIT_USAGE on an unknown flag, so every `parlay identity --register
		// … --worktree <path> --project <path>` call `parlay spawn` makes for a
		// worktree agent exited 2 and wrote NO frontmatter at all — and
		// the spawn pipeline's registerIdentity swallows the exit code (`_ =
		// cmd.Run()`), so the agent launched looking fine with an empty
		// launch spec. Downstream that made `parlay teardown` read no
		// worktree, delete the store, and orphan the worktree (and any
		// unpushed commits in it) unchecked. Restored: robots-6xq7.
		"--worktree", "--project",
		// --bead binds a beads work item at SPAWN time (the beads-required
		// mode: `parlay spawn --bead <id>` → `identity --register …
		// --bead <id>`), and that bead's open/closed lifecycle then governs
		// the agent's. It MUST be in this table, not MemBoolFlags: same
		// robots-6xq7 trap as --worktree/--project above — a value flag
		// missing from here makes args.Parse die EXIT_USAGE, and
		// the spawn pipeline's registration warns but does not abort, so the agent
		// would launch with no bead recorded and no relaunch suppression.
		"--bead",
		// --gc-session/--gc-city stamp the Gas City session pointer at spawn
		// time (spawn-lift unit 7): the session's bead id and city dir, the
		// projection of the bead-backed AddressDirectory identity that
		// `parlay gc-resolve` resolves through. Value flags, so the same
		// robots-6xq7 trap applies: dropping either from this table would
		// kill the WHOLE register call (worktree included) with EXIT_USAGE.
		"--gc-session", "--gc-city",
	}
)

// AgentsRoot is the root of the per-agent store:
// ${PARLAY_AGENT_HOME:-~/.parlay/agents}. Every id (named, ephemeral,
// renamed) lives in a <root>/<id>/ directory.
func AgentsRoot() string {
	if h := os.Getenv("PARLAY_AGENT_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".parlay", "agents")
}

// ContextInfo is the {id, name, color} triple written to context.json — the
// reply-attribution record the panel reads.
type ContextInfo struct {
	ID    string
	Name  string
	Color string
}

// contextJSON mirrors store.ts's write shape field-for-field; struct field
// order fixes the emitted key order (id, name, color) and omitempty matches
// the TS `if (ctx.name) out.name = ...` guards.
type contextJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Color string `json:"color,omitempty"`
}

// WriteContextJSON (re)writes dir/context.json. It MUST exist for every id
// or attribution breaks, so callers write it whenever an identity's launch
// spec is seeded or an id is renamed.
func WriteContextJSON(dir string, ctx ContextInfo) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(contextJSON{ID: ctx.ID, Name: ctx.Name, Color: ctx.Color}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, "context.json"), data, 0o644)
}

// ContextResetCmd resolves the self-restart command: prefer "context-reset"
// if it is resolvable on PATH, otherwise fall back to the legacy
// "reincarnate" name (which is on PATH and execs context-reset).
//
// store.ts does this via `command -v` under a Bun-specific workaround
// (bun does not propagate process.env mutations to child processes when
// `env` is omitted). That workaround doesn't apply to Go's os/exec, but the
// *behavior* — resolve the best available name, prefer the new one, fall
// back to the legacy one — must be preserved (docs/scope-go-cli.md §5 item
// 3); exec.LookPath does the PATH resolution directly.
func ContextResetCmd() string {
	if abs, err := exec.LookPath("context-reset"); err == nil && abs != "" {
		return abs
	}
	return "reincarnate"
}

// MemFile resolves the per-agent memory file under <root>/<id>/<kind>.md,
// keyed by PARLAY_AGENT_ID (or an explicit agentOverride). Creates the dir.
// Dies with EXIT_USAGE if no identity is resolvable.
func MemFile(kind MemKind, agentOverride string) (agent, file string) {
	agent = strings.TrimSpace(agentOverride)
	if agent == "" {
		agent = strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
	}
	if agent == "" {
		httpc.Die(
			"parlay "+string(kind)+": no agent identity — run inside a parlay-spawned agent (sets PARLAY_AGENT_ID) or pass --agent <id>",
			config.ExitUsage,
		)
		return "", ""
	}
	dir := filepath.Join(AgentsRoot(), agent)
	_ = os.MkdirAll(dir, 0o755)
	file = filepath.Join(dir, string(kind)+".md")
	return agent, file
}
