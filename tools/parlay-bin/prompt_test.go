package main

import (
	"strconv"
	"strings"
	"testing"
)

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
