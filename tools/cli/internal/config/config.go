// Package config is the parlay CLI's shared config: server URL resolution,
// persisted config file, and process exit codes.
//
// Ported from packages/cli/src/config.ts — see docs/scope-go-cli.md §5 item 5
// for why the resolution precedence and env var names must stay exact
// (PARLAY_SERVER, PARLAY_STATE_HOME; asserted by `parlay doctor` and this
// repo's CLAUDE.md).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DefaultServer is the coded fallback when neither PARLAY_SERVER nor a
// persisted config value is set.
const DefaultServer = "http://localhost:4242"

// Exit codes: 0 = ok, 1 = runtime/server error, 2 = usage error (bad flag/command/args).
const (
	ExitOK      = 0
	ExitRuntime = 1
	ExitUsage   = 2
)

// TruncateAt is the default line-truncation width used by internal/format.
const TruncateAt = 100

// StateHome is the root directory for all persisted parlay state. Same
// override convention as commands-guard.ts / robots-watch's cursor.ts:
// $PARLAY_STATE_HOME (default ~/.parlay). Tests should set this env var to a
// tmp dir so a persisted config on the machine running them is never read.
func StateHome() string {
	if h := os.Getenv("PARLAY_STATE_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".parlay")
}

func configPath() string {
	return filepath.Join(StateHome(), "config.json")
}

type persistedConfig struct {
	Server string `json:"server,omitempty"`
}

// A missing or corrupt config file is treated as empty — resolution falls
// through to the next precedence level, matching config.ts's try/catch.
func readPersistedConfig() persistedConfig {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return persistedConfig{}
	}
	var cfg persistedConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return persistedConfig{}
	}
	return cfg
}

func writePersistedConfig(cfg persistedConfig) error {
	dir := StateHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	encErr := enc.Encode(cfg)
	// Sync before Close, and before the rename: a rename that lands ahead of
	// the data publishes a correctly-named config file holding nothing, which
	// is the exact failure an "atomic swap" is supposed to rule out. Only
	// attempted when the encode succeeded — there is nothing worth flushing
	// otherwise, and the encode error is the one the caller needs.
	var syncErr error
	if encErr == nil {
		syncErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if encErr != nil {
		os.Remove(tmpPath)
		return encErr
	}
	if syncErr != nil {
		os.Remove(tmpPath)
		return syncErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}

	return os.Rename(tmpPath, configPath()) // atomic swap
}

// SetPersistedServer persists url (trailing slashes trimmed) as the default
// server URL in $PARLAY_STATE_HOME/config.json. An empty url clears the
// persisted value, same as config.ts's setPersistedServer(undefined).
func SetPersistedServer(url string) error {
	cfg := readPersistedConfig()
	cfg.Server = strings.TrimRight(url, "/")
	return writePersistedConfig(cfg)
}

// PersistedServerURL returns the persisted server URL, or "" if unset.
func PersistedServerURL() string {
	return strings.TrimSpace(readPersistedConfig().Server)
}

// ConfigFilePath returns the path to the persisted config file.
func ConfigFilePath() string {
	return configPath()
}

// ServerURL resolves the server base URL, trimming trailing slashes.
// Precedence: PARLAY_SERVER env var (explicit, per-shell override) >
// persisted config (~/.parlay/config.json, set via `parlay remote set`) >
// coded default. Read lazily on every call — mirrors config.ts's serverUrl()
// — so a PARLAY_SERVER set after process start (e.g. in a test) is honored.
func ServerURL() string {
	if env := strings.TrimSpace(os.Getenv("PARLAY_SERVER")); env != "" {
		return strings.TrimRight(env, "/")
	}
	if persisted := PersistedServerURL(); persisted != "" {
		return strings.TrimRight(persisted, "/")
	}
	return DefaultServer
}

// ServerSourceKind names which precedence level ServerSource() resolved from.
type ServerSourceKind string

const (
	SourceEnv     ServerSourceKind = "env"
	SourceConfig  ServerSourceKind = "config"
	SourceDefault ServerSourceKind = "default"
)

// ServerSourceInfo is the resolved server URL plus which source it came from —
// used by `parlay doctor` / `parlay remote` to explain resolution to the user.
type ServerSourceInfo struct {
	Source ServerSourceKind
	URL    string
}

// ServerSource reports which precedence level is currently in effect.
func ServerSource() ServerSourceInfo {
	if env := strings.TrimSpace(os.Getenv("PARLAY_SERVER")); env != "" {
		return ServerSourceInfo{SourceEnv, strings.TrimRight(env, "/")}
	}
	if persisted := PersistedServerURL(); persisted != "" {
		return ServerSourceInfo{SourceConfig, persisted}
	}
	return ServerSourceInfo{SourceDefault, DefaultServer}
}
