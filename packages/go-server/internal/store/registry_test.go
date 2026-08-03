package store

import (
	"path/filepath"
	"testing"
)

func TestRegistryStoreUpsertAndGet(t *testing.T) {
	rs, err := openRegistryStore(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatalf("openRegistryStore: %v", err)
	}

	if _, err := rs.Upsert(AgentInfo{ID: "c0", Name: "Foundation", Color: "#34d399"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok := rs.Get("c0")
	if !ok {
		t.Fatal("Get(c0) not found after Upsert")
	}
	if got.Name != "Foundation" || got.Color != "#34d399" {
		t.Errorf("Get(c0) = %+v, want Name=Foundation Color=#34d399", got)
	}
}

func TestRegistryStoreUpsertMergesPartialUpdate(t *testing.T) {
	rs, err := openRegistryStore(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatalf("openRegistryStore: %v", err)
	}

	if _, err := rs.Upsert(AgentInfo{ID: "c0", Name: "Foundation", Color: "#34d399", Nicknames: []string{"c-zero"}}); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	// A second call (e.g. re-register on restart) sending only id+name must
	// not blank out color/nicknames set by the first call.
	updated, err := rs.Upsert(AgentInfo{ID: "c0", Name: "Foundation v2"})
	if err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}
	if updated.Name != "Foundation v2" {
		t.Errorf("Name = %q, want updated value", updated.Name)
	}
	if updated.Color != "#34d399" {
		t.Errorf("Color = %q, want preserved from first Upsert", updated.Color)
	}
	if len(updated.Nicknames) != 1 || updated.Nicknames[0] != "c-zero" {
		t.Errorf("Nicknames = %v, want preserved from first Upsert", updated.Nicknames)
	}
}

func TestRegistryStoreUpsertClearsNicknamesWithEmptySlice(t *testing.T) {
	rs, err := openRegistryStore(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatalf("openRegistryStore: %v", err)
	}
	if _, err := rs.Upsert(AgentInfo{ID: "c0", Nicknames: []string{"c-zero"}}); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	updated, err := rs.Upsert(AgentInfo{ID: "c0", Nicknames: []string{}})
	if err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}
	if len(updated.Nicknames) != 0 {
		t.Errorf("Nicknames = %v, want cleared by explicit empty slice", updated.Nicknames)
	}
}

func TestRegistryStoreRemove(t *testing.T) {
	rs, err := openRegistryStore(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatalf("openRegistryStore: %v", err)
	}
	if _, err := rs.Upsert(AgentInfo{ID: "c0"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	removed, err := rs.Remove("c0")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !removed {
		t.Error("Remove(c0) = false, want true")
	}
	if _, ok := rs.Get("c0"); ok {
		t.Error("Get(c0) still found after Remove")
	}

	// Unregistering an unknown id is documented as an idempotent no-op.
	removed, err = rs.Remove("does-not-exist")
	if err != nil {
		t.Fatalf("Remove(unknown): %v", err)
	}
	if removed {
		t.Error("Remove(unknown) = true, want false")
	}
}

func TestRegistryStoreListSortedByID(t *testing.T) {
	rs, err := openRegistryStore(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatalf("openRegistryStore: %v", err)
	}
	for _, id := range []string{"c2", "c0", "c1"} {
		if _, err := rs.Upsert(AgentInfo{ID: id}); err != nil {
			t.Fatalf("Upsert %s: %v", id, err)
		}
	}
	list := rs.List()
	if len(list) != 3 || list[0].ID != "c0" || list[1].ID != "c1" || list[2].ID != "c2" {
		t.Errorf("List() = %+v, want sorted [c0 c1 c2]", list)
	}
}

func TestRegistryStorePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.json")

	rs1, err := openRegistryStore(path)
	if err != nil {
		t.Fatalf("openRegistryStore: %v", err)
	}
	if _, err := rs1.Upsert(AgentInfo{ID: "c0", Name: "Foundation"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	rs2, err := openRegistryStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := rs2.Get("c0")
	if !ok || got.Name != "Foundation" {
		t.Errorf("after reopen Get(c0) = %+v, ok=%v, want Name=Foundation ok=true", got, ok)
	}
}

func TestRegistryStoreUpsertRequiresID(t *testing.T) {
	rs, err := openRegistryStore(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatalf("openRegistryStore: %v", err)
	}
	if _, err := rs.Upsert(AgentInfo{Name: "no id"}); err == nil {
		t.Error("Upsert with empty ID: want error, got nil")
	}
}
