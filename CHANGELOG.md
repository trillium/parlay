# Changelog

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
