package gctemplate

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fullSpec/minimalSpec are the golden fixtures: fixed specs whose synthesis
// must be byte-for-byte stable (testdata/). Any change to the renderer is a
// deliberate golden update, reviewed as bytes in the diff — regenerate with:
//
//	cd tools/cli && GCTEMPLATE_UPDATE=1 go test ./internal/gctemplate/ -run TestGolden
var fullSpec = LaunchSpec{
	ID:      "review-bot",
	Name:    "Review Bot",
	Color:   "#c084fc",
	Prompt:  "Review the diff in ~/code/foo and report findings.\nBe thorough.",
	Cwd:     "/Users/example/code/foo",
	Model:   "opus",
	Account: "acc2",
	Server:  "http://localhost:14242",
}

var minimalSpec = LaunchSpec{
	ID:     "probe_1",
	Prompt: "",
}

var piSpec = LaunchSpec{
	ID:     "spark-helper",
	Name:   "Spark Helper",
	Color:  "#7dd3fc",
	Prompt: "Summarise the repo status.",
	Cwd:    "/Users/example/code/foo",
	Kind:   "pi",
	Model:  "opencode-go/muse-spark-1.3-contributor",
	Server: "http://localhost:14242",
}

var update = os.Getenv("GCTEMPLATE_UPDATE") == "1"

func TestGolden(t *testing.T) {
	cases := []struct {
		name string
		spec LaunchSpec
	}{
		{"full", fullSpec},
		{"minimal", minimalSpec},
		{"pi", piSpec},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files, err := Synthesize(c.spec)
			if err != nil {
				t.Fatal(err)
			}
			var rels []string
			for rel := range files {
				rels = append(rels, rel)
			}
			sort.Strings(rels)

			wantFiles := []string{
				"agents/" + c.spec.ID + "/agent.toml",
				"agents/" + c.spec.ID + "/prompt.template.md",
			}
			if strings.Join(rels, ",") != strings.Join(wantFiles, ",") {
				t.Fatalf("Synthesize files = %v, want %v", rels, wantFiles)
			}

			for _, rel := range rels {
				golden := filepath.Join("testdata", c.name, filepath.Base(rel))
				if update {
					if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, files[rel], 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("missing golden file %s (regenerate: GCTEMPLATE_UPDATE=1 go test ./internal/gctemplate/): %v", golden, err)
				}
				if string(files[rel]) != string(want) {
					t.Errorf("%s drifted from golden %s:\n--- got ---\n%s\n--- want ---\n%s", rel, golden, files[rel], want)
				}
			}
		})
	}
}

func TestKindStartPerKind(t *testing.T) {
	// claude keeps the YOLO flag set the herdr/subprocess launchers pass.
	start, args, mode, err := kindStart("claude", "opus")
	if err != nil {
		t.Fatal(err)
	}
	if start != "claude" || mode != "arg" {
		t.Errorf("claude start = (%q, %q), want (claude, arg)", start, mode)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--dangerously-skip-permissions", "--model opus"} {
		if !strings.Contains(joined, want) {
			t.Errorf("claude args %q lack %q", args, want)
		}
	}

	// Empty kind defaults to claude (byte-identical goldens above prove it).
	if start, _, _, err := kindStart("", ""); err != nil || start != "claude" {
		t.Errorf("empty kind = (%q, %v), want (claude, nil)", start, err)
	}

	// pi takes its own --model flag, never claude's YOLO set.
	start, args, mode, err = kindStart("pi", "opencode-go/muse-spark-1.3-contributor")
	if err != nil {
		t.Fatal(err)
	}
	if start != "pi" || mode != "arg" {
		t.Errorf("pi start = (%q, %q), want (pi, arg)", start, mode)
	}
	if len(args) != 2 || args[0] != "--model" || args[1] != "opencode-go/muse-spark-1.3-contributor" {
		t.Errorf("pi args = %q, want [--model opencode-go/muse-spark-1.3-contributor]", args)
	}

	// pi without a model launches bare (its own config decides).
	if _, args, _, err := kindStart("pi", ""); err != nil || len(args) != 0 {
		t.Errorf("pi without model = (%q, %v), want ([], nil)", args, err)
	}

	// Unknown harnesses refuse loudly instead of launching with guessed flags.
	if _, _, _, err := kindStart("opencode", ""); err == nil || !strings.Contains(err.Error(), `"opencode"`) {
		t.Errorf("opencode kind should refuse naming the kind, got: %v", err)
	}
}

