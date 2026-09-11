# Fleet vs core split (parlay / parlay-dev)

**Rule:** the parlay **core product** (`packages/*`, `tools/cli`, `bin/parlay`, `tools/monitor`, `tools/mechanic-dispatch`) lives in the repo's main tree. The **fleet layer** — inbox tooling, the pi-inbox bridge, and the agent skills — is personal agent-adjacent glue that does NOT belong in `tools/` or `skills/` of the core repo.

## Why

Commits `952c346e` (`feat(inbox): add inbox create-emit wrapper and pi-inbox bridge`) and `957634b2` (`feat(inbox): add inbox-dispatch command and robotswatch handler`) originally placed fleet glue in `tools/inbox-*` and `skills/`. Those were:

- env-specific (paths like `/Users/trilliumsmith/data/inbox/.beads`, `~/.pi/agent/...`),
- personal (Talon voice-command consulting, the captain's ± 1 fleet),
- and duplicated the "installed artifact" pattern that other tools (robots-emit, mechanic-dispatch) already handle via backup-then-copy `install.sh`.

Shipping them in `main`'s product tree made the core repo look like it owns Trillium's personal fleet.

## The split

**Source** lives under `examples/fleet/` (authored source, Gas City authored-vs-live doctrine — c.f. `city/`):

```
examples/fleet/
├── inbox-dispatch/        command + install.sh + test
├── inbox-emit/            `inbox` wrapper + install.sh + test
├── pi-inbox-bridge/       Pi extension + install.sh
├── skills/
│   ├── inbox-handler/
│   ├── parlay-spawn/
│   └── voice-command-consulting/
├── skills-lock.json
└── install.sh             umbrella installer
```

**Live** installation targets (via `examples/fleet/install.sh`):

| Thing | Home | How |
|---|---|---|
| `inbox`, `inbox-dispatch` | `~/.local/bin/` | backup-then-copy via each tool's `install.sh` |
| pi-inbox bridge | `~/.pi/agent/extensions/parlay-pi-inbox/` | copy (isolated dir, so `./src` imports resolve) |
| fleet skills | `~/.claude/skills/<name>` | symlink to `examples/fleet/skills/<name>` (edits live-immediately) |

## `parlay` vs `parlay-dev`

`bin/parlay` is ONE script, reachable as two names (`~/.local/bin/parlay`, `~/.local/bin/parlay-dev` — both symlinks to `bin/parlay`; `bin/parlay-dev` is a repo-tracked symlink `bin/parlay-dev -> parlay`).

The script captures its invoked basename (`INVOKED=basename "$0"`) BEFORE resolving symlinks, then:

- `parlay` → production/fleet mode, no env overrides, state = `~/.parlay` (the live fleet).
- `parlay-dev` → `export PARLAY_STATE_HOME="${PARLAY_STATE_HOME:-$HOME/.parlay-dev}"` — isolated state so dev/test scraping never touches the live fleet (`robots-earr`: a dev scrape clobbered fleet registration). The `:-` default-preserving form respects a caller's pre-set `PARLAY_STATE_HOME`.

**Symlink target gotcha:** `bin/parlay-dev -> parlay` (relative, pointing at the sister file inside `bin/`), NOT `bin/parlay-dev -> bin/parlay`. The latter resolves through the outer `~/.local/bin/parlay-dev` symlink to `parlay/bin/bin/parlay` and breaks exec.

**Move-broke-a-relative-symlink gotcha:** `examples/fleet/skills/show-me` was `git mv`'d as `skills/show-me`, and its relative target `../.agents/skills/show-me` (correct at depth 1) silently broke at depth 3 — relative symlink targets are resolved from the LINK's directory, not the original. After any `git mv` of a symlink, `[ -e target ]` to confirm, and re-point with the new depth (`../../../.agents/skills/show-me`).

## What installs when

- `examples/fleet/install.sh` — umbrella: all fleet commands + skills. Idempotent, reversible (`--uninstall` restores backups and removes skill symlinks), `--status` reports.
- Each component keeps its own `install.sh` (backup-then-restore, matching `tools/mechanic-dispatch`), so you can install just the dispatcher or just the bridge.

CI runs `examples/fleet/inbox-dispatch/inbox-dispatch.test.sh` (path in `.github/workflows/ci.yml`), so moved tests keep running from the new home.

## Testing in sandbox / CI

- Shell harnesses must self-isolate; the fleet's `install.sh` writes to `$HOME` — in a sandbox set `HOME` first, or invoke individual `install.sh --status`.
- `parlay-dev` isolation is testable by setting `PARLAY_STATE_HOME` to a temp dir and checking `parlay-dev remote` reads `default` (no persisted fleet url) while `parlay remote` reads the fleet's recorded server.