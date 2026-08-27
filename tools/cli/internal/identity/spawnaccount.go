// Which ccjuggler account an agent respawns under. Shared by every relaunch
// path so the precedence cannot drift between them: `parlay launch <id>`
// (internal/commands/launch.go) and `identity --launch <id>`
// (lifecycle.go's HandleLaunch, which `parlay reset --reboot` and
// `identity --submit` both funnel through).
package identity

import (
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// SpawnAccountArgs returns the `--account <name>` argv pair a spawner needs
// to put the agent on its own ccjuggler token, or nil when no account is
// configured at all.
//
// identityAccount is the identity's `account:` frontmatter value; it wins,
// and an unset (or whitespace-only) one falls back to config.SpawnAccount().
//
// Passing the configured default explicitly is not redundant: resolveSpawner
// PREFERS parlay-bin, and parlay-bin reads neither config.toml nor
// PARLAY_SPAWN_DEFAULT_ACCOUNT — only its own --account flag. Only the bash
// bin/parlay-spawn derives the default itself, so without this a relaunched
// agent silently came up on the launching shell's ambient session token.
//
// Returning nil rather than an empty value is load-bearing: the spawner
// exits 2 on a valueless --account, so an empty string would turn "no
// account configured" into a hard launch failure.
func SpawnAccountArgs(identityAccount string) []string {
	account := strings.TrimSpace(identityAccount)
	if account == "" {
		account = config.SpawnAccount()
	}
	if account == "" {
		return nil
	}
	return []string{"--account", account}
}
