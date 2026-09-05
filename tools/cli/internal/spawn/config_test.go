package spawn

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfigToml(t *testing.T, contents string) {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", stateHome)
	if contents == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(stateHome, "config.toml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSpawnConfigMissingFileReturnsZeroValue(t *testing.T) {
	writeConfigToml(t, "")
	cfg := loadSpawnConfig()
	if cfg.SpawnAccount != "" || cfg.Spawn.Launcher != "" {
		t.Errorf("expected zero-value config, got %+v", cfg)
	}
}

func TestLoadSpawnConfigBadTOMLReturnsZeroValue(t *testing.T) {
	writeConfigToml(t, "this is not [ valid toml")
	cfg := loadSpawnConfig()
	if cfg.SpawnAccount != "" || cfg.Spawn.Launcher != "" {
		t.Errorf("expected zero-value config on parse failure, got %+v", cfg)
	}
}

func TestResolveDefaultAccount(t *testing.T) {
	t.Run("env wins over config", func(t *testing.T) {
		writeConfigToml(t, `spawnAccount = "from-config"`)
		t.Setenv("PARLAY_SPAWN_DEFAULT_ACCOUNT", "from-env")
		cfg := loadSpawnConfig()
		if got := resolveDefaultAccount(cfg); got != "from-env" {
			t.Errorf("got %q, want from-env", got)
		}
	})
	t.Run("config used when env unset", func(t *testing.T) {
		writeConfigToml(t, `spawnAccount = "from-config"`)
		cfg := loadSpawnConfig()
		if got := resolveDefaultAccount(cfg); got != "from-config" {
			t.Errorf("got %q, want from-config", got)
		}
	})
	t.Run("empty when neither set", func(t *testing.T) {
		writeConfigToml(t, "")
		cfg := loadSpawnConfig()
		if got := resolveDefaultAccount(cfg); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestResolveLauncher(t *testing.T) {
	cases := []struct {
		name   string
		env    string
		config string
		want   string
	}{
		{"defaults to herdr", "", "", "herdr"},
		{"env wins over config", "subprocess", `[spawn]
launcher = "gc"`, "subprocess"},
		{"config used when env unset", "", `[spawn]
launcher = "gc"`, "gc"},
		{"gascity normalizes to subprocess via env", "gascity", "", "subprocess"},
		{"gascity normalizes to subprocess via config", "", `[spawn]
launcher = "gascity"`, "subprocess"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeConfigToml(t, tc.config)
			if tc.env != "" {
				t.Setenv("PARLAY_SPAWN_LAUNCHER", tc.env)
			}
			cfg := loadSpawnConfig()
			if got := resolveLauncher(cfg); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveBeadsRequired(t *testing.T) {
	cases := []struct {
		name   string
		env    string
		config string
		want   bool
	}{
		{"defaults to off", "", "", false},
		{"env 1 is on", "1", "", true},
		{"env true is on", "true", "", true},
		{"env yes is on", "yes", "", true},
		{"env on is on", "on", "", true},
		{"env garbage is off", "nonsense", "", false},
		{"config bool true", "", `[spawn]
beads_required = true`, true},
		{"config bool false", "", `[spawn]
beads_required = false`, false},
		{"config string yes", "", `[spawn]
beads_required = "yes"`, true},
		{"env overrides config", "0", `[spawn]
beads_required = true`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeConfigToml(t, tc.config)
			if tc.env != "" {
				t.Setenv("PARLAY_SPAWN_BEADS_REQUIRED", tc.env)
			}
			cfg := loadSpawnConfig()
			if got := resolveBeadsRequired(cfg); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
