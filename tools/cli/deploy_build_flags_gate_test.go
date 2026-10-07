package main

// Every Go build a committed deploy installer performs must pin
// CGO_ENABLED=0. This is not a style preference: the CLI module's beads
// dependency carries an embedded Dolt tree whose ICU binding needs C++
// headers a stock macOS toolchain does not ship, so a default-cgo build of
// tools/cli dies with
//
//	file.cpp:3:10: fatal error: 'unicode/regex.h' file not found
//
// bin/parlay documents and works around exactly this (robots-wgij), and so
// does tools/relay/build.sh, examples/bootstrap-sandbox.sh,
// packages/go-server/deploy/install.sh and its ensure-up.sh.
//
// tools/eval-engine/deploy/install.sh was the one installer that did not: it
// ran a bare `go build` of tools/cli. That installer's output is the FIRST fix
// `parlay health` and `parlay doctor` print for the eval-engine FAIL — the one
// red line the Quickstart explicitly tells every newcomer to expect — so on any
// machine without ICU headers the documented repair could not build the engine
// it then failed to verify. Nothing gated it: the shell harnesses exercise
// go-server's and relay's installers, never the eval engine's, because it is
// the only one that bootstraps a launchd job.
//
// This is deliberately scoped to `*/deploy/install.sh`. tools/gc-build/build-gc.sh
// is excluded on purpose: it builds an external pinned checkout rather than a
// module of this repo, and its default-cgo branch is an explicit, documented
// opt-in (the ICU flags there are the caller's responsibility).
//
// The same rule applies to the commands the README teaches a contributor to
// run: `cd tools/cli && go test ./...` failed to build on a stock macOS
// toolchain for exactly the reason above, and the Development section is the
// only place a contributor is told how to run the suite.
import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A `go build` / `go test` invocation. Shell quoting is not modelled — these
// are heredoc-free, plainly quoted commands — so a match is a real command, and
// anything the regex cannot read is left for review rather than silently
// skipped.
var goToolRe = regexp.MustCompile(`(?:^|[;&|]\s*|\(\s*|\bthen\s+|\belse\s+)(CGO_ENABLED=[01]\s+)?(go\s+(?:build|test|run|install)\b)`)

// deployInstallScripts returns every committed */deploy/install.sh in the tree.
// It fails the test if it finds none, so a rename that emptied the set cannot
// turn the gate vacuously green.
func deployInstallScripts(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "node_modules", "dist", "third_party":
				return filepath.SkipDir
			}
			return nil
		}
		if name != "install.sh" {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "deploy" {
			return nil
		}
		found = append(found, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("found no */deploy/install.sh — the scan is vacuous, not green")
	}
	return found
}

// TestDeployInstallersNeverBuildWithCGO asserts every go build/test in every
// committed deploy installer explicitly disables cgo.
func TestDeployInstallersNeverBuildWithCGO(t *testing.T) {
	root := repoRoot(t)
	for _, path := range deployInstallScripts(t) {
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			m := goToolRe.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			if m[1] == "" {
				t.Errorf("%s:%d: builds without CGO_ENABLED=0 — on macOS this dies on the missing ICU headers (robots-wgij):\n\t%s",
					rel, i+1, trimmed)
			}
		}
	}
}

// TestDocumentedToolsCLICommandsDisableCGO asserts the README's Development
// commands cannot regress to a default-cgo `go test` of the CLI module. It
// reads the repo root README (the only place a contributor is told how to run
// the CLI suite) and checks every line that both names tools/cli and invokes
// the go tool.
func TestDocumentedToolsCLICommandsDisableCGO(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	checked := 0
	for i, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "tools/cli") {
			continue
		}
		m := goToolRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		checked++
		if m[1] == "" {
			t.Errorf("README.md:%d: documents a tools/cli go command with cgo on — it does not "+
				"build on a stock macOS toolchain (robots-wgij):\n\t%s", i+1, strings.TrimSpace(line))
		}
	}
	if checked == 0 {
		t.Fatal("no tools/cli go command found in README.md — the scan is vacuous, not green")
	}
}
