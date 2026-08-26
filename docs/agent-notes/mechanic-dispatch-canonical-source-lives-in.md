# `mechanic-dispatch` canonical source lives in `tools/mechanic-dispatch/`

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


The robots-ticket dispatcher — `robots create` → `~/data/robots/events.jsonl`
→ `parlay robots-tail` (`tools/cli/internal/robotswatch/`) →
`mechanic-dispatch <id>` → `parlay-spawn` — was for a long time an
install-only artifact at `~/.local/bin/mechanic-dispatch` with **no in-repo
source**. Its canonical source is now `tools/mechanic-dispatch/mechanic-dispatch`,
installed via `tools/mechanic-dispatch/install.sh` (backup-once, `--status`/
`--uninstall`, mirrors `tools/robots-emit/`). Edit the repo file, then re-run
the installer; never hand-edit the `~/.local/bin` copy.

Mechanics run in an **isolated git worktree**, never a repo's primary checkout:
`mechanic-dispatch` passes `--worktree` to `parlay-spawn` whenever the resolved
zone `--cwd` is inside a git repo (`git -C <cwd> rev-parse --show-toplevel`),
so a future git-repo zone in `zone_entry()` is isolated automatically.
`parlay-spawn` resolves `--worktree` against `--cwd` (creating
`<repo>/.worktrees/parlay-<id>`), so the two compose with no extra plumbing.
The `default`/`~` zone is deliberately left non-isolated (triage-only — `$HOME`
is not a repo). Bash 3.2 portable; behavior otherwise unchanged (bad-id guard,
closed-ticket skip, liveness re-dispatch). Test:
`tools/mechanic-dispatch/mechanic-dispatch.test.sh`. Phase-1 (isolation) only —
firstmate state-meta bridging and worktree teardown/landing are follow-ups.

Every launch also **names its bead**: `parlay-spawn` runs in beads-required
mode, where a spawn with no `--bead` is refused with **exit 2** before anything
registers. `mechanic-dispatch` was passing `--claim <ticket>` but no `--bead`,
so the whole dead-mechanic branch launched *nothing* — and because the
`robots-tail`/`robots-watch` daemon converges on that same branch, every
auto-dispatch for a newly filed ticket failed the same way and silently
(robots-aswz). The live-mechanic branch (`parlay send`) was unaffected, which is
why the fleet looked healthy. `--bead` now carries the store-qualified id
(`robots-<x>`), read back off the `robots show` status line so a bare
`mechanic-dispatch <x>` — legal, since the store resolves bare ids — is
qualified before it reaches `parlay-spawn`, which derives the store from the
id's leading token.
