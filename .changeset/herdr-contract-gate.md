---
"@parlay/cli": minor
---

Add a **herdr contract gate** (robots-4v2e) so herdr CLI drift can no longer silently break mechanic-dispatch. `parlay-spawn` shells out to `herdr` with no version pin or shape check, so in one day three separate herdr upgrades (robots-1uez `agent start --cwd` removal, robots-i4pi `agent_pane_busy` race, robots-tzpe `agent wait --status`→`--until`) each broke live dispatch as a cryptic mid-launch `unknown option` that orphaned a tab.

- **`bin/herdr-contract`** — single source of truth for the herdr subcommands/flags `parlay-spawn` depends on, plus a preflight (`herdr_contract_check`): asserts a pinned version range (`0.7.5 ≤ v < 0.9.0`, env-overridable) as a loud tripwire, and hard-fails naming the exact drifted flag/subcommand. Sourceable or runnable standalone (`herdr-contract`).
- **`bin/parlay-spawn`** now sources it and gates at startup **before** registering the agent, so a drifted herdr fails fast with a precise message instead of a rollback mid-dispatch. Escape hatch: `PARLAY_SPAWN_SKIP_CONTRACT=1`. (Symlink-resolves its own path so the gate loads through the `~/.local/bin` install.)
- **`bin/pipeline-selftest`** gains a `contract` check (now the first hop) as the post-`herdr update` gate.
