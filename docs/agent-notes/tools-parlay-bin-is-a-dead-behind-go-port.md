# `tools/parlay-bin` is a dead, behind-schedule Go port — not a starting point you can assume works

`resolveSpawner()` (`tools/cli/internal/commands/launch.go`) prefers `parlay-bin`
over `parlay-spawn` on PATH, but `parlay-bin` is not installed anywhere in
practice — `parlay spawn` always falls through to `bin/parlay-spawn` (bash)
today. Don't read `resolveSpawner`'s preference order as evidence the Go path
is exercised in production.

`tools/parlay-bin` (its own Go module, `github.com/trillium/parlay/tools/parlay-bin`,
built+tested in CI's `GO_MODULES` list) originated in PR #23 (2026-08-03) as a
partial port of `bin/parlay-spawn`. It has not tracked bash's growth since:

- Implements: registration, worktree creation (git-toplevel only — no
  treehouse-lease path), env sourcing, herdr tab launch + rollback, identity
  registration, herdr-only watchdog, batch dispatch, ephemeral minting.
- Missing entirely: `--profile`/`--list` (profiles.toml + quota-axi
  headroom), `--pii`/`--no-pii` routing, `--bead`/beads-required gating,
  `--claim`, `--pane` in-place mode, `--workspace` resolution, config.toml
  defaults, and the `subprocess`/`gc` launcher backends as in-spawn branches
  (they exist only as separate top-level subcommands).
- Was missing the mandatory-model refusal gate (task-qyu8q, added to bash
  2026-08-31) until 2026-09-03 — the gate landed in bash four weeks after
  this port's last real feature work and was never backported. Fixed in
  `tools/parlay-bin/spawn.go`'s `requireModel`.
- Herdr tab/pane **creation** has zero prior Go code anywhere in the repo —
  `commands/herdr.go` only covers teardown/close. That organ is a
  from-scratch write against any future full port, not a port of existing
  code.

`docs/scope-go-spawn.md`, cited repeatedly in this package's own source
comments (e.g. `spawnpipeline.go`'s `launchScript` doc, `main.go`'s header),
**does not exist in the repo** (confirmed via `git log --all
--diff-filter=A`) — same pattern as `docs/scope-go-server.md` and
`docs/scope-go-cli.md` noted elsewhere in this file. Anything citing it is
describing an analysis that was never written down; treat those citations as
aspirational, not a spec you can go read.

Before doing further work here: re-scope as a reconciliation between two
divergent implementations (most of bash's feature area has zero existing Go
code to build on), not a fresh single-pass port. See task-04g1's 2026-09-03
comment for the full inventory this note summarizes.
