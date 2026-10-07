# Never tell a reader to run `handoff create` / `handoff show` unconditionally

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->

`handoff` is a beads-store wrapper from the author's federation — the same
family as `task`, `inbox`, `robots` — pinned to the `handoff-` store and
`exec bd "$@"`. **It is not a command this repo installs and not a `parlay`
verb** (`parlay handoff` does not exist). On any plain clone, a string that
says "run `handoff show <id>`" is an instruction the reader cannot follow.

This cost four passes to fully close, because the population kept hiding:

| Surface | Why it is the worst kind of place to get this wrong |
|---|---|
| `identity --submit/--park/--handoff/--dismiss-handoff` refusal | A diagnostic read once, by whoever is confused. Recoverable. |
| `parlay drawdown` closing recipe, `context-check` ROTATE line, `doctor` notes | Same — read once, skippable. |
| `internal/identity/lifecycle.go`'s respawn charter | Read by a **live agent at the exact moment it has least capacity to discover a command does not exist.** The state it wants IS the handoff body, so a missing command reads as amnesia — and an agent that believes it lost its state goes looking for work to redo instead of resuming. |
| `parlay claim`'s no-work exit procedure | Its step 2 took the id *from* its step 1, so on a plain clone the whole procedure dead-ended at step 1. That brief exists solely to stop an agent lingering on a pane with no work; dead-ending it defeats the only purpose it has. |

**The rule:** any RUNTIME string that names a `handoff` subcommand must branch
on `resolvehandoff.StoreAvailable("")` and name the portable substitute when
the store is absent. That substitute is always the same pair —
`parlay drawdown` (drafts a handoff body from chat history, needs no store)
and `parlay identity --submit <any-handoff-id>` (pins any id verbatim and
resets; the bead and the auto-resolved id are the only things missing).

Two things are deliberately NOT store-dependent:

- **The on-disk `> 📎 Handoff: <id> — run \`handoff show <id>\`` pointer.** It
  is a durable artifact read by the NEXT session on the SAME machine, so its
  bytes must not depend on which box wrote them — and three packages assert it
  byte-exactly. The interactive surfaces that *read* the pointer are the ones
  that branch.
- **Static help text** (`internal/help`). Help cannot probe PATH, so its only
  honest form is the recipe PLUS a caveat, enforced by
  `TestHelpNamingHandoffCreateAlsoCarriesTheNotInstalledCaveat`.

## The gates

- `internal/help/help_test.go` — help entries naming `handoff` must carry the
  "NOT something this repo installs" caveat.
- `tools/cli/handoff_prescription_test.go` — parses every non-test `.go` file
  under `internal/` and fails the build if any **declaration** puts a `handoff`
  subcommand in a string literal without referencing
  `resolvehandoff.StoreAvailable`.

Two details in that gate are load-bearing and easy to get wrong:

- **Declaration granularity, not file granularity.** The first version of this
  gate checked whether the *file* mentioned `StoreAvailable` anywhere — and it
  passed a deliberately injected `handoff create` in a new function of
  `claim.go`, because a different function in that same file had already been
  fixed. A file becomes silently exempt the moment one of its functions is
  corrected; the check has to be per `FuncDecl`/`GenDecl`.
- **The allowlist has a staleness test.** Entries are two: the frozen pointer
  format, static help, and `sayguard`'s warning (which can only fire when the
  store already answered, so `handoff show` is resolvable by construction — a
  conditional there would be dead code). An entry that stops matching anything
  is a silent blanket exemption, so `TestHandoffAllowlistHasNoStaleEntries`
  fails if one goes stale. It earned its place immediately: it caught a stale
  entry written five minutes earlier.

## Testing store-awareness without depending on the box

Any test that exercises a store-aware string must pin `PATH` to a directory
containing only a stub (`internal/commands/drawdown_test.go`'s
`pinHandoffStore`, and its twin in `internal/identity`), or it asserts
whichever branch the machine running the suite happens to have. Two claim
tests asserted `handoff create` / `handoff show handoff-abc` with no pin, so
they were silently testing `StoreAvailable() == true`.

Verified: with `handoff` removed from `PATH`, the whole `tools/cli` suite is
green except one PRE-EXISTING unrelated failure (`internal/robotswatch`'s
scratch-store test, which needs `bd` from the same directory).

## Related

- `internal/resolvehandoff`'s package comment for why `StoreAvailable` exists
  at all (`ResolveCurrentHandoff` collapses "nothing open" and "no store" into
  `""`).
