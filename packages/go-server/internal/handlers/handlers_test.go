package handlers

import (
	"testing"

	"parlay/go-server/internal/store"
)

// newTestStore opens a Store rooted at a fresh temp dir and registers
// cleanup to close it — the shared setup every handler test in this package
// needs.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(store.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Messages.Close() })
	return st
}
