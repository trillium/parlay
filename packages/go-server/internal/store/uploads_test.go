package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUploadStoreSaveWritesFileUnderDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	us, err := openUploadStore(dir)
	if err != nil {
		t.Fatalf("openUploadStore: %v", err)
	}
	name, err := us.Save("photo.png", []byte("fake-png-bytes"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if filepath.Ext(name) != ".png" {
		t.Errorf("Save name = %q, want .png extension kept", name)
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) != "fake-png-bytes" {
		t.Errorf("saved contents = %q, want %q", data, "fake-png-bytes")
	}
}

func TestUploadStoreSaveGeneratesDistinctNames(t *testing.T) {
	us, err := openUploadStore(t.TempDir())
	if err != nil {
		t.Fatalf("openUploadStore: %v", err)
	}
	n1, err := us.Save("a.jpg", []byte("one"))
	if err != nil {
		t.Fatalf("Save 1: %v", err)
	}
	n2, err := us.Save("a.jpg", []byte("two"))
	if err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	if n1 == n2 {
		t.Errorf("two Save calls with the same origName produced the same filename %q", n1)
	}
}

func TestUploadStoreSaveDropsUnsafeExtension(t *testing.T) {
	us, err := openUploadStore(t.TempDir())
	if err != nil {
		t.Fatalf("openUploadStore: %v", err)
	}
	name, err := us.Save("../../etc/passwd", []byte("x"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if filepath.Ext(name) != "" {
		t.Errorf("Save name = %q, want no extension kept from a path-like origName", name)
	}
	if name == ".." || name == "passwd" {
		t.Errorf("Save name = %q, want a generated name, not origName", name)
	}
}

func TestUploadStoreSaveDropsNonImageExtension(t *testing.T) {
	us, err := openUploadStore(t.TempDir())
	if err != nil {
		t.Fatalf("openUploadStore: %v", err)
	}
	for _, origName := range []string{"evil.html", "evil.svg", "evil.js", "notes.txt"} {
		name, err := us.Save(origName, []byte("x"))
		if err != nil {
			t.Fatalf("Save(%q): %v", origName, err)
		}
		if filepath.Ext(name) != "" {
			t.Errorf("Save(%q) name = %q, want no extension kept (only image extensions are allow-listed)", origName, name)
		}
	}
}

func TestUploadStorePathResolvesSavedName(t *testing.T) {
	dir := t.TempDir()
	us, err := openUploadStore(dir)
	if err != nil {
		t.Fatalf("openUploadStore: %v", err)
	}
	name, err := us.Save("a.gif", []byte("x"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := us.Path(name)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != filepath.Join(dir, name) {
		t.Errorf("Path(%q) = %q, want %q", name, got, filepath.Join(dir, name))
	}
}

func TestUploadStorePathRejectsTraversal(t *testing.T) {
	us, err := openUploadStore(t.TempDir())
	if err != nil {
		t.Fatalf("openUploadStore: %v", err)
	}
	for _, bad := range []string{"", ".", "..", "../secret", "sub/dir", `sub\dir`} {
		if _, err := us.Path(bad); err == nil {
			t.Errorf("Path(%q): want error, got nil", bad)
		}
	}
}
