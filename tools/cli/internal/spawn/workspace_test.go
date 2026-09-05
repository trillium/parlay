package spawn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubHerdrWorkspace puts a fake `herdr` on PATH: `workspace list` prints
// listJSON, `workspace create ...` prints createJSON. Any other invocation
// (including none at all, when failIfCalled is set) fails loudly so a test
// can prove resolveWorkspace never shelled out.
func stubHerdrWorkspace(t *testing.T, listJSON, createJSON string, failIfCalled bool) {
	t.Helper()
	binDir := t.TempDir()
	var body string
	if failIfCalled {
		body = "#!/usr/bin/env bash\necho 'herdr must not be invoked for an id passthrough' >&2\nexit 1\n"
	} else {
		body = "#!/usr/bin/env bash\n" +
			"if [ \"$1\" = workspace ] && [ \"$2\" = list ]; then cat <<'EOF'\n" + listJSON + "\nEOF\n" +
			"elif [ \"$1\" = workspace ] && [ \"$2\" = create ]; then cat <<'EOF'\n" + createJSON + "\nEOF\n" +
			"else exit 1\nfi\n"
	}
	if err := os.WriteFile(filepath.Join(binDir, "herdr"), []byte(body), 0o755); err != nil {
		t.Fatalf("write herdr stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestResolveWorkspaceIDPassthroughNeverCallsHerdr(t *testing.T) {
	stubHerdrWorkspace(t, "", "", true)
	got, err := resolveWorkspace("wAbc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "wAbc123" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestResolveWorkspaceLabelResolvesToExisting(t *testing.T) {
	listJSON := `{"result":{"workspaces":[{"workspace_id":"wExisting1","label":"my-workspace"}]}}`
	stubHerdrWorkspace(t, listJSON, "", false)
	got, err := resolveWorkspace("my-workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "wExisting1" {
		t.Errorf("expected wExisting1, got %q", got)
	}
}

func TestResolveWorkspaceLabelCreatesWhenMissing(t *testing.T) {
	listJSON := `{"result":{"workspaces":[]}}`
	createJSON := `{"result":{"workspace":{"workspace_id":"wNewlyCreated1"}}}`
	stubHerdrWorkspace(t, listJSON, createJSON, false)
	got, err := resolveWorkspace("brand-new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "wNewlyCreated1" {
		t.Errorf("expected wNewlyCreated1, got %q", got)
	}
}

func TestResolveWorkspaceListFailureErrors(t *testing.T) {
	emptyPATH(t)
	_, err := resolveWorkspace("some-label")
	if err == nil {
		t.Fatal("expected an error when herdr is not on PATH")
	}
	if !strings.Contains(err.Error(), "workspace list failed") {
		t.Errorf("expected a workspace-list-failed error, got %v", err)
	}
}

func TestResolveWorkspaceCreateUnparseableErrors(t *testing.T) {
	listJSON := `{"result":{"workspaces":[]}}`
	createJSON := `not json`
	stubHerdrWorkspace(t, listJSON, createJSON, false)
	_, err := resolveWorkspace("brand-new")
	if err == nil {
		t.Fatal("expected an error for an unparseable create response")
	}
	if !strings.Contains(err.Error(), "could not parse workspace_id") {
		t.Errorf("expected a parse error, got %v", err)
	}
}
