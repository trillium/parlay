// Package store is the persistence layer for the Go rewrite of the Pulse
// chat server (packages/go-server). The real TypeScript server's on-disk
// format cannot be read (every handler under packages/server/src/ is a
// broken symlink loop — see this repo's CLAUDE.md), so nothing here can be
// a byte-for-byte port of live data. Every format below is a clean-slate
// design chosen for minimal custom parsing and for this project's
// single-user, single-instance deployment model — not reverse-engineered
// from anything. See docs/api-contract.md for the HTTP-level behavioral
// spec this storage layer is built to serve.
//
// MIGRATION: if the real TS server's on-disk format is ever recovered (the
// symlink loop under ~/.claude/PAI/PULSE gets fixed), a one-time import
// script must translate that data into the shapes defined in this package
// before this server can inherit the live captain's history/registry/
// settings — do not assume the two are wire-compatible. Until then this
// server starts from empty state on first run, like any new deployment.
//
// Layout under the state directory (PARLAY_STATE_HOME, default ~/.parlay —
// matching packages/server/src/debug-log.ts's own convention):
//
//	messages.jsonl   append-only chat history, one ChatMessage per line
//	agents.json      full-snapshot agent registry, atomic rewrite on change
//	draft.json       full-snapshot single current draft, atomic rewrite
//	settings.json    full-snapshot ParlaySettings, atomic rewrite
//	uploads/         one file per uploaded attachment, named by UploadStore.Save
//
// PresenceTracker and CommandRegistry have no on-disk form at all: both hold
// state that is only true while this process is up, and each says why in its
// own doc comment.
//
// Every substore owns its own sync.RWMutex and exposes a narrow method set
// (Append/History, Upsert/List, Get/Set, Get/Replace) rather than raw file
// access, so this format can change later without touching
// internal/handlers — the whole point of isolating storage in this ticket.
package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// Store aggregates every substore the server needs. Each field is safe for
// concurrent use independently; Store itself holds no additional lock.
type Store struct {
	Messages *MessageStore
	Registry *RegistryStore
	Drafts   *DraftStore
	Settings *SettingsStore
	Presence *PresenceTracker
	Uploads  *UploadStore
	Commands *CommandRegistry
}

// Config controls where and how much Open persists.
type Config struct {
	Dir             string // state directory; created if missing
	MaxMessages     int    // ring-buffer cap; <=0 uses DefaultMaxMessages
	MaxHistoryBytes int64  // messages.jsonl compaction trigger; <=0 uses DefaultMaxHistoryBytes
}

// Open loads (or initializes) every substore from cfg.Dir, creating the
// directory if it does not exist. Call once at startup; the returned
// substores are safe for concurrent use afterward.
func Open(cfg Config) (*Store, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("store: Config.Dir is required")
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create state dir %s: %w", cfg.Dir, err)
	}

	messages, err := openMessageStore(filepath.Join(cfg.Dir, "messages.jsonl"), cfg.MaxMessages, cfg.MaxHistoryBytes)
	if err != nil {
		return nil, fmt.Errorf("store: messages: %w", err)
	}
	registry, err := openRegistryStore(filepath.Join(cfg.Dir, "agents.json"))
	if err != nil {
		messages.Close()
		return nil, fmt.Errorf("store: registry: %w", err)
	}
	drafts, err := openDraftStore(filepath.Join(cfg.Dir, "draft.json"))
	if err != nil {
		messages.Close()
		return nil, fmt.Errorf("store: drafts: %w", err)
	}
	settings, err := openSettingsStore(filepath.Join(cfg.Dir, "settings.json"))
	if err != nil {
		messages.Close()
		return nil, fmt.Errorf("store: settings: %w", err)
	}
	uploads, err := openUploadStore(filepath.Join(cfg.Dir, "uploads"))
	if err != nil {
		messages.Close()
		return nil, fmt.Errorf("store: uploads: %w", err)
	}

	return &Store{
		Messages: messages,
		Registry: registry,
		Drafts:   drafts,
		Settings: settings,
		Presence: newPresenceTracker(),
		Uploads:  uploads,
		Commands: NewCommandRegistry(CommandRegistryConfig{}),
	}, nil
}

// writeFileAtomic writes data to path via a temp file + rename in the same
// directory, so a crash or concurrent reader never observes a partially
// written file, and the rename stays on one filesystem.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed away

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
