package spawn

import (
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// spawnConfig mirrors the subset of ~/.parlay/config.toml bin/parlay-spawn
// reads (lines 60–133): a top-level spawnAccount key, and a [spawn] table
// with launcher/beads_required. Fields are the raw strings/bool as they
// appear in the file — precedence resolution (env > config > default)
// happens in the caller, matching bash's per-field structure exactly rather
// than folding it into this loader.
type spawnConfig struct {
	SpawnAccount string `toml:"spawnAccount"`
	Spawn        struct {
		Launcher      string `toml:"launcher"`
		BeadsRequired any    `toml:"beads_required"`
	} `toml:"spawn"`
}

// configTomlPath mirrors bash's `${PARLAY_STATE_HOME:-$HOME/.parlay}/config.toml`.
func configTomlPath() string {
	base := os.Getenv("PARLAY_STATE_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".parlay")
	}
	return filepath.Join(base, "config.toml")
}

// loadSpawnConfig reads config.toml, returning a zero-value spawnConfig on
// any error (missing file, bad TOML) — bash's silent-fallthrough-on-missing-
// python3 parity contract: a config problem never aborts a spawn, callers
// just see empty/default fields.
func loadSpawnConfig() spawnConfig {
	var cfg spawnConfig
	data, err := os.ReadFile(configTomlPath())
	if err != nil {
		return cfg
	}
	_ = toml.Unmarshal(data, &cfg)
	return cfg
}

// resolveDefaultAccount mirrors bash lines 60–73: PARLAY_SPAWN_DEFAULT_ACCOUNT
// env wins, else config.toml's top-level spawnAccount, else "".
func resolveDefaultAccount(cfg spawnConfig) string {
	if v := os.Getenv("PARLAY_SPAWN_DEFAULT_ACCOUNT"); v != "" {
		return v
	}
	return cfg.SpawnAccount
}

// resolveLauncher mirrors bash lines 75–103: PARLAY_SPAWN_LAUNCHER env wins,
// else config.toml's [spawn].launcher, else "herdr". "gascity" (the
// pre-rename spelling) normalizes to "subprocess" with a deprecation notice
// left to the caller (matching bash, which prints it inline at this same
// resolution point — see resolveLauncherWithNotice below for the CLI path
// that needs the notice text).
func resolveLauncher(cfg spawnConfig) string {
	resolved := os.Getenv("PARLAY_SPAWN_LAUNCHER")
	if resolved == "" {
		resolved = cfg.Spawn.Launcher
	}
	if resolved == "" {
		resolved = "herdr"
	}
	if resolved == "gascity" {
		return "subprocess"
	}
	return resolved
}

// resolveBeadsRequired mirrors bash lines 107–133: PARLAY_SPAWN_BEADS_REQUIRED
// env wins, else config.toml's [spawn].beads_required (a TOML bool or one of
// the string spellings "1"/"true"/"yes"/"on", case-insensitive), else off.
func resolveBeadsRequired(cfg spawnConfig) bool {
	raw := os.Getenv("PARLAY_SPAWN_BEADS_REQUIRED")
	if raw == "" {
		switch v := cfg.Spawn.BeadsRequired.(type) {
		case bool:
			if v {
				raw = "1"
			}
		case string:
			raw = v
		}
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
