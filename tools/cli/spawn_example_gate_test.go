package main

// `parlay spawn` enforces a mandatory-model gate (task-21d36): a spawn with
// no resolved model is REFUSED with exit 2 and
// `refusing to spawn — no model was chosen`. There is no implicit default, the
// launching session's model is never inherited, and there is no silent sonnet
// fallback.
//
// The gate is only as good as the examples that teach the verb. Verified by
// running the documented onboarding path end to end on a fresh clone: the
// README's headline spawn example — the product's headline feature — omitted
// `--model` and therefore could not run. Three more examples in the fleet
// spawn skill were in the same state.
//
// Nothing kept that honest. internal/spawn has unit tests for the gate itself,
// but no test compared the gate against the invocation examples in the docs,
// so a doc example could not pass the command it teaches. This is the gate for
// that class: every `parlay spawn` example in a fenced shell block across the
// repository's markdown and shell files must resolve a model via `--model`,
// `--profile`, or `--no-pii` — or be a placeholder synopsis, which is not a
// runnable example and is exempted by shape.
//
// The other half of the same failure mode — an example naming flags that no
// longer exist — is caught by the deleted-artifact hygiene step in CI for *.sh,
// and by review for prose. This gate covers the model gate because it is the
// one gate that refuses with a message a newcomer will read verbatim from a
// doc.
import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// modelSelectors are the flags that satisfy the mandatory-model gate.
// `--profile` resolves a model from packages/spawn-profiles, and `--no-pii`
// auto-routes to a free model; both run before requireModel (spawn.go calls
// runPIIRouting first at every call site).
var modelSelectors = []string{"--model", "--profile", "--no-pii"}

// spawnInvocationRe matches `parlay spawn` / `parlay-dev spawn` as a command,
// so prose that merely mentions the verb, or the sibling binaries
// `spawn-watchdog` and `spawn-triage` that really do exist in bin/, are not
// mistaken for examples.
var spawnInvocationRe = regexp.MustCompile(`\bparlay(?:-dev)?[ \t]+spawn[ \t]`)

// placeholderArgRe matches a template argument — `<id>` or `[options]` — the
// shape that marks a line as a synopsis rather than a runnable example.
var placeholderArgRe = regexp.MustCompile(`^(<[^>]*>|\[-?[^\]]*\]|\.\.\.)$`)

// spawnExample is one documented invocation.
type spawnExample struct {
	command string // joined, backslash continuations resolved
	line    int    // 1-indexed line of the command's first line
}

// TestEverySpawnDocExampleResolvesAModel is the gate. It fails the build on a
// documented `parlay spawn` invocation that could not run.
func TestEverySpawnDocExampleResolvesAModel(t *testing.T) {
	repo := repoRoot(t)
	files := docFiles(t, repo)

	checked := 0
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, inv := range spawnExamples(string(raw), strings.HasSuffix(rel, ".sh")) {
			if isSynopsis(inv.command) {
				continue
			}
			checked++
			if !resolvesAModel(inv.command) {
				t.Errorf("%s:%d: documented `parlay spawn` example does not satisfy the mandatory-model gate — add %s.\n  refusing to spawn — no model was chosen (exit 2).\n  example: %s",
					rel, inv.line, strings.Join(modelSelectors, " / "), oneLine(inv.command))
			}
		}
	}
	// Vacuous-pass guard: a rename of the walker, or a refactor that empties
	// the corpus, must not turn this gate silently green.
	if checked == 0 {
		t.Fatal("no `parlay spawn` example was checked — this gate would pass vacuously")
	}
	t.Logf("checked %d spawn example(s) across %d doc file(s)", checked, len(files))
}

func resolvesAModel(command string) bool {
	for _, sel := range modelSelectors {
		if strings.Contains(command, sel) {
			return true
		}
	}
	return false
}

// isSynopsis reports whether every positional argument is a placeholder, i.e.
// the line is a signature (`parlay spawn <id> <name> <color> <task> [options]`)
// rather than an example a reader could paste.
func isSynopsis(command string) bool {
	fields := commandFields(command)
	// Drop the `parlay spawn` prefix itself; only the arguments decide whether
	// this is a signature.
	if len(fields) < 2 {
		return false
	}
	positionals := 0
	for _, arg := range fields[2:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		positionals++
		if !placeholderArgRe.MatchString(arg) {
			return false
		}
	}
	return positionals > 0
}

