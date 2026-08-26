# Go CLI ticket B10: coverage/parity close-out — `bin/parlay` now execs the Go binary

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


B10 was workstream B's closing ticket, with three parts: close Go test-
coverage gaps against `packages/cli/src/*.test.ts`, build a TS-vs-Go parity
harness and fix whatever it finds, and wire `bin/parlay` to the Go binary.

`bin/parlay` (the one shared entry point every crewmate and the captain use)
now builds and execs `tools/cli`'s Go binary instead of `bun
packages/cli/src/index.ts` — see the script itself for the symlink-safe
`$REPO` resolution and the build-if-stale check (`find tools/cli -name
'*.go' -newer "$GO_BIN"`). One exception: `parlay lavish-import` still
routes to the TS CLI — `packages/cli/src/index.ts` has a real `case
"lavish-import"` (`cmdLavishImport`, in `lavish-import.ts`) and the Go help
text in `internal/help/help.go` already documents the subcommand, but
`tools/cli/main.go`'s dispatch switch never grew a matching `case` in any
prior ticket. `bin/parlay` special-cases that one verb back to `bun
packages/cli/src/index.ts` rather than silently 404ing; porting
`lavish-import.ts` to Go is open follow-up work, not done in B10.

An original TS-vs-Go parity harness (no `packages/go-server` C5 harness
existed yet to base this on) lives at `tools/cli/parity/run.sh`: it builds a
disposable `packages/go-server` fixture instance plus the Go CLI, redirects
`$HOME` to a scratch dir for both CLIs (which also safely scopes the
hardcoded, non-`$PARLAY_STATE_HOME`-aware `~/.parlay/agents|worktrees` paths
used by guard/teardown/variant/launch — see the B4/B9 notes above), runs the
full representative command surface through both `bun
packages/cli/src/index.ts` and the built Go binary, and diffs normalized
stdout/stderr/exit code. Run it with `tools/cli/parity/run.sh [-v]`; on any
FAIL it copies the raw diffs to `tools/cli/parity/last-diffs.log`
(gitignored scratch output, not the source of truth — re-run the harness for
current results). Three real Go-side bugs were caught and fixed this way:

- `internal/httpc`'s `GetJSON`/`PostJSON` die-message duplicated the HTTP
  status code (Go's `resp.Status` already contains it, unlike TS's
  `res.status`/`res.statusText` pair printed separately).
- `main.go`'s `cmdHelp` had grown a `parlay help <cmd>` per-subcommand
  lookup with no TS equivalent (TS's `case "help"` always prints the full
  `USAGE` regardless of trailing args) and was reverted to match.
- `internal/commands/doctor.go`'s presence-status fallback printed `presence:
  ` (empty) instead of `presence: unknown`: `packages/go-server`'s
  `/api/chat/subscribers` never sends a `status` key on presence entries at
  all (see `internal/handlers/registry.go`'s `subscribersPresenceEntry` —
  only `channel`/`lastSeen`), so Go's `pres.Status` zero-values to `""`
  where TS's `pres?.status ?? "unknown"` sees a missing property and falls
  back correctly. Fixed by also checking `pres.Status != ""` before using it;
  regression test: `TestDoctorReportsUnknownWhenPresenceEntryHasNoStatus`.

**A Go-only verb is not free just because it has no `check` case
(robots-xaxt).** `parlay help` prints the whole usage block, so every verb the
Go CLI documents and TS does not shows up as a diff on all four `help` cases —
`claim`, `merge-gate`, `branch-audit` and `sweep` between them turned a CLI
with no defect into a 4-FAIL harness. `run.sh`'s `GO_ONLY_VERBS` array is the
registry: its usage lines are filtered out of the **Go** side of the diff only,
so a verb that merely got forgotten on the TS side still fails normally.
`audit_go_only_verbs` keeps the list from rotting into a blanket mute — per
verb it asserts the line is still in Go's usage (else the entry is stale) and
still absent from TS's (else the verb gained a TS side and belongs in the
ordinary check list), reporting each as its own `GO-ONLY`/`FAIL` summary row.
**Adding a Go-only verb means adding it here too**, in the same change that
adds the verb.

Any new harness case must first check whether the command under test honors
`PARLAY_AGENT_HOME`/`PARLAY_STATE_HOME`, or — like `commands-status.ts`'s
`statusSink()` and its Go port — hardcodes `homedir()/.parlay/agents/<id>`
and only respects the narrower `PARLAY_STATUS_FILE` override; getting this
wrong writes real files under the live `~/.parlay` rather than the harness's
temp dirs (a `~/.parlay/agents/status-agent/` pollution incident happened
once during B10 itself, before the `$HOME`-redirection approach settled, and
had to be manually cleaned up — confirmed gone as of this note).
