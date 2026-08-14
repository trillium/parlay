package commands

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGofmtClean fails when any file in the tools/cli module needs gofmt.
//
// This exists because a non-clean `gofmt -l` is a false positive for every
// agent that runs it as a pre-commit check: it reports a file the agent never
// touched, and "fixing" it with `gofmt -w` can silently rewrite prose — that is
// exactly how robots-dqle happened, when gofmt mangled the POSIX single-quote
// escape documented above claimShellQuote. Keeping the module gofmt-clean keeps
// the check trustworthy.
func TestGofmtClean(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}

	gofmt, err := exec.LookPath("gofmt")
	if err != nil {
		t.Skipf("gofmt not on PATH: %v", err)
	}

	out, err := exec.Command(gofmt, "-l", root).CombinedOutput()
	if err != nil {
		t.Fatalf("gofmt -l %s: %v\n%s", root, err, out)
	}

	if dirty := strings.TrimSpace(string(out)); dirty != "" {
		t.Errorf("these files are not gofmt-clean:\n%s\n\n"+
			"Before running `gofmt -w`, check the diff (`gofmt -d <file>`): gofmt\n"+
			"reformats doc comments through go/doc/comment and will rewrite prose\n"+
			"punctuation. If the change corrupts documented text, fix the comment\n"+
			"(move the literal into an indented block) rather than accepting it.", dirty)
	}
}
