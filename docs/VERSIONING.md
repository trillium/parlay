# Parlay versioning — automatic semver tagging (task-prk)

> Makes the semver scheme Trillium defined (2026-07-17) **tooling-enforced**
> instead of manual-discipline: every feature-scope merge auto-creates the correct
> semver git tag. Deliverable of task-prk.

## The two version axes (audit finding + decision)

Parlay carries **two intentionally-separate version numbers**. They are NOT
expected to match — they measure different things — and the old assumption that
they should track each other is the source of the "divergence" confusion.

| Axis | Where | What it measures | How it moves |
|---|---|---|---|
| **Repo release** (`vX.Y.Z` git tag) | git tags | the whole repository's released version | **auto-tagged on merge** by `gate-tag` (this doc) |
| **Panel build** (`PA_VERSION`) | `packages/client/src/version.ts` | the panel bundle the captain is looking at | auto-bumped **per client-src commit** by `tools/bump-version.ts` (pre-commit) |

**Decision — keep two, canonicalize each (no forced unification).** The git tag is
the single source of truth for the *repo release version*; `PA_VERSION` is the
single source of truth for the *panel build*. They diverge by design: the panel
bumps on every client change (many per release), the repo tag bumps once per merge.
Forcing them equal would either throw away the panel's per-commit granularity or
stop the repo tag from reflecting non-client work. If a unified number is ever
wanted, derive `PA_VERSION` *from* the tag — do not hand-sync them. Until then,
read the tag for "which release" and `PA_VERSION` for "which panel bundle."

## The bump rules (Trillium's semver, 2026-07-17)

Inferred from the merged commits' conventional-commit prefixes + markers:

| Change | Bump | Example |
|---|---|---|
| `chore:` / `docs:` / `refactor:` / `style:` / plain word-change | **patch** `N.M.Z+1` | `chore(gitignore): …` |
| `feat:` / `fix:` | **minor** `N.M+1.0` | `feat(cli): add robots-tail` |
| structural / breaking: `feat!:` / `fix!:`, a `BREAKING CHANGE` footer, or a `[structural]`/`[major]` marker | **major** `N+1.0.0` | `feat!: drop legacy poll` |

A **feature-scope merge aggregates the HIGHEST bump across the whole merged range**,
so a 60-commit fast-forward tags **once** at the strongest bump it contains.

The rules live in `tools/gate-tag/semver.ts` (`classifyCommit` / `aggregateBump`),
which is pure and unit-tested — change the policy there.

## How it runs

`tools/gate-tag/gate-tag.ts` (wrapper: `bin/parlay-gate-tag`):

1. finds the last `v*` tag (newest by version, `for-each-ref` so `column.ui` can't
   corrupt it),
2. aggregates the highest bump across `lastTag..HEAD`,
3. computes the next version and creates an **annotated** tag whose message records
   the bump, the range, and the commit that drove it.

**Idempotent + safe:**
- HEAD already carries a `v*` tag → **no-op**.
- the computed tag already exists but *not* at HEAD → **refuses** (never forces).
- no commits since the last tag → nothing to do.

```sh
parlay-gate-tag                 # dry-run: print the bump + the tag it would create
parlay-gate-tag --apply         # create the annotated tag locally
parlay-gate-tag --apply --push  # create + push origin <tag>   (the gate's step)
parlay-gate-tag --from v4.12.0  # override the base tag
```

## Merge-gate integration (durable + automatic)

The parlay merge gate (parlay-dev, after a PR lands on `main` and `main` is pulled)
runs **one command** as its final step — no hand-tagging:

```sh
parlay-gate-tag --apply --push
```

Because it is idempotent, running it on every merge is safe; running it twice is a
no-op. This replaces the manual per-change tagging in the merge-gate policy.

### Optional: fully hook-driven

To make tagging fire automatically on any `main` advance without the gate
remembering, add this line to the **main checkout's** `tools/hooks/post-merge`
(after the bundle-delivery block; it is already guarded to the main checkout, and
pasted there it also inherits the delivery opt-in, so it only fires on a clone where
`git config --bool parlay.autobuild true` has been set):

```sh
# Auto-tag the semver bump for this merge (task-prk). Best-effort; never fails the merge.
bun "$(git rev-parse --show-toplevel)/tools/gate-tag/gate-tag.ts" --apply >> "$LOG" 2>&1 || true
```

(Left as a documented snippet rather than a committed hook edit to avoid colliding
with the delivery post-merge hook maintained on another branch. Pushing is kept out
of the hook — the gate does the `--push` — so a local merge never surprises origin
with a tag.)

## Reversibility

Entirely additive and reversible: `gate-tag` only *reads* git history and creates
tags. Delete a mistaken local tag with `git tag -d <tag>` (and
`git push origin :refs/tags/<tag>` if it was pushed). Nothing else is touched.
