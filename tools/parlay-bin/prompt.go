package main

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed launch-templates/default.txt
var defaultTemplate string

// composeDoD mirrors bin/parlay-spawn's per-mode Definition of Done (the
// DOD switch in parlay-spawn at the "Compose the Definition of Done per
// delivery mode" step).
func composeDoD(mode, agentID string) string {
	switch mode {
	case "branch":
		return fmt.Sprintf("Commit your work on branch 'parlay/%s'. When done, run: parlay status done \"ready in branch parlay/%s\"", agentID, agentID)
	case "pr":
		return fmt.Sprintf("Push your branch 'parlay/%s' and open a PR via 'gh pr create'. When done, run: parlay status done \"PR <url>\"", agentID)
	default:
		return "Do the task, then reply your result with 'reply \"<summary>\"' and run: parlay status done \"<one-line summary>\""
	}
}

// composeSetupBlock mirrors bin/parlay-spawn's worktree isolation block for
// the brief — empty string when no worktree is in play.
func composeSetupBlock(wantWorktree bool, worktreePath, projectPath string) string {
	if !wantWorktree {
		return ""
	}
	return fmt.Sprintf(`
## Setup

You are running in an isolated git worktree — NOT the primary checkout.
Assert isolation BEFORE any git operation:

  pwd -P                         # must resolve to: %s
  git rev-parse --show-toplevel  # must resolve to: %s

If EITHER resolves to %s (the primary checkout), STOP immediately:
  parlay status blocked "isolation failure — running in primary checkout, not worktree"
Never commit, branch, or push from the primary checkout.
`, worktreePath, worktreePath, projectPath)
}

// composeStartupPrompt builds the full first-turn brief handed to the
// spawned claude: it renders the single canonical template
// (launch-templates/default.txt, embedded via go:embed from
// tools/parlay-bin/launch-templates/default.txt — that package file is the
// physical home of the template, and launch-templates/default.txt is a
// symlink to it) with the same {{VAR}} → value substitutions
// bin/parlay-spawn's load_template performs. The template is the single
// source of truth; this function only supplies the substitution values.
func composeStartupPrompt(server, agentID, name, color, setupBlock, prompt, dod string) string {
	// Single-quote each value for the shell, then render the whole thing as a
	// quoted literal for the Monitor({}) call. A display name is arbitrary
	// prose; inside plain double quotes `$(…)`, backticks and `$VAR` are live,
	// so a name mentioning `$( )` got command-substituted the moment the agent
	// pasted the printed line (robots-2h4n).
	monitorCmd := fmt.Sprintf("PARLAY_SERVER=%s parlay listen --agent %s --name %s --color %s",
		shellQuote(server), shellQuote(agentID), shellQuote(name), shellQuote(color))

	values := map[string]string{
		"PARLAY":           server,
		"AGENT_ID":         agentID,
		"NAME":             name,
		"COLOR":            color,
		"MONITOR_CMD_JSON": strconv.Quote(monitorCmd),
		"SETUP_BLOCK":      setupBlock,
		"PROMPT":           prompt,
		"DOD":              dod,
	}

	out := defaultTemplate
	for k, v := range values {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	// bin/parlay-spawn's load_template reads via `content=$(cat …)`, whose
	// command substitution strips the template's trailing newline; go:embed
	// preserves it. Trim the single trailing newline so the Go path emits
	// byte-identical output to the bash path (robots-hrt2).
	out = strings.TrimSuffix(out, "\n")
	return out
}
