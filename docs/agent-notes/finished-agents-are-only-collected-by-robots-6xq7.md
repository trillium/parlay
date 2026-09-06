# Finished agents are only collected by `parlay sweep` — firstmate can never see them (robots-6xq7)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Parlay-spawned agents are **structurally invisible** to firstmate's idle>2h
auto-close: every firstmate shutdown path enumerates sessions via
`$STATE/*.meta`, and a parlay agent has no `.meta` file. Nothing else closed
them either — `crew-state` reports a terminal state, `teardown` does the safe
destroy, `supervise` is per-agent wake-on-status — so finished agents posted
`done` and waited forever (38 stale panes against 2 live orchestrators).

`parlay sweep [--apply] [--agent <id>] [--all] [--force] [--interval <sec>]
[--verbose]` (`tools/cli/internal/commands/sweep.go`) is the missing
collector: it walks every store under `~/.parlay/agents`, asks `crew-state`
for each one, and tears down the provably-finished through
`teardownAgent` — the same chain `parlay teardown` uses, factored out of
`Teardown` so a refusal (uncommitted work, unlanded commits) surfaces as an
error the sweep reports and steps over instead of an `os.Exit`. Default is a
dry run; `--apply` acts. Policy lives in the pure `ClassifySweep`, tested
with no filesystem in `sweep_test.go`. **Go-only, no TS port** — same
reasoning as `merge-gate` above; no `check` case in
`tools/cli/parity/run.sh`, but it is in that script's `GO_ONLY_VERBS`.

**Teardown closes the herdr surface too, and that is the whole point
(robots-iz9o).** The first version of the sweep unregistered, removed the
worktree and deleted the store — and left the pane running. It printed
`closed` for a fleet that was entirely still alive, and 57 panes had to be
walked by hand with `herdr tab close` afterwards. `teardownAgent` now ends by
calling `closeHerdrSurface` (`tools/cli/internal/commands/herdr.go`), so both
`sweep --apply` and a direct `parlay teardown` reclaim the terminal.

The lookup key on both sides is the parlay agent id, because the spawn
pipeline uses `herdr agent start <id>` and `herdr tab create --label <id>`. Both
lookups are needed: a live agent resolves through `herdr agent get`, while an
agent whose process already exited has no herdr agent at all and is findable
only by its lingering labelled tab — that residue is what fills `herdr tab
list` with dead `mc-*` tabs. Two rules hold the blast radius: a tab reporting
`pane_count > 1` is shared, so only the agent's own pane is closed (`herdr tab
close` would take the bystanders with it), and the *calling* agent's surface is
never closed, so `parlay teardown $SELF` cannot kill the pane mid-command. All
of it is best-effort like `bestEffortUnregister` — no herdr on PATH, no daemon
or an unparseable reply must never block the git safety checks or the store
delete. Note `herdr` exits 0 even when it prints an `error` object, so the
reply body is the only usable signal.

Four things are never swept, and each guard exists because of a real way this
could destroy work: the sweeping agent itself; ids listed in
`$PARLAY_STATE_HOME/sweep-keep` (one id per line, `#` comments — where
long-lived dispatchers go); `needs-decision`/`blocked`/`failed`, which are
*held for the captain* because absorbing them destroys the state he needs to
read; and any agent whose `identity.md` has **no frontmatter**, held even
under `--all` (`--force` is the deliberate override).

That last guard is the important one. `--worktree`/`--project` had been
dropped from `MemValueFlags` and from `--register`'s meta-field loop during
the Go port, and `args.Parse` dies with `EXIT_USAGE` on an unknown flag
(`args.go:89`) — so
every `parlay identity --register … --worktree <path> --project <path>` that
`parlay-spawn` issues for a worktree agent exited 2 and wrote no frontmatter
at all, with `registerIdentity`'s `_ = cmd.Run()` swallowing the code. The
agent launched looking fine with an empty launch spec, and `parlay teardown`
then read no worktree, deleted the store, and orphaned the worktree plus any
unpushed commits **without ever reaching its git checks** — teardown only
checks a *recorded* worktree. Both flags are restored and pinned by
`TestRegisterRecordsWorktreeAndProject`, but stores registered before that
fix are still empty on disk, which is exactly what the hold protects. When
adding a flag to a Go-ported command, diff its table against the TS source's
(`packages/cli/src/commands-identity/store.ts` here); a dropped flag is not a
degraded flag, it is a hard exit that callers may be discarding.

Two follow-ons to that (robots-jusi). **When you add a lifecycle field to the
launch spec, add it in three places** — the flag table, the `--register` field
loop in `mem.go`, and whatever reads it back. And the reason the fatal exit
went unseen for so long: `bin/parlay-spawn` ended its registration with
`>/dev/null 2>&1 || true`; it now prints a named warning on failure instead
(still non-fatal — a launch spec isn't worth aborting a live spawn over).

Note `teardown` resolves `~/.parlay/agents` from `HOME` and ignores
`PARLAY_AGENT_HOME` (matching `commands-teardown.ts:23`); `identity` honors
`PARLAY_AGENT_HOME`. Set `HOME` when testing teardown end-to-end.
