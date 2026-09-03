// Table-driven coverage for resolveSpawnerChoice's precedence
// (docs/scope-go-spawn.md Stage 4): PARLAY_SPAWN_IMPL / config.toml
// `spawnImpl` override > parlay-bin on PATH > bin/parlay-spawn. Every case
// here is hermetic — PATH points at a t.TempDir() holding only fake shell
// stubs (never a real spawner binary), so no test in this file can ever
// launch a real agent.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveFixture is one row of the precedence table: which fake binaries
// exist on PATH, what override (if any) is set via env or config.toml, and
// the expected outcome.
type resolveFixture struct {
	name string
	// binaries present on the fixture's PATH.
	haveParlayBin, haveParlaySpawn bool
	// override sources; both empty means "no override, auto precedence".
	envImpl    string
	configImpl string

	wantErr       bool
	wantErrSubstr string
	wantName      string // "parlay-bin" or "parlay-spawn", when !wantErr
	wantExplicit  bool
	wantSubArg    string // first argv element, when !wantErr ("spawn" for parlay-bin, "" for bash)
}

func TestResolveSpawnerChoicePrecedence(t *testing.T) {
	cases := []resolveFixture{
		{
			name:          "neither binary on PATH is an error",
			wantErr:       true,
			wantErrSubstr: "no spawner on PATH",
		},
		{
			name:          "auto: parlay-bin only",
			haveParlayBin: true,
			wantName:      "parlay-bin",
			wantExplicit:  false,
			wantSubArg:    "spawn",
		},
		{
			name:            "auto: parlay-spawn only",
			haveParlaySpawn: true,
			wantName:        "parlay-spawn",
			wantExplicit:    false,
		},
		{
			name:            "auto: both present prefers parlay-bin",
			haveParlayBin:   true,
			haveParlaySpawn: true,
			wantName:        "parlay-bin",
			wantExplicit:    false,
			wantSubArg:      "spawn",
		},
		{
			name:          "env override go: picks parlay-bin explicitly",
			haveParlayBin: true,
			envImpl:       "go",
			wantName:      "parlay-bin",
			wantExplicit:  true,
			wantSubArg:    "spawn",
		},
		{
			name:            "env override go: parlay-bin absent is a loud error, no bash fallback",
			haveParlaySpawn: true,
			envImpl:         "go",
			wantErr:         true,
			wantErrSubstr:   "PARLAY_SPAWN_IMPL=go",
		},
		{
			name:            "env override bash: picks parlay-spawn even when parlay-bin is present",
			haveParlayBin:   true,
			haveParlaySpawn: true,
			envImpl:         "bash",
			wantName:        "parlay-spawn",
			wantExplicit:    true,
		},
		{
			name:          "env override bash: parlay-spawn absent is a loud error",
			haveParlayBin: true,
			envImpl:       "bash",
			wantErr:       true,
			wantErrSubstr: "PARLAY_SPAWN_IMPL=bash",
		},
		{
			name:          "env override is case-insensitive",
			haveParlayBin: true,
			envImpl:       "GO",
			wantName:      "parlay-bin",
			wantExplicit:  true,
			wantSubArg:    "spawn",
		},
		{
			name:            "config.toml spawnImpl applies with no env set",
			haveParlaySpawn: true,
			haveParlayBin:   true,
			configImpl:      "bash",
			wantName:        "parlay-spawn",
			wantExplicit:    true,
		},
		{
			name:            "env override beats config.toml",
			haveParlayBin:   true,
			haveParlaySpawn: true,
			envImpl:         "go",
			configImpl:      "bash",
			wantName:        "parlay-bin",
			wantExplicit:    true,
			wantSubArg:      "spawn",
		},
		{
			name:          "invalid override value is a usage error, not silent auto",
			haveParlayBin: true,
			envImpl:       "cobol",
			wantErr:       true,
			wantErrSubstr: `must be "go" or "bash"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PARLAY_STATE_HOME", filepath.Join(home, ".parlay"))

			bin := t.TempDir()
			if tc.haveParlayBin {
				fakeSpawner(t, bin, "parlay-bin", 0)
			}
			if tc.haveParlaySpawn {
				fakeSpawner(t, bin, "parlay-spawn", 0)
			}
			t.Setenv("PATH", bin)

			t.Setenv("PARLAY_SPAWN_IMPL", tc.envImpl)
			if tc.configImpl != "" {
				if err := os.MkdirAll(filepath.Join(home, ".parlay"), 0o755); err != nil {
					t.Fatal(err)
				}
				toml := "spawnImpl = \"" + tc.configImpl + "\"\n"
				if err := os.WriteFile(filepath.Join(home, ".parlay", "config.toml"), []byte(toml), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			choice, err := resolveSpawnerChoice([]string{"id", "name", "#abcdef", "prompt"})

			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveSpawnerChoice() = %+v, nil, want an error containing %q", choice, tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Errorf("resolveSpawnerChoice() error = %q, want it to contain %q", err.Error(), tc.wantErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSpawnerChoice() error = %v, want nil", err)
			}
			if choice.name != tc.wantName {
				t.Errorf("choice.name = %q, want %q", choice.name, tc.wantName)
			}
			if choice.explicit != tc.wantExplicit {
				t.Errorf("choice.explicit = %v, want %v", choice.explicit, tc.wantExplicit)
			}
			if tc.wantSubArg != "" {
				if len(choice.argv) == 0 || choice.argv[0] != tc.wantSubArg {
					t.Errorf("choice.argv = %v, want it to start with %q", choice.argv, tc.wantSubArg)
				}
			} else if len(choice.argv) > 0 && choice.argv[0] == "spawn" {
				t.Errorf("choice.argv = %v, did not expect a leading %q subcommand for %s", choice.argv, "spawn", tc.wantName)
			}
		})
	}
}
