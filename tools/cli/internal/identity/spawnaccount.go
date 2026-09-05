// Which ccjuggler account an agent respawns under. Shared by every relaunch
// path so the precedence cannot drift between them: `parlay launch <id>`
// (internal/commands/launch.go) and `identity --launch <id>`
// (lifecycle.go's HandleLaunch, which `parlay reset --reboot` and
// `identity --submit` both funnel through).
package identity

import "strings"

// SpawnAccountArgs returns the `--account <name>` argv pair a spawner needs
// to put the agent on its own ccjuggler token, or nil when the identity pins
// no account of its own.
//
// identityAccount is the identity's `account:` frontmatter value, and it is
// the ONLY source consulted here. The config-level default
// (PARLAY_SPAWN_DEFAULT_ACCOUNT env, else config.toml's spawnAccount) is
// deliberately NOT synthesized into the argv: the spawn pipeline
// (internal/spawn) resolves that default itself on every launch, so an
// unpinned agent still lands on it, and it stays a live default rather than
// something a relaunch freezes. Since task-0d6mi the pipeline also PERSISTS
// an explicitly-passed --account back into identity.md, so a synthesized
// default would pin itself on the first relaunch and outrank every later
// `parlay defaults set account` rotation.
//
// The identity's own account must still be passed explicitly: the pipeline
// knows nothing of identity frontmatter, so without this a relaunched agent
// with a pinned account silently came up on the wrong token.
//
// Returning nil rather than an empty value is load-bearing: the spawner
// exits 2 on a valueless --account, so an empty string would turn "no
// account pinned" into a hard launch failure.
func SpawnAccountArgs(identityAccount string) []string {
	account := strings.TrimSpace(identityAccount)
	if account == "" {
		return nil
	}
	return []string{"--account", account}
}
