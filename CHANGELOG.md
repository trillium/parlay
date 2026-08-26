# Changelog

## 2026-08-26 — session 8ca540c1

Files: tools/cli/internal/identity/mem.go

## 2026-08-20 — session 8ba730e4

Files: tools/cli/internal/identity/store.go, tools/cli/internal/identity/worklink.go, tools/cli/internal/identity/mem.go, tools/cli/internal/commands/launch.go, tools/parlay-bin/gascity_spawn.go, bin/parlay-spawn, tools/cli/internal/identity/bead_test.go
## 2026-08-20 — session 8ba730e4

Files: tools/cli/internal/identity/store.go, tools/cli/internal/identity/worklink.go, tools/cli/internal/identity/mem.go, tools/cli/internal/commands/launch.go, tools/parlay-bin/gascity_spawn.go, bin/parlay-spawn, tools/cli/internal/identity/bead_test.go
## 2026-08-20 — session 8ba730e4

Files: tools/cli/internal/identity/store.go, tools/cli/internal/identity/worklink.go, tools/cli/internal/identity/mem.go, tools/cli/internal/commands/launch.go, tools/parlay-bin/gascity_spawn.go, bin/parlay-spawn, tools/cli/internal/identity/bead_test.go
## 2026-08-20 — session 8ba730e4

Files: tools/cli/internal/identity/store.go, tools/cli/internal/identity/worklink.go, tools/cli/internal/identity/mem.go, tools/cli/internal/commands/launch.go, tools/parlay-bin/gascity_spawn.go, bin/parlay-spawn, tools/cli/internal/identity/bead_test.go
## 2026-08-20 — session 9d0a8427

Files: AGENTS.md, tools/cli/internal/help/help.go, tools/cli/internal/commands/merge_gate.go, tools/cli/internal/commands/claim.go, tools/cli/internal/commands/claim_test.go, packages/client/src/shell.html, packages/client/build.ts, tools/cli/internal/commands/merge_gate_test.go, packages/go-server/internal/static/static.go, packages/go-server/cmd/parlay-server/main.go
## 2026-08-20 — session f04e2b02

Files: skills/voice-command-consulting/SKILL.md, skills/voice-command-consulting/consulting-agent.md
## 2026-08-20 — session cf74f28d

Files: bin/parlay-spawn
## 2026-08-19 — session 889aed33

Files: bin/herdr-rpc, bin/parlay-spawn, bin/herdr-rpc.test.sh, VISION.md, VISION-answers.md
## 2026-08-18 — session 1eb8dc35

Files: bin/herdr-contract, bin/parlay-spawn, tools/cli/main.go, tools/cli/internal/commands/spawn.go, bin/herdr-rpc, tools/cli/internal/help/help.go, tools/cli/parity/run.sh
## 2026-08-17 — session d06df28f

Files: bin/parlay-spawn-triage
## 2026-08-10 — Externalize parlay launch/shutdown text to template files

**Refactor:** Launch and shutdown text moved from embedded strings in `bin/parlay-spawn` to external template files.

**Files added:**
- `launch-templates/default.txt` — default agent launch prompt with enrollment block, task, and status protocol
- `launch-templates/claim.txt` — agent launch prompt for `--claim` variant (shorter, defers task to ticket)
- `bin/parlay-spawn.templates.test.sh` — unit tests for template loading and variable interpolation
- `bin/parlay-spawn.integration.test.sh` — integration tests verifying templates load and interpolate in spawn context

**Files modified:**
- `bin/parlay-spawn` — refactored to use `load_template()` function; no behavior change, only text sourcing
  - Added `load_template()` helper that loads template files and interpolates `{{VAR_NAME}}` placeholders
  - Default and claim startup prompts now load from external files with proper variable substitution
  - Supports variables: `PARLAY`, `AGENT_ID`, `NAME`, `COLOR`, `MONITOR_CMD_JSON`, `SETUP_BLOCK`, `PROMPT`, `DOD`, `CLAIM`

**Tests:**
- All existing tests pass (quoting, batch dispatch, worktree)
- New template tests: verify files exist, contain required variables, load/interpolate correctly, handle missing files gracefully

**Benefits:**
- Dynamic template text is externalized, not embedded in code
- Easier to maintain and evolve launch prose separately from shell logic
- Template structure scales for future variants (e.g., team launch, debug mode)
- Variable interpolation tested independently of parlay-spawn's dispatch logic

## 2026-08-04 — Go CLI `claim` command and identity memory
## 2026-08-05 — robots-1186 (worktree guardrail names the symlink case)

Files: tools/cli/internal/commands/claim.go, tools/cli/internal/commands/claim_test.go

The mechanic DoD said "do all repo work in an isolated worktree". Followed literally over a symlinked subtree that is a silent no-op: `git worktree add` copies the symlink, not the tree behind it, so writes land in the shared checkout it points at. The agent believes it is isolated while dirtying a checkout other sessions hold — and a follow-up `git checkout -b` there strands them, the exact failure the guardrail exists to prevent.

- **claim.go**: added a guardrail clause naming the symlink case, the general remedy (worktree the repo that actually owns the files), the concrete instance (`~/.claude/hooks` work → worktree `~/code/pai-hooks`, not `~/.claude`), and a one-command precheck (`ls -ld <the-dir-you-will-edit>`).
- **claim_test.go**: asserts both the general warning and the concrete pointer survive future edits to the DoD.

Enforcement backstop ships separately in pai-hooks (`WorktreeDiscipline/symlink.ts`), which blocks such a write at the Edit/Write layer.


Files: tools/cli/internal/identity/mem.go, tools/cli/internal/commands/claim.go, tools/cli/internal/commands/claim_test.go
## 2026-08-04 — herdr contract gate for spawn and pipeline selftest

Files: bin/herdr-contract, bin/parlay-spawn, bin/pipeline-selftest, .changeset/herdr-contract-gate.md
## 2026-08-04 — `parlay-spawn` and `pipeline-selftest` (1)

Files: bin/parlay-spawn, bin/pipeline-selftest
## 2026-08-04 — `parlay-spawn` and `pipeline-selftest` (2)

Files: bin/parlay-spawn, bin/pipeline-selftest
## 2026-08-04 — `parlay-spawn` and `pipeline-selftest` (3)

Files: bin/parlay-spawn, bin/pipeline-selftest
## 2026-08-04 — Handoff resolution in the Go and TypeScript CLIs

Files: tools/cli/internal/resolvehandoff/resolvehandoff.go, packages/cli/src/resolve-handoff.ts, tools/cli/internal/resolvehandoff/resolvehandoff_test.go, packages/cli/src/resolve-handoff.test.ts, .changeset/handoff-guard-agent-scoped-authoritative.md
## 2026-07-22 — CLI verbs and events documentation

Files: docs/CLI_VERBS_AND_EVENTS.md
## 2026-07-22 — TypeScript CLI `robots-watch`

Files: packages/cli/src/commands-robots-watch/index.ts
