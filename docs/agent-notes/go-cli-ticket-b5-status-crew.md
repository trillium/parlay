# Go CLI ticket B5: `status`/`crew-state`/`supervise`/`unattended-queue`/`context-check`

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Ticket B5 (`status` verb, `crew-state`, `supervise`, `unattended-queue`,
`context-check`) fixed one confirmed TS bug during the port —
`crewStateForAgent(agentId)` (`commands-crew-state.ts`) resolved its status
file via the caller's own `PARLAY_AGENT_ID`/`PARLAY_STATUS_FILE` instead of
the passed `agentId`; the Go port (`internal/commands/status_verb.go`'s
`statusFileForAgent`) resolves the target agent's file directly. Two sibling
defects were found but deliberately left bug-for-bug faithful in B5 (only the
crew-state fix was in scope there); a same-branch follow-up ticket then
extended the identical fix to both, each pinned by a regression test:
- `commands-supervise.ts`'s `cmdSupervise` had the identical
  caller-identity-instead-of-argument bug — only mattered when supervise was
  invoked by something other than the target agent itself. Fixed in
  `internal/commands/supervise.go`'s `Supervise` by resolving the status file
  via `statusFileForAgent(agentID)` instead of `statusSink()`.
- The shared status-line regex
  (`/^(\w+)(?:\s*\[key=...\])?\s*:\s*(.*)$/`, ported to
  `statusLineRe` in `internal/commands/crew_state.go`) used `\w+` for the
  verb, which couldn't match hyphenated verbs (`needs-decision`,
  `captain-held`) despite both being in the code's own declared verb
  vocabulary — such lines silently failed to parse and read back as
  "unknown / no status recorded" in both crew-state and supervise. Fixed by
  widening the verb class to `[\w-]+`.

`tools/cli/internal/commands/{guard,teardown,variant}.go` (ticket B4) port
`commands-guard.ts`/`commands-teardown.ts`/`commands-variant.ts` verbatim,
including three TS-source quirks worth knowing before touching this code:
(1) all three TS files hardcode `AGENTS_DIR`/`WKTREES_DIR` to
`homedir()/.parlay/{agents,worktrees}` and never honor `$PARLAY_AGENT_HOME`
or `$PARLAY_STATE_HOME` — unlike `internal/identity.AgentsRoot()` (honors
`$PARLAY_AGENT_HOME`) or `commands-guard.ts`'s own beacon path (honors
`$PARLAY_STATE_HOME`); the Go port preserves this split via non-env-aware
`parlayAgentsDir()`/`parlayWktreesDir()` helpers in `guard.go`, deliberately
distinct from `internal/identity`/`internal/config`'s env-aware equivalents.
(2) `cmdVariantTeardown`'s `try { await postJSON(...) } catch {}` around its
unregister call looks best-effort but isn't: `die()`'s `process.exit()`
is not a catchable JS exception, so an unreachable server there genuinely
aborts teardown before the final cleanup+success message — verified
empirically against the Go port. Contrast `commands-teardown.ts`'s raw
`fetch(...).catch(() => {})`, which IS genuinely best-effort (no status
check, network errors swallowed). The Go port matches both real behaviors:
`variant.go`'s teardown calls `httpc.PostJSON` unwrapped (dies loud, matching
reality over the misleading comment); `teardown.go` has its own
`bestEffortUnregister` that truly swallows every error.
(3) `commands-teardown.ts`/`commands-variant.ts` each define their own local
`parseFm`, distinct from `commands-identity/store.ts`'s `readFrontmatter`
(what `internal/identity.ReadFrontmatter` mirrors) — the local `parseFm`'s
per-line regex requires the whole `key: "value"` shape to match, silently
dropping a line whose value contains an embedded quote rather than keeping
it mangled. `guard.go`'s `readLocalFrontmatter`/`localFrontmatter` replicate
that local parity for `teardown.go`/`variant.go`'s frontmatter reads; see the
doc comment above `localFrontmatterBlockRe` in `guard.go` for the full
rationale. `identity.go`'s register/launch/rename/reap-ephemeral verbs are
unaffected — they keep using `internal/identity.ReadFrontmatter`, matching
their own TS source.
