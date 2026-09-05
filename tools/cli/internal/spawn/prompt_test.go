package spawn

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestStartupPromptMatchesBashPath is the single-source-of-truth parity gate
// (robots-hrt2): composeStartupPrompt (the Go path) must render byte-identical
// output to bin/parlay-spawn's load_template (the bash path) for the same
// inputs, against the same physical template file. Both paths now consume the
// one canonical launch-templates/default.txt (the regular file embedded from
// tools/cli/internal/spawn/launch-templates/default.txt — the repo-root launch-templates/default.txt
// is a symlink to it), so they can never silently re-implement different text;
// this test proves the substitution still matches too.
//
// The name deliberately carries command-injection characters that survive into
// the Monitor arm-command as a JSON string (`$( )` and embedded quotes), proving
// both renderers single-quote and JSON-escape them identically (robots-2h4n).
// It omits an apostrophe on purpose: a `'` in the name makes shell_quote emit a
// `\` inside the monitor command, and bin/parlay-spawn's load_template
// substitution `${content//…/$var_value}` collapses `\\` → `\` (a pre-existing
// bash quirk this task is forbidden from touching), so the Go path — which
// preserves both backslashes, the correct JSON — deliberately does not match
// bash on that exotic edge. For every realistic spawn input (no apostrophes)
// the two paths are byte-identical.
func TestStartupPromptMatchesBashPath(t *testing.T) {
	server := "http://localhost:4242"
	agentID := "mc-x"
	name := "hostile() $(x) \"quoted\" and value"
	color := "#f97316"
	setupBlock := "## Setup\n\nYou are running in an isolated git worktree.\n"
	prompt := "Do the thing, then say done."
	dod := "reply your result with 'reply \"<summary>\"' and run: parlay status done"

	goOut := composeStartupPrompt(server, agentID, name, color, setupBlock, prompt, dod)

	// Render the same template through bin/parlay-spawn's exact load_template
	// algorithm (cat + per-{{VAR}} literal substitution), reading the same
	// physical file the Go path embeds. The MONITOR_CMD_JSON value is built the
	// way bash did it: shell_quote each component, then json_escape
	// the whole command (jq -Rs .).
	templatePath := "launch-templates/default.txt"
	if _, err := os.Stat(templatePath); err != nil {
		t.Fatalf("canonical template not reachable from test cwd (%v): %v", templatePath, err)
	}

	bashScript := `
set -euo pipefail
load_template() {
  local template_path="$1"
  shift
  local content
  content=$(cat "$template_path")
  while [ $# -gt 0 ]; do
    local var_pair="$1"
    local var_name="${var_pair%%=*}"
    local var_value="${var_pair#*=}"
    content="${content//"{{$var_name}}"/$var_value}"
    shift
  done
  printf '%s' "$content"
}
shell_quote() { printf "'%s'" "${1//\'/\'\\\'\'}"; }
json_escape() { printf '%s' "$1" | jq -Rs .; }
MONITOR_CMD_JSON=$(json_escape "PARLAY_SERVER=$(shell_quote "$PARLAY") parlay listen --agent $(shell_quote "$AGENT_ID") --name $(shell_quote "$NAME") --color $(shell_quote "$COLOR")")
load_template "$1" \
  "PARLAY=$PARLAY" \
  "AGENT_ID=$AGENT_ID" \
  "NAME=$NAME" \
  "COLOR=$COLOR" \
  "MONITOR_CMD_JSON=$MONITOR_CMD_JSON" \
  "SETUP_BLOCK=$SETUP_BLOCK" \
  "PROMPT=$PROMPT" \
  "DOD=$DOD"
`
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH; skipping both-paths parity comparison")
	}
	cmd := exec.Command("bash", "-c", bashScript, "--", templatePath)
	// Pass PATH through (bash's json_escape shells out to jq) on top of the
	// substitution inputs — a fresh cmd.Env would otherwise leave bash without
	// PATH and jq unresolvable.
	cmd.Env = append(
		[]string{"PATH=" + os.Getenv("PATH")},
		"PARLAY="+server,
		"AGENT_ID="+agentID,
		"NAME="+name,
		"COLOR="+color,
		"SETUP_BLOCK="+setupBlock,
		"PROMPT="+prompt,
		"DOD="+dod,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash load_template render failed: %v\nstderr: %s", err, stderr.String())
	}
	bashOut := stdout.String()

	if goOut != bashOut {
		t.Errorf("Go path and bash path produce DIFFERENT prompt output\n--- bash ---\n%s\n--- go ---\n%s", bashOut, goOut)
	}
}

// robots-2h4n: composeStartupPrompt prints a Monitor arm-command the agent is
// told to paste. The values it interpolates (notably the display name, which is
// often a ticket title verbatim) are arbitrary prose, so they must be inert
// under a shell — the pre-fix `--name "%s"` form evaluated `$(…)`, backticks and
// `$VAR`, and a `"` broke out of the JS string literal.
func TestComposeStartupPromptQuotesMonitorCommand(t *testing.T) {
	hostile := "$( ) and `id` and $HOME and \"quoted\" and it's"
	out := composeStartupPrompt("http://localhost:4242", "mc-x", hostile, "#f97316", "", "do the thing", "reply when done")

	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Monitor({ command:") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("prompt has no Monitor arm-command line:\n%s", out)
	}

	cmd := line[strings.Index(line, `"`) : strings.LastIndex(line, `"`)+1]
	unq, err := strconv.Unquote(cmd)
	if err != nil {
		t.Fatalf("Monitor command is not a well-formed string literal (%v): %s", err, line)
	}

	want := "--name '$( ) and `id` and $HOME and \"quoted\" and it'\\''s'"
	if !strings.Contains(unq, want) {
		t.Errorf("arm-command does not single-quote the name\n got: %s\nwant substring: %s", unq, want)
	}
	if strings.Contains(unq, `--name "`) {
		t.Errorf("arm-command still double-quotes the name (shell would expand it): %s", unq)
	}
	// The other interpolated values are quoted too, so none of them can split
	// or expand either.
	for _, w := range []string{"--agent 'mc-x'", "--color '#f97316'", "PARLAY_SERVER='http://localhost:4242'"} {
		if !strings.Contains(unq, w) {
			t.Errorf("arm-command missing %q; got: %s", w, unq)
		}
	}
}

// TestComposeClaimPromptSubstitutes proves composeClaimPrompt renders
// launch-templates/claim.txt with the same {{VAR}} substitution and
// trailing-newline-trim behavior as composeStartupPrompt (robots-hrt2),
// against the already-existing claim.txt template (bin/parlay-spawn lines
// 1359–1364's --claim branch of prompt composition).
func TestComposeClaimPromptSubstitutes(t *testing.T) {
	out := composeClaimPrompt("mc-x", "task-abc123", "\n## Setup\n\nisolated worktree\n")

	for _, want := range []string{"parlay claim task-abc123", "mc-x", "## Setup"} {
		if !strings.Contains(out, want) {
			t.Errorf("claim prompt missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{{") {
		t.Errorf("claim prompt has an unsubstituted {{VAR}} placeholder:\n%s", out)
	}
	if strings.HasSuffix(out, "\n") {
		t.Errorf("claim prompt should have its trailing newline trimmed (robots-hrt2)")
	}
}
