package store

import (
	"os"
	"testing"

	"parlay/go-server/internal/atomicfile"
	"path/filepath"
)

func TestOpenCreatesStateDirAndAllSubstores(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "state")
	st, err := Open(Config{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Messages.Close()

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("state dir not created: %v", err)
	}
	if st.Messages == nil || st.Registry == nil || st.Drafts == nil || st.Settings == nil || st.Presence == nil || st.Uploads == nil {
		t.Fatalf("Open returned a Store with a nil substore: %+v", st)
	}
}

func TestOpenRequiresDir(t *testing.T) {
	if _, err := Open(Config{}); err == nil {
		t.Error("Open with empty Dir: want error, got nil")
	}
}

func TestOpenIsIdempotentAcrossRestarts(t *testing.T) {
	dir := t.TempDir()

	st1, err := Open(Config{Dir: dir})
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	if _, err := st1.Messages.Append(ChatMessage{Role: "user", Text: "hi"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := st1.Registry.Upsert(AgentInfo{ID: "c0"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := st1.Messages.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st2, err := Open(Config{Dir: dir})
	if err != nil {
		t.Fatalf("Open 2 (restart): %v", err)
	}
	defer st2.Messages.Close()

	if got := st2.Messages.Count(); got != 1 {
		t.Errorf("after restart Messages.Count() = %d, want 1", got)
	}
	if _, ok := st2.Registry.Get("c0"); !ok {
		t.Error("after restart Registry.Get(c0) not found")
	}
}

func TestWriteFileAtomicLeavesNoTempFilesBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	if err := atomicfile.Write(path, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "out.json" {
		t.Errorf("dir entries = %v, want exactly [out.json] (no leftover temp file)", entries)
	}
}
