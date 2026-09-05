// Package commands/docs: `parlay defaults` — show or persist the CLI's
// default ccjuggler spawn account (the account agents come up under) plus the
// current server URL, in one glance. The show form resolves through the same
// precedence the spawn pipeline uses — PARLAY_SPAWN_DEFAULT_ACCOUNT env var >
// `spawnAccount` in config.toml > none — so what is displayed is exactly what
// a spawn picks up. The set/clear forms persist the config.toml half of that
// chain; the server URL half lives under `parlay remote` and is shown here
// read-only.
package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// Defaults is `parlay defaults`'s entry point: show / set / clear the
// persisted spawn account, and always show where the server URL resolves from.
func Defaults(argv []string) {
	if helpWanted("defaults", argv) {
		return
	}
	r := args.Parse("defaults", argv, nil, nil)
	if len(r.Positionals) == 0 {
		showDefaults()
		return
	}

	switch r.Positionals[0] {
	case "set":
		if len(r.Positionals) < 3 || r.Positionals[1] != "account" || strings.TrimSpace(r.Positionals[2]) == "" {
			httpc.Die("parlay defaults set: usage: parlay defaults set account <n>", config.ExitUsage)
			return
		}
		account := r.Positionals[2]
		if err := config.SetSpawnAccount(account); err != nil {
			httpc.Die(fmt.Sprintf("parlay defaults set: %v", err), config.ExitRuntime)
			return
		}
		fmt.Printf("persisted default spawn account: %s (%s)\n", account, config.SpawnAccountConfigPath())

	case "clear":
		if len(r.Positionals) < 2 || r.Positionals[1] != "account" {
			httpc.Die("parlay defaults clear: usage: parlay defaults clear account", config.ExitUsage)
			return
		}
		if err := config.SetSpawnAccount(""); err != nil {
			httpc.Die(fmt.Sprintf("parlay defaults clear: %v", err), config.ExitRuntime)
			return
		}
		fmt.Printf("cleared persisted spawn account (%s)\n", config.SpawnAccountConfigPath())

	default:
		httpc.Die(fmt.Sprintf("parlay defaults: unknown subcommand %q — expected \"set account <n>\" or \"clear account\"", r.Positionals[0]), config.ExitUsage)
	}
}

// showDefaults prints the resolved server URL + resolved spawn account, each
// with the precedence source they came from.
func showDefaults() {
	info := config.ServerSource()
	fmt.Printf("server:       %s (source: %s)\n", info.URL, info.Source)

	account := config.SpawnAccount()
	switch {
	case account == "":
		fmt.Println("spawnAccount: (none — set with: parlay defaults set account <n>)")
	case strings.TrimSpace(os.Getenv(config.SpawnAccountEnv)) != "":
		fmt.Printf("spawnAccount: %s (source: env, %s)\n", account, config.SpawnAccountEnv)
	default:
		fmt.Printf("spawnAccount: %s (source: config, %s)\n", account, config.SpawnAccountConfigPath())
	}
}
