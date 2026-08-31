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
}

var minimalSpec = LaunchSpec{
	ID:     "probe_1",
	Prompt: "",
}

var update = os.Getenv("GCTEMPLATE_UPDATE") == "1"

func TestGolden(t *testing.T) {
	cases := []struct {
		name string
		spec LaunchSpec
	}{
		{"full", fullSpec},
		{"minimal", minimalSpec},
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
		`start_command = "/bin/sleep"`,
		`args = ["300"]`,
		`prompt_mode = "none"`,
		`process_names = ["sleep"]`,
		"suspended = true",
	} {
		if !strings.Contains(toml, want) {
			t.Errorf("agent.toml missing %q:\n%s", want, toml)
		}
	}
	if strings.Contains(toml, "--dangerously-skip-permissions") {
		t.Error("start-command override must not inherit the claude default args")
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
