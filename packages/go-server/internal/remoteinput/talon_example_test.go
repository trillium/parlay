package remoteinput

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repo ships a copyable Talon file defining the actions the allow-list
// runs. If the allow-list gains an action the example lacks (or the example
// drifts), a newcomer's submit would settle inject_failed — fail the build.
func TestShippedTalonExampleDefinesEveryAllowedAction(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "examples", "talon")
	py, err := os.ReadFile(filepath.Join(root, "parlay_slides.py"))
	if err != nil {
		t.Fatal(err)
	}
	voice, err := os.ReadFile(filepath.Join(root, "parlay_slides.talon"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"slide_next", "slide_previous"} {
		snippet, ok := ActionSnippet(name)
		if !ok {
			t.Fatalf("%s is not an allow-listed action", name)
		}
		if want := "actions.user." + name + "()"; snippet != want {
			t.Fatalf("snippet %q != %q", snippet, want)
		}
		if !strings.Contains(string(py), "def "+name+"():") {
			t.Errorf("parlay_slides.py does not define %s", name)
		}
		if !strings.Contains(string(voice), "user."+name+"()") {
			t.Errorf("parlay_slides.talon has no voice command for %s", name)
		}
	}
}
