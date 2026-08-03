package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Draft matches the shape read/written by GET/PUT /api/chat/draft in
// docs/api-contract.md (§Drafts).
type Draft struct {
	Text      string `json:"text"`
	ClientID  string `json:"clientId,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// DraftStore holds a single current draft. The documented contract has no
// per-device id on GET /draft — only PUT sends clientId, and only to
// suppress a device's own echo over SSE (see the client callers cited in
// docs/api-contract.md §Drafts), not to key separate drafts. Combined with
// this being a single-user personal deployment (docs/scope-go-server.md §5
// risk #11), one global "current draft" record is the simplest model that
// still matches what the contract actually describes. If a later ticket
// finds evidence of real per-device drafts, this is the place to add a
// keyed map instead.
type DraftStore struct {
	mu    sync.RWMutex
	path  string
	draft Draft
}

func openDraftStore(path string) (*DraftStore, error) {
	ds := &DraftStore{path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ds, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &ds.draft); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return ds, nil
}

// Get returns the currently saved draft (zero value if none saved yet).
func (ds *DraftStore) Get() Draft {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return ds.draft
}

// Set saves (or, with text=="", clears) the current draft.
func (ds *DraftStore) Set(text, clientID string) (Draft, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.draft = Draft{Text: text, ClientID: clientID, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	data, err := json.Marshal(ds.draft)
	if err != nil {
		return Draft{}, fmt.Errorf("marshal draft: %w", err)
	}
	if err := writeFileAtomic(ds.path, data, 0o644); err != nil {
		return Draft{}, fmt.Errorf("write %s: %w", ds.path, err)
	}
	return ds.draft, nil
}
