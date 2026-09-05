// `parlay spawn-account` — show/persist/clear the default ccjuggler spawn
// account, ported from packages/cli/src/commands-spawn-account.ts (the TS
// tree at 871b3f8f^; the verb was the one dispatch gap left after T-08 —
// robots-ni5p). The Go-only `parlay defaults` verb covers the same
// functionality with an env-aware show; this verb keeps the TS name and
// contract alive: the show form reads the config file ONLY (never
// PARLAY_SPAWN_DEFAULT_ACCOUNT), exactly like TS's persistedSpawnAccount().
package commands

import (
	"fmt"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// SpawnAccount ports commands-spawn-account.ts's cmdSpawnAccount:
// show (bare) / set <account> / clear the persisted default spawn account in
// $PARLAY_STATE_HOME/config.toml — the file the spawn pipeline reads.
func SpawnAccount(argv []string) {
	if helpWanted("spawn-account", argv) {
		return
	}
	r := args.Parse("spawn-account", argv, nil, nil)
	var sub, account string
	if len(r.Positionals) > 0 {
		sub = r.Positionals[0]
	}
	if len(r.Positionals) > 1 {
		account = r.Positionals[1]
	}

	switch sub {
	case "":
		if current := config.PersistedSpawnAccount(); current != "" {
			fmt.Printf("%s (source: config, %s)\n", current, config.SpawnAccountConfigPath())
		} else {
			fmt.Println("(none — set with: parlay spawn-account set <account>)")
		}

	case "set":
		if account == "" {
			httpc.Die("parlay spawn-account set: account name required, e.g. parlay spawn-account set acc2", config.ExitUsage)
			return
		}
		if err := config.SetSpawnAccount(account); err != nil {
			httpc.Die(fmt.Sprintf("parlay spawn-account set: %v", err), config.ExitRuntime)
			return
		}
		fmt.Printf("persisted default spawn account: %s (%s)\n", account, config.SpawnAccountConfigPath())

	case "clear":
		if err := config.SetSpawnAccount(""); err != nil {
			httpc.Die(fmt.Sprintf("parlay spawn-account clear: %v", err), config.ExitRuntime)
			return
		}
		fmt.Printf("cleared persisted default spawn account (%s)\n", config.SpawnAccountConfigPath())

	default:
		httpc.Die(fmt.Sprintf("parlay spawn-account: unknown subcommand %q — expected \"set <account>\" or \"clear\"", sub), config.ExitUsage)
	}
}
