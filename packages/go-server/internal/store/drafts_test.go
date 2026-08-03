package store

import (
	"path/filepath"
	"testing"
)

func TestDraftStoreGetEmptyByDefault(t *testing.T) {
	ds, err := openDraftStore(filepath.Join(t.TempDir(), "draft.json"))
	if err != nil {
		t.Fatalf("openDraftStore: %v", err)
	}
	got := ds.Get()
	if got.Text != "" {
		t.Errorf("Get() on fresh store = %+v, want empty text", got)
	}
}

func TestDraftStoreSetAndGet(t *testing.T) {
	ds, err := openDraftStore(filepath.Join(t.TempDir(), "draft.json"))
	if err != nil {
		t.Fatalf("openDraftStore: %v", err)
	}
	if _, err := ds.Set("hello world", "device-1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := ds.Get()
	if got.Text != "hello world" || got.ClientID != "device-1" {
		t.Errorf("Get() = %+v, want Text=%q ClientID=device-1", got, "hello world")
	}
	if got.UpdatedAt == "" {
		t.Error("UpdatedAt not set")
	}
}

func TestDraftStoreClearWithEmptyText(t *testing.T) {
	ds, err := openDraftStore(filepath.Join(t.TempDir(), "draft.json"))
	if err != nil {
		t.Fatalf("openDraftStore: %v", err)
	}
	if _, err := ds.Set("something", "device-1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := ds.Set("", "device-1"); err != nil {
		t.Fatalf("Set (clear): %v", err)
	}
	if got := ds.Get().Text; got != "" {
		t.Errorf("Get().Text after clear = %q, want empty", got)
	}
}

func TestDraftStorePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.json")

	ds1, err := openDraftStore(path)
	if err != nil {
		t.Fatalf("openDraftStore: %v", err)
	}
	if _, err := ds1.Set("saved draft", "device-1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	ds2, err := openDraftStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := ds2.Get(); got.Text != "saved draft" {
		t.Errorf("after reopen Get() = %+v, want Text=saved draft", got)
	}
}
