---
name: parlay-spawn
description: Spawn a new parlay agent using bin/parlay-spawn. Covers the full invocation signature, how account/token injection works, and what each option does.
argument-hint: "What kind of agent do you want to spawn?"
disable-model-invocation: false
---

# How to spawn a parlay agent

Use `bin/parlay-spawn` directly from the `~/code/parlay` working directory, or via `parlay-spawn` if it's on PATH.

## Signature

```
parlay-spawn <id> <name> <color> <task> [options]
```

| Arg | Description |
|-----|-------------|
| `<id>` | Short slug — becomes the agent's registration name (e.g. `pr-auditor`, `gascity-scope`) |
| `<name>` | Display name shown in the panel (quoted, spaces OK) |
| `<color>` | Hex color for the panel tab (e.g. `"#f97316"`) |
| `<task>` | Multi-line task description delivered as the agent's startup prompt |

## Common options

| Flag | Description |
|------|-------------|
| `--cwd <path>` | Working directory for the agent (default: caller's cwd) |
| `--account <name>` | ccjuggler account to inject as `CLAUDE_CODE_OAUTH_TOKEN` (e.g. `acc2`) |
| `--pane <id>` | In-place mode: reuse caller's existing herdr pane instead of creating a new tab |
| `--worktree` | Spawn in an isolated git worktree (created under `<repo>/.worktrees/parlay-<id>`) |
| `--claim <bead-id>` | Associate with an existing task bead |
| `--workspace <id>` | Place tab in a specific herdr workspace |

## Default account

The default ccjuggler account is read from `~/.parlay/config.toml` (`spawnAccount` field).
Set it with:
```
parlay spawn-account set acc2
parlay spawn-account          # show current
parlay spawn-account clear    # remove
```
Env var `PARLAY_SPAWN_DEFAULT_ACCOUNT` overrides the config. `--account` overrides both.

## Token injection

`ccjuggler-resolve <account>` (from `packages/ccjuggler`) resolves the OAuth token:
1. macOS keychain: `security find-generic-password -a ccjuggler -s ccjuggler-<account>`
2. Flat file: `~/.ccjuggler/<account>/.oauth-token`

Spawn exits non-zero with a clear error if no token is found. Token is injected as `CLAUDE_CODE_OAUTH_TOKEN` via `herdr tab create --env` (or exported directly in `--pane` in-place mode).

## Typical examples

```bash
# Spawn a background research agent on acc2 (from config.toml)
parlay-spawn gascity-scope "Gascity Scope" "#f59e0b" \
  "Auto-discover what gascity is … reply when done." \
  --cwd ~/code/parlay

# Explicit account override
parlay-spawn pr-auditor "PR Auditor" "#f97316" \
  "Audit PRs #78 #82 #98 in trillium/parlay …" \
  --cwd ~/code/parlay --account acc1

# Worktree-isolated agent
parlay-spawn refactor-x "Refactor X" "#6366f1" \
  "Refactor the auth layer …" \
  --cwd ~/code/myapp --worktree
```

## Spawn stages (what happens after you run it)

1. Register agent name with Parlay server
2. `herdr tab create` — opens a new terminal tab, injects env vars
3. Pane prep — clears inherited claude env vars, echoes `READY_$$`, waits for shell
4. `herdr agent start` — launches the claude harness in the pane
5. Retry loop — polls until agent state is `running`
6. `herdr agent prompt` — delivers the task description as the startup prompt
7. `sleep 3` — brief post-prompt settle
8. `parlay identity --register` — seeds the launch spec (worktree, project, etc.)
9. Liveness watchdog — nudges if the agent goes idle > 60s

## Snapshot logs

Every spawn writes a hash-deduped JSONL log of pane content at each stage to:
```
~/.parlay/spawn-logs/<agent-id>.jsonl
```
Useful for diagnosing stuck spawns. Capped at 1000 entries.

## Troubleshooting

- **"ccjuggler-resolve not found"** — run `bun install` in `packages/ccjuggler` or ensure the bin is on PATH.
- **"no root pane returned"** — herdr tab create failed; check `herdr` is running and the socket path is valid.
- **Agent registers but never receives prompt** — check the spawn-logs JSONL for which stage stalled.
- **Wrong worktree / repo** — `treehouse get` resolves from the process cwd; always pass `--cwd <repo>` explicitly.
