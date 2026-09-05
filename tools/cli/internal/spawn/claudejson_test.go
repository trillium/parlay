package spawn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pretrustWorkdir does a read-modify-write of the WHOLE of ~/.claude.json, so
// every failure mode here costs the captain their entire Claude Code state
// rather than one setting. It had no test at all; these pin the properties
// that make a partial write impossible to publish.
//
// Note on scope: the fsync added alongside these tests is NOT directly
// asserted. Whether the data reached the device before the rename is not
// observable from Go without a filesystem-level injection point, and a test
// that passes with and without the fix is worse than no test — it reports
// coverage it does not have. What is asserted is everything that IS
// observable: the rename publishes complete content or nothing, no temp file
// survives, and a failure leaves the previous file untouched.

func withClaudeJSON(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	t.Setenv("PARLAY_CLAUDE_JSON", path)
	return path
}

func readDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("read back is not valid JSON (%v): %q", err, raw)
	}
	return doc
}

func trusted(t *testing.T, doc map[string]any, cwd string) bool {
	t.Helper()
	projects, _ := doc["projects"].(map[string]any)
	entry, _ := projects[cwd].(map[string]any)
	v, _ := entry["hasTrustDialogAccepted"].(bool)
	return v
}

func TestPretrustMarksTheWorkdirTrusted(t *testing.T) {
	path := withClaudeJSON(t, `{"projects":{}}`)
	pretrustWorkdir("/work/repo")
	if !trusted(t, readDoc(t, path), "/work/repo") {
		t.Fatal("workdir was not marked trusted")
	}
}

func TestPretrustPreservesUnrelatedState(t *testing.T) {
	// The whole file is rewritten, so anything the write drops is permanently
	// lost. This is the property most at risk from an edit to the write path.
	path := withClaudeJSON(t, `{
		"numStartups": 42,
		"userID": "abc123",
		"projects": {"/other/repo": {"hasTrustDialogAccepted": true, "note": "keep me"}}
	}`)
	pretrustWorkdir("/work/repo")

	doc := readDoc(t, path)
	if got := doc["numStartups"]; got != float64(42) {
		t.Errorf("numStartups lost or altered: %v", got)
	}
	if got := doc["userID"]; got != "abc123" {
		t.Errorf("userID lost or altered: %v", got)
	}
	if !trusted(t, doc, "/other/repo") {
		t.Error("an unrelated project's trust flag was dropped")
	}
	projects, _ := doc["projects"].(map[string]any)
	other, _ := projects["/other/repo"].(map[string]any)
	if other["note"] != "keep me" {
		t.Errorf("unrelated key inside another project was dropped: %v", other)
	}
	if !trusted(t, doc, "/work/repo") {
		t.Error("the new workdir was not marked trusted")
	}
}

func TestPretrustLeavesNoTempFileBehind(t *testing.T) {
	// The temp file is created next to the target, i.e. in the captain's home
	// in production. A leaked .claude.json.tmp-* on every spawn would
	// accumulate there indefinitely.
	path := withClaudeJSON(t, `{"projects":{}}`)
	pretrustWorkdir("/work/repo")

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".claude.json.tmp-") {
			t.Errorf("temp file survived the write: %s", e.Name())
		}
	}
}

func TestPretrustIsIdempotent(t *testing.T) {
	path := withClaudeJSON(t, `{"projects":{}}`)
	pretrustWorkdir("/work/repo")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	pretrustWorkdir("/work/repo")
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("second run changed the file:\n%s\n---\n%s", first, second)
	}
}

func TestPretrustLeavesAnUnparseableFileUntouched(t *testing.T) {
	// Parity with bash's jq-failure fallback: warn, do not write. Clobbering a
	// file we could not parse would destroy state we never understood.
	const garbage = `{this is not json`
	path := withClaudeJSON(t, garbage)
	pretrustWorkdir("/work/repo")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(raw) != garbage {
		t.Errorf("an unparseable file was overwritten: %q", raw)
	}
}

func TestPretrustDoesNothingWhenTheFileIsAbsent(t *testing.T) {
	// bash only acts `if [ -f "$CLAUDE_JSON" ]`. Creating the file here would
	// hand Claude Code a config it never wrote.
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	t.Setenv("PARLAY_CLAUDE_JSON", path)

	pretrustWorkdir("/work/repo")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a missing ~/.claude.json was created; stat err = %v", err)
	}
}
