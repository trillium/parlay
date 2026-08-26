# Go CLI ticket B9: `launch`/`drawdown`/`idle`

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`tools/cli/internal/commands/{launch,drawdown,idle}.go` port `cmdLaunch`/
`cmdDrawdown`/`cmdIdle` from `packages/cli/src/commands/{launch,drawdown,
idle}.ts` (that source has since been split from the older single
`commands.ts` file some ticket briefs still cite — read the actual
`packages/cli/src/commands/*.ts` files directly, not `commands.ts`). Two
things worth knowing before touching this code:

- **`launch` resolves its spawner at runtime — never hardcode one binary
  name (robots-v81b)**. Ticket A1 renamed `bin/parlay-spawn` (bash) to
  `tools/parlay-bin` (Go, `spawn`/`reset` subcommands), and `launch.ts`
  followed the new name while `commands-variant.ts` kept the old one. That
  divergence was documented here as harmless; it was not. **`parlay-bin` is
  built by no install path in this repo** — `bin/parlay` builds `tools/cli`
  only — so on the captain's box, where `~/.local/bin` carries the
  `bin/parlay-spawn` symlink and nothing named `parlay-bin`, every `parlay
  launch <id>` exec'd a nonexistent binary. Compounding it, both CLIs
  discarded the spawn result (`_ = cmd.Run()` / an unchecked
  `Bun.spawnSync`), so ENOENT was indistinguishable from success: an
  announcement, exit 0, and no agent. Both now walk `parlay-bin` (with the
  `spawn` subcommand) then `parlay-spawn` (bare positionals), take the first
  on PATH, die loudly when neither resolves, and treat a non-zero spawner
  exit as a failed launch. The two names are still both live on purpose —
  the resolution order is the contract, not either binary. `variant.go`
  still calls `parlay-spawn` directly; it is the one that always existed.
- **`launch.go`'s `knownAgents()` reuses `guard.go`'s
  `readLocalFrontmatter`/`parlayAgentsDir`/`parlayHomeDir`** instead of a
  fourth copy of the local frontmatter parser — `launch.ts` defines its own
  `parseFrontmatter` with the identical regex pair
  (`^---\n([\s\S]*?)\n---` block, `^(\w+):\s*"?([^"]*)"?\s*$` KV) already
  duplicated by `commands-teardown.ts`/`commands-variant.ts` and already
  consolidated once in `guard.go` for the Go port (see the B4 section
  above) — same hardcoded non-env-aware `~/.parlay/agents` resolution, so
  the existing helpers apply directly.