func TestSynthesizePiRendersPiCommand(t *testing.T) {
	files, err := Synthesize(piSpec)
	if err != nil {
		t.Fatal(err)
	}
	toml := string(files["agents/spark-helper/agent.toml"])
	for _, want := range []string{
		`pi --model opencode-go/muse-spark-1.3-contributor`,
		`prompt_mode = "arg"`,
		`process_names = ["pi"]`,
	} {
		if !strings.Contains(toml, want) {
			t.Errorf("pi agent.toml missing %q:\n%s", want, toml)
		}
	}
	for _, banned := range []string{"--dangerously-skip-permissions", "--fallback-model", "--strict-mcp-config", "claude"} {
		if strings.Contains(toml, banned) {
			t.Errorf("pi agent.toml must not contain claude surface %q:\n%s", banned, toml)
		}
	}
}

func TestSynthesizeRejectsInvalidID(t *testing.T) {
	for _, id := range []string{"", "-leading-dash", "has space", "has.dot", "has/slash", "../escape"} {
		if _, err := Synthesize(LaunchSpec{ID: id}); err == nil {
			t.Errorf("Synthesize accepted invalid id %q", id)
		}
	}
}

func TestSynthesizeDeterministic(t *testing.T) {
	a, err := Synthesize(fullSpec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Synthesize(fullSpec)
	if err != nil {
		t.Fatal(err)
	}
	for rel := range a {
		if string(a[rel]) != string(b[rel]) {
			t.Errorf("%s differs across two runs of the same spec", rel)
		}
	}
}

func TestStartCommandOverrideDisablesPromptArg(t *testing.T) {
	files, err := Synthesize(LaunchSpec{ID: "inert", StartCommand: "/bin/sleep", Args: []string{"300"}})
	if err != nil {
		t.Fatal(err)
	}
	toml := string(files["agents/inert/agent.toml"])
	for _, want := range []string{
		`start_command = "/usr/bin/env BEADS_ACTOR=parlay-inert PARLAY_AGENT_ID=inert /bin/sleep 300"`,
		`prompt_mode = "none"`,
		`process_names = ["sleep"]`,
		"suspended = true",
	} {
		if !strings.Contains(toml, want) {
			t.Errorf("agent.toml missing %q:\n%s", want, toml)
		}
	}
	// gc's agent-level start_command is an escape hatch that ignores any
	// separate args field (internal/config/resolve.go step 1 at the pin) —
	// a rendered `args =` line would be silently dropped at launch.
	if strings.Contains(toml, "args =") {
		t.Error("agent.toml must not render a separate args field — gc ignores it under start_command")
	}
	if strings.Contains(toml, "--dangerously-skip-permissions") {
		t.Error("start-command override must not inherit the claude default args")
	}
}

func TestStartCommandArgsAreShellQuoted(t *testing.T) {
	files, err := Synthesize(LaunchSpec{
		ID:           "quoted",
		Name:         "Quoted Probe",
		Server:       "http://localhost:14242",
		StartCommand: "/bin/sh",
		Args:         []string{"-c", "echo 'hi there' > /tmp/x; exec sleep 300"},
	})
	if err != nil {
		t.Fatal(err)
	}
	toml := string(files["agents/quoted/agent.toml"])
	// TOML-escaped rendering of the shell-quoted line: the env rides as a
	// /usr/bin/env prefix (gc's start_command escape hatch drops the agent
	// [env] table — this prefix is the delivery channel), K=V pairs with
	// metacharacters are single-quoted, and the arg with spaces, quotes, and
	// metacharacters rides inside single quotes with the embedded single
	// quotes escaped the way gc's shellquote round-trips them.
	want := `start_command = "/usr/bin/env BEADS_ACTOR=parlay-quoted PARLAY_AGENT_ID=quoted 'PARLAY_AGENT_NAME=Quoted Probe' PARLAY_SERVER=http://localhost:14242 /bin/sh -c 'echo '\\''hi there'\\'' > /tmp/x; exec sleep 300'"`
	if !strings.Contains(toml, want) {
		t.Errorf("agent.toml missing %s\ngot:\n%s", want, toml)
	}
}

func TestTOMLStringEscaping(t *testing.T) {
	cases := map[string]string{
		`plain`:        `"plain"`,
		`has "quotes"`: `"has \"quotes\""`,
		`back\slash`:   `"back\\slash"`,
		"tab\tnl\n":    `"tab\tnl\n"`,
	}
	for in, want := range cases {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestWriteIntoOverwrites(t *testing.T) {
	pack := t.TempDir()
	written, err := WriteInto(pack, fullSpec)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 {
		t.Fatalf("WriteInto wrote %d files, want 2: %v", len(written), written)
	}

	// Re-synthesis reconciles: a drifted file is restored, not preserved.
	tomlPath := filepath.Join(pack, "agents", fullSpec.ID, "agent.toml")
	if err := os.WriteFile(tomlPath, []byte("drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteInto(pack, fullSpec); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "drift" {
		t.Error("WriteInto must overwrite a drifted template")
	}
}
