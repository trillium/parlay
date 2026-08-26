package atomicfile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The store copy of this helper omitted MkdirAll, so the first write into a
// not-yet-created store directory failed with ENOENT. Nothing caught it
// because the handlers copy (which did have MkdirAll) was the one exercised by
// the server's happy path.
func TestWriteCreatesMissingParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "c", "settings.json")

	if err := Write(path, []byte(`{"ok":true}`), 0644); err != nil {
		t.Fatalf("Write into a missing directory tree: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Errorf("contents = %q, want %q", got, `{"ok":true}`)
	}
}

func TestWriteAppliesTheRequestedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")

	if err := Write(path, []byte("shh"), 0600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// CreateTemp makes the temp file 0600, so a helper that forgot to chmod
	// would still pass a 0600 assertion. 0644 is the mode that can only be
	// reached by actually applying perm.
	if err := Write(path, []byte("public"), 0644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestWriteReplacesExistingContentsRatherThanAppending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.json")

	if err := Write(path, []byte("aaaaaaaaaaaaaaaaaaaa"), 0644); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if err := Write(path, []byte("bb"), 0644); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	// A truncating-but-not-atomic implementation would pass this; an appending
	// one, or one that renamed over without truncating, leaves the tail behind.
	if string(got) != "bb" {
		t.Errorf("contents = %q, want %q — a shorter write did not fully replace the longer one", got, "bb")
	}
}

// tempFilesIn lists the leftover scratch files in dir, which must always be
// empty once Write has returned — on either the success or the failure path.
func tempFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	var leftovers []string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			leftovers = append(leftovers, e.Name())
		}
	}
	return leftovers
}

func TestRepeatedWritesLeaveOnlyTheTargetFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")

	for i := 0; i < 5; i++ {
		if err := Write(path, []byte("x"), 0644); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if leftovers := tempFilesIn(t, dir); len(leftovers) > 0 {
		t.Errorf("after 5 writes, %d temp files remain: %v", len(leftovers), leftovers)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want exactly 1 (the target file)", len(entries))
	}
}

// This is the test that actually pins `defer os.Remove(tmpPath)`. On the
// success path that defer is already a no-op — the temp file has been renamed
// away, so deleting the line changes nothing and a success-only test passes
// with the cleanup gone. The defer earns its place solely on the FAILURE path:
// a Write that dies after CreateTemp must not strand its scratch file, or the
// store directory accumulates one per failed write forever.
//
// A directory standing at the target path is the reliable way to get there —
// everything through Chmod succeeds, and only the final rename fails.
func TestAFailedWriteRemovesItsTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "occupied")
	// Non-empty, so the rename cannot succeed on any platform.
	if err := os.MkdirAll(filepath.Join(path, "child"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := Write(path, []byte("payload"), 0644); err == nil {
		t.Fatal("Write onto a non-empty directory returned nil, want an error")
	}
	if leftovers := tempFilesIn(t, dir); len(leftovers) > 0 {
		t.Errorf("a failed Write stranded %d temp file(s): %v", len(leftovers), leftovers)
	}
}

// An error on the way to the rename must leave the previous contents in place.
// The caller's alternative — a half-written file — is what this whole helper
// exists to prevent, so the failure path has to be as safe as the success one.
func TestWriteLeavesTheOldFileIntactWhenItCannotProceed(t *testing.T) {
	dir := t.TempDir()
	// A regular file standing where a parent directory is needed makes
	// MkdirAll fail, which is the cheapest way to force an early return.
	blocker := filepath.Join(dir, "notadir")
	if err := os.WriteFile(blocker, []byte("original"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := Write(filepath.Join(blocker, "child.json"), []byte("new"), 0644)
	if err == nil {
		t.Fatal("Write through a file-as-directory returned nil, want an error")
	}
	got, readErr := os.ReadFile(blocker)
	if readErr != nil {
		t.Fatalf("the blocking file is gone after a failed Write: %v", readErr)
	}
	if string(got) != "original" {
		t.Errorf("blocking file = %q, want it untouched at %q", got, "original")
	}
}

// The point of the helper: a reader racing a writer sees one whole version or
// the other, never a torn half. A plain os.WriteFile fails this — it truncates
// first, so a reader can catch the file empty or partially rewritten.
func TestConcurrentReadersNeverObserveAPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	oldContents := bytes.Repeat([]byte("A"), 64*1024)
	newContents := bytes.Repeat([]byte("B"), 64*1024)

	if err := Write(path, oldContents, 0644); err != nil {
		t.Fatalf("seed Write: %v", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var torn []int

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if err := Write(path, newContents, 0644); err != nil {
				t.Errorf("Write: %v", err)
				return
			}
			if err := Write(path, oldContents, 0644); err != nil {
				t.Errorf("Write: %v", err)
				return
			}
		}
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				got, err := os.ReadFile(path)
				if err != nil {
					// ENOENT would itself be a tear — the target name must
					// always resolve to some complete version.
					mu.Lock()
					torn = append(torn, -1)
					mu.Unlock()
					continue
				}
				if !bytes.Equal(got, oldContents) && !bytes.Equal(got, newContents) {
					mu.Lock()
					torn = append(torn, len(got))
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if len(torn) > 0 {
		t.Errorf("%d reads observed neither complete version (sizes: %v) — the write is not atomic", len(torn), torn)
	}
}
