package spawn

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestStartupPromptRendersTheCanonicalTemplate is the single-source-of-truth
// gate for the startup brief: the template `go:embed` compiles in must be the
// SAME BYTES as the physical file agents and humans read (the repo-root
// launch-templates/default.txt is a symlink to it), and rendering it must
// leave no `{{VAR}}` behind — a leftover placeholder would be handed to a live
// agent as literal text, in the one prompt it is guaranteed to read.
//
// It USED to be a bash/Go byte-parity test against bin/parlay-spawn's
// load_template, but that script was deleted with the bash spawner
// (task-42qot): the comparison was re-rendering the same file through an
// algorithm nothing ships any more, so it guarded nothing while its name and
// comment claimed a live second implementation. What is worth keeping is the
// invariant below. The monitor arm-command's shell-quoting is still pinned, by
// TestComposeStartupPromptQuotesMonitorCommand.
func TestStartupPromptRendersTheCanonicalTemplate(t *testing.T) {
	physical, err := os.ReadFile("launch-templates/default.txt")
	if err != nil {
		t.Fatalf("canonical template not reachable from test cwd: %v", err)
	}
	if string(physical) != defaultTemplate {
		t.Errorf("the embedded launch-templates/default.txt has drifted from the physical file\n"+
			"(%d embedded bytes vs %d on disk) — edit the real file, never the embed or the symlink",
			len(defaultTemplate), len(physical))
	}

	// A name carrying command-injection characters, to prove they are
	// single-quoted and JSON-escaped rather than interpolated raw (robots-2h4n).
	out := composeStartupPrompt("mc-x", `hostile() $(x) "quoted"`, "#f97316",
		"## Setup\n\nYou are running in an isolated git worktree.\n",
		"Do the thing, then say done.", `reply "done"`)

	if strings.Contains(out, "{{") {
		t.Errorf("rendered startup prompt has an unsubstituted {{VAR}} — an agent would read it literally:\n%s", out)
	}
	for _, want := range []string{
		`Monitor({ command: "parlay listen --agent 'mc-x'`,
		"Do the thing, then say done.",
		"## Setup",
		`reply "done"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered startup prompt missing %q:\n%s", want, out)
		}
	}
}

// declaredPlaceholders lists every {{NAME}} the template uses, so the render
// test can assert the template and the substitution map agree in both
// directions: a placeholder nothing supplies would otherwise survive into an
// agent's first message.
func declaredPlaceholders(template string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`\{\{([A-Z_]+)\}\}`).FindAllStringSubmatch(template, -1) {
		out = append(out, m[0])
	}
	return out
}

// Every placeholder the templates declare must be one composeStartupPrompt /
// composeClaimPrompt actually substitutes. A new {{FOO}} added to a template
// and forgotten in prompt.go would otherwise reach a live agent verbatim.
func TestEveryTemplatePlaceholderIsSubstituted(t *testing.T) {
	cases := []struct {
		name     string
		template string
		render   func() string
	}{
		{"default.txt", defaultTemplate, func() string {
			return composeStartupPrompt("mc-x", "n", "#f97316", "setup", "prompt", "dod")
		}},
		{"claim.txt", claimTemplate, func() string {
			return composeClaimPrompt("mc-x", "task-abc123", "setup")
		}},
	}
	for _, tc := range cases {
		phs := declaredPlaceholders(tc.template)
		if len(phs) == 0 {
			t.Errorf("%s declares no {{PLACEHOLDER}} at all — the file was probably emptied by a bad edit", tc.name)
			continue
		}
		out := tc.render()
		for _, ph := range phs {
			if strings.Contains(out, ph) {
				t.Errorf("%s: placeholder %s survived rendering — add it to the substitution map in prompt.go:\n%s", tc.name, ph, out)
			}
		}
	}
}

// robots-2h4n: composeStartupPrompt prints a Monitor arm-command the agent is
// told to paste. The values it interpolates (notably the display name, which is
// often a ticket title verbatim) are arbitrary prose, so they must be inert
// under a shell — the pre-fix `--name "%s"` form evaluated `$(…)`, backticks and
// `$VAR`, and a `"` broke out of the JS string literal.
func TestComposeStartupPromptQuotesMonitorCommand(t *testing.T) {
	hostile := "$( ) and `id` and $HOME and \"quoted\" and it's"
	out := composeStartupPrompt("mc-x", hostile, "#f97316", "", "do the thing", "reply when done")

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
	for _, w := range []string{"--agent 'mc-x'", "--color '#f97316'"} {
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
