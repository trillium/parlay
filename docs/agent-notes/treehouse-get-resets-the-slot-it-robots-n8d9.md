# `treehouse get` RESETS the slot it hands out — guard the pool first (robots-n8d9)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`treehouse get --lease` does not just *pick* a free slot, it **checks
origin/main out over whatever branch that slot held**, at acquire time. Its
eligibility rules are dirty / attributable-processes / already-leased and
nothing else — so a clean slot holding a live agent's work looks free, and one
spawn detached a running agent's branch out from under it. Detecting this
afterwards is useless; the checkout has already happened.

`bin/parlay-treehouse-guard` is the prevention, called by both spawn paths
immediately before `treehouse get` (`bin/parlay-spawn`, and
`guardTreehousePool` in `tools/parlay-bin/worktree.go`). It writes a
protective lease — `lease_holder: "parlay-guard:<reason>"` — into the pool's
`treehouse-state.json` for every slot that is still occupied, which treehouse
does honor (verified against the real binary: it takes another slot, or
creates a new one, rather than reclaiming a protected one). Three reasons:
`dirty`, `unlanded` (commits no remote has), and `live-agent` (some
`state/*.meta` under **any** firstmate home on the box records that path as
`worktree=` and `fm_backend_agent_alive` does not say `dead` — scanning only
the spawner's own home is the blind spot that caused the bug). Guard leases
are released on the next sweep once their reason lapses; another holder's
lease is never touched. It is best effort by design — a missing or failing
guard warns and lets the spawn proceed.

Two sharp edges for anything else that writes that state file:

- **`leased_at` must be strict RFC3339** — treehouse parses it with Go's
  `time.RFC3339`, which rejects `date +%z`'s `-0700`. A malformed stamp makes
  treehouse declare the whole file corrupt and "recover" by marking **every**
  slot leased, taking the entire pool out of service. Use
  `date -u +%Y-%m-%dT%H:%M:%SZ`.
- **Never protect on a signal the repo cannot answer.** The `unlanded` check
  selects its comparison scope (`--remotes=origin`, then any remote, else skip)
  — without that, a remote-less checkout reads as ahead on every slot and the
  guard permanently starves the pool.

Coverage: `bin/parlay-treehouse-guard.test.sh` (real repos, real origin, real
state file — every assertion reads what the guard actually wrote) and
`TestSetupWorktreeGuardsPoolBeforeLeasing` in
`tools/parlay-bin/worktree_test.go` (pins guard-before-`get` ordering and the
guard's cwd).
