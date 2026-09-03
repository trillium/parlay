# `tools/parlay-bin` is a dead, behind-schedule Go port — not a starting point you can assume works

`resolveSpawner()` (`tools/cli/internal/commands/launch.go`) prefers `parlay-bin`
over `parlay-spawn` on PATH, but `parlay-bin` is not installed anywhere in
practice — `parlay spawn` always falls through to `bin/parlay-spawn` (bash)
today. Don't read `resolveSpawner`'s preference order as evidence the Go path
is exercised in production.

`tools/parlay-bin` (its own Go module, `github.com/trillium/parlay/tools/parlay-bin`,
built+tested in CI's `GO_MODULES` list) originated in PR #23 (2026-08-03) as a
partial port of `bin/parlay-spawn`. It has not tracked bash's growth since —
the full organ-by-organ implemented/missing/divergent inventory, re-verified
against current code, now lives in [`docs/scope-go-spawn.md`](../scope-go-spawn.md)
§2; don't duplicate that table here, it will drift.

Two corrections to earlier reports, both settled by re-reading the code
directly rather than trusting a prior summary:

- Worktree creation is **not** "git-toplevel only" — `worktree.go`'s
  `setupWorktree()` implements full treehouse-lease support with a
  git-fallback path. An earlier PR body claimed otherwise; the code disagrees
  and wins. See `docs/scope-go-spawn.md` §2's worktree row and §8's
  correction note.
- The mandatory-model refusal gate (task-qyu8q) landed in bash 2026-08-31 and
  was backported to `tools/parlay-bin/spawn.go`'s `requireModel` on
  2026-09-03 (PR #238) — no longer a gap.

Before doing further work here: re-scope as a reconciliation between two
divergent implementations (most of bash's feature area has zero existing Go
code to build on), not a fresh single-pass port. See `docs/scope-go-spawn.md`
§7 for the staged reconciliation plan and task-04g1's 2026-09-03 comment for
the gap-inventory this note and that doc both derive from.