// commandFields splits a command on whitespace, honoring single and double
// quotes well enough to keep a quoted multi-word prompt in one field.
func commandFields(command string) []string {
	var (
		out     []string
		cur     strings.Builder
		quote   rune
		bracket int
	)
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		// A bracketed group such as `[--cwd PATH]` is one template argument,
		// so its spaces must not split the field.
		case r == '[':
			bracket++
			cur.WriteRune(r)
		case r == ']':
			if bracket > 0 {
				bracket--
			}
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && bracket == 0:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// spawnExamples extracts every `parlay spawn` command from the fenced code
// blocks of a markdown document, or from every statement of a shell script.
// Shell line continuations are joined so a flag on the last line counts.
func spawnExamples(body string, shellFile bool) []spawnExample {
	inFence := false
	var out []spawnExample
	var (
		joined  string
		started int
	)
	flush := func() {
		if started == 0 {
			return
		}
		cmd := strings.TrimSpace(joined)
		if spawnInvocationRe.MatchString(cmd) {
			out = append(out, spawnExample{command: cmd, line: started})
		}
		joined, started = "", 0
	}
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		// A shell script has no fences: its whole body is code. In markdown a
		// fence line toggles in and out, and everything outside is prose.
		if !shellFile && strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			flush()
			continue
		}
		if !shellFile && !inFence {
			flush()
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if started == 0 {
			started = i + 1
		}
		if strings.HasSuffix(trimmed, "\\") {
			joined += strings.TrimSuffix(trimmed, "\\") + " "
			continue
		}
		joined += trimmed + " "
		flush()
	}
	flush()
	return out
}

// docFiles returns every markdown and shell file in the repository that could
// teach a newcomer the verb, as repo-relative slash paths.
func docFiles(t *testing.T, repo string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(repo, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repo, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			switch {
			case rel == ".git", rel == ".gnhf", strings.HasPrefix(rel, "node_modules"),
				rel == "third_party", strings.HasSuffix(rel, "/node_modules"):
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".md") && !strings.HasSuffix(rel, ".sh") {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo for doc files: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("docFiles found no markdown or shell files — this gate would pass vacuously")
	}
	return out
}

// repoRoot resolves the repository root from the test's working directory
// (tools/cli), asserting a marker file so a layout change fails loudly
// instead of silently scanning nothing.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join(".", "..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "README.md")); err != nil {
		t.Fatalf("expected a repository root two levels up from tools/cli (README.md): %v", err)
	}
	return root
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestSpawnExamplesParsing is the gate's own test-the-test. A corpus gate that
// silently stopped extracting examples would go green, so the extraction is
// pinned directly: continuations must join, prose outside a fence must be
// ignored, comments must not start a command, and the sibling
// `spawn-watchdog` binary must not match.
func TestSpawnExamplesParsing(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		shellFile bool
		wantCount int
		wantCmd   string
	}{
		{
			name: "continuation joins so a flag on the last line counts",
			body: "```sh\n" +
				"parlay spawn reviewer \"Reviewer\" \"#fff\" \\\n" +
				"  \"do the thing\" --cwd /tmp/x --model sonnet\n" +
				"```\n",
			wantCount: 1,
			wantCmd:   `--cwd /tmp/x --model sonnet`,
		},
		{
			name:      "prose outside a fence is ignored",
			body:      "Run `parlay spawn foo \"Foo\" \"#fff\" \"x\"` first.\n",
			wantCount: 0,
		},
		{
			name:      "a comment line does not become a command",
			body:      "```bash\n# parlay spawn nope \"Nope\" \"#fff\" \"x\"\nparlay ok\n```\n",
			wantCount: 0,
		},
		{
			name:      "the spawn-watchdog sibling is not an example",
			body:      "```sh\nparlay spawn-watchdog --help\n```\n",
			wantCount: 0,
		},
		{
			name:      "a shell script body is code without fences",
			body:      "parlay spawn reviewer \"Reviewer\" \"#fff\" \"x\" --model opus\n",
			shellFile: true,
			wantCount: 1,
			wantCmd:   "--model opus",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := spawnExamples(tc.body, tc.shellFile)
			if len(got) != tc.wantCount {
				t.Fatalf("spawnExamples returned %d example(s), want %d: %+v", len(got), tc.wantCount, got)
			}
			if tc.wantCount == 1 && tc.wantCmd != "" && !strings.Contains(got[0].command, tc.wantCmd) {
				t.Errorf("command = %q, want it to contain %q", got[0].command, tc.wantCmd)
			}
		})
	}
}

// TestIsSynopsis pins the one exemption, so a real example can never start
// hiding behind it by growing angle brackets.
func TestIsSynopsis(t *testing.T) {
	synopsis := []string{
		`parlay spawn <id> <name> <color> <task> [options]`,
		`parlay spawn --ephemeral <initial-prompt> [--cwd PATH]`,
	}
	for _, s := range synopsis {
		if !isSynopsis(s) {
			t.Errorf("isSynopsis(%q) = false, want true", s)
		}
	}
	runnable := []string{
		`parlay spawn code-reviewer "Code Reviewer" "#c084fc" "go" --cwd ~/code/foo`,
		`parlay spawn reviewer "Reviewer" "#fff" "go" --profile fast`,
	}
	for _, s := range runnable {
		if isSynopsis(s) {
			t.Errorf("isSynopsis(%q) = true, want false", s)
		}
	}
}
