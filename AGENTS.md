# Project agent memory

This file is internal operating memory for AI agents working in this repository, not user documentation — it is written for whoever (or whatever) is editing the code next. See [`README.md`](README.md) if you are looking for how to run parlay.

**This file is an index, not the archive.** It is loaded into every agent session, so it is kept small on purpose: the non-negotiable rules are inline below, and everything else is one line plus a pointer into [`docs/agent-notes/`](docs/agent-notes/), which holds the full rationale. Read the pointer before touching the code it describes — the one-liner is a reminder, not the whole rule.

## Do not do these things

Each of these has already caused a real incident on the captain's box.

- **Never `pkill -f 'src/index.ts'`, `bun`, or `parlay`.** The production chat server runs as launchd job `com.parlay.chat-server` on that exact path. Kill test servers by port or pid. Same for `$TMPDIR/parlay/` — match the `srv-<hash>` subdirectory, never the parent. → [notes](docs/agent-notes/packages-server-is-a-standalone-bun.md)
- **Never target or use port `:31337`.** That is the captain's live Pulse instance.
- **Never run `bun run build` / `bun build.ts` in `packages/client`.** Its `build.ts` POSTs a reload beacon to `:31337` and force-reloads the captain's connected clients from any worktree. Use `bun test` or a scoped `bun build src/<file>.ts --outdir=<tmp>`.
- **Never edit `~/.claude` from this repo.** Reading it to diagnose is fine.
- **A `READY` gate is not evidence a review found nothing.** CodeRabbit posts findings it cannot attach to the diff as *"Outside diff range comments"* in the review BODY, where they create no thread — so `merge-gate`'s `unresolved-threads` check cannot see them and returns READY with a Major outstanding. Read the newest CodeRabbit review body, not just the thread list. (task-42qot PR B1: exactly this hid an in-place-rollback Major.)
- **Never merge on a green check alone.** Run `parlay merge-gate <pr>`. CodeRabbit reports conclusion `pass` when it never ran — and it **never auto-runs here**, because the repo has under 10 stars. Commenting `@coderabbitai review` is the **only** way to get gate-visible review evidence, so do that before falling back to merge-and-disclose. Run `coderabbit review --agent --committed --base origin/main` locally *before opening the PR* — it is an extra check, never a substitute, and it spends from the same 3-review pool the bot uses. → [notes](docs/agent-notes/never-merge-on-a-green-check-robots-jap6.md)
- **Never `git worktree remove --force` directly.** Route it through `checkWorktreeGitSafety`. → [notes](docs/agent-notes/every-path-that-removes-a-worktree-robots-cncx.md) Teardown now refuses on lease/liveness/borrow/freshness/git-state/stashes, and `--force` bypasses only the inspectable git half — never widen it. → [notes](docs/agent-notes/teardown-gates-liveness-lift.md)
- **Deploy scripts trash, never `rm`.** A `uninstall.sh --purge` once permanently deleted the live `~/.parlay`. → [notes](docs/agent-notes/go-server-ticket-c6-parlay-server.md)
- **Never let `gh` pick the repo implicitly.** It prefers an `upstream` remote over `origin`, so a bare `gh pr view N` reads someone else's PR. Always pass `--repo`.
- **Never plan to push directly to `origin/main` — it is impossible, not just discouraged.** Branch protection (the 4 required CI checks + `enforce_admins: true`) declines the push for every account including the owner; "protected branch hook declined" is that refusal, not a transient error. Work lands only via a PR whose checks passed. Never modify the protection itself — that is captain-only.

## Test and sandbox rules

- **`bun test` only works from inside a package directory** — there is no root `bunfig.toml`, so `document`/`window` tests fail at the root. Always `cd packages/X && bun test`. → [notes](docs/agent-notes/bun-test-only-works-from-inside.md)
- **A test instance needs four redirects, not one.** `PARLAY_DATA_DIR` covers only what goes through `paths.ts`. Also redirect `HOME` (guard/teardown/variant/launch/sweep hardcode `~/.parlay`), `PARLAY_STATE_HOME`, and `PAI_DIR` — the TTS and tailer paths write and delete under `$PAI_DIR`, and the tailers replay live agent turns into whatever hub answers `PARLAY_HUB_URL`. → [notes](docs/agent-notes/packages-server-is-a-standalone-bun.md)
- **Need a real instance? Use `examples/bootstrap-sandbox.sh`** rather than hand-rolling one; it encodes the isolation recipe. → [notes](docs/agent-notes/need-a-real-parlay-instance-to.md)
- **A best-effort probe written as `VAR=$(cmd)` is not best-effort.** Under `set -euo pipefail` a plain assignment takes the substitution's exit status. Write `VAR="$(cmd)" || VAR=""`. → [notes](docs/agent-notes/a-best-effort-probe-written-as-robots-dcag.md)
- **`Bun.spawn` without an explicit `env` snapshots env at Bun startup, and a signal-killed spawn does not reject its awaited promises** — always pass `env: process.env`, and track timeouts with your own flag, not the exit code. → [notes](docs/agent-notes/bun-spawn-env-and-abort-gotchas.md)
- **CI is `.github/workflows/ci.yml`** — four jobs (go, bun, shell, hygiene), including a 2 MiB tracked-blob ceiling. Several harnesses are deliberately excluded. → [notes](docs/agent-notes/ci-is-github-workflows-ci-yml.md)
- **Developer PATH shims leak into every bash harness subprocess** — a `sleep` guard that exits 1 turned `parlay-monitor.test.sh` red in 5 places that were all the shim aborting polling loops, not product defects. Shell harnesses must self-isolate: pin system dirs first on PATH, scrub ambient `BASH_ENV`/`PARLAY_*`/exported functions, and assert a clean subprocess before running. → [notes](docs/agent-notes/shell-harnesses-self-isolate-robots-buu8.md)
- **The bun job's `*.test.ts`/`*.test.tsx` coverage gate is repo-wide, not workspace-scoped.** It greps `git ls-files` for the whole tree and hard-fails on any match outside its `$roots` list — even a test file living entirely outside `packages/*` (e.g. under `examples/`). Name such a test file `.spec.ts` instead, or add its directory to `$roots`. → [notes](docs/agent-notes/bun-test-ts-gate-is-repo-wide.md)
- **A conflicted PR gets NO Actions runs — missing checks, not red ones.** pull_request runs build the merge ref; on conflict GitHub silently skips them while head-SHA apps (CodeRabbit, GitGuardian) keep reporting. Don't retrigger — resolve the conflict. → [notes](docs/agent-notes/a-conflicting-pr-gets-no-actions.md)
- **Never assert on elapsed time across a subprocess.** `bun` startup jitter is bigger than most quantities worth testing, so a bound loose enough not to flake cannot fail — assert on emitted output, and test-the-test. → [notes](docs/agent-notes/a-timing-assertion-loose-enough-not.md)
- **`tools/cli` sweeps need `CGO_ENABLED=0`** — a default-CGO `go build ./...`/`go test ./...` there dies on missing ICU C++ headers (the beads dependency's embedded-Dolt tree); `bin/parlay` (`go build .`) is unaffected. → [docs/status-lift-topology.md](docs/status-lift-topology.md)
- **A resettable `sync.Once` and a never-stopped test goroutine are both data-race magnets.** A test-only cache-reset hook needs a real mutex, not `once = sync.Once{}`; a `newHub`-style bridge goroutine outlives its test unless something calls a `Stop()` — wire it via `t.Cleanup`. → [notes](docs/agent-notes/linkrewrite-cache-race-hub-goroutine-leak.md)
- **`make test-bdd` runs the Gherkin/Cucumber scaffold** (`features/`); there is no `packages/eval-engine` — the real matcher/platform code is `tools/cli/internal/evalengine/`. → [notes](docs/agent-notes/bdd-scaffold-and-eval-engine-path.md)
- **On modern macOS, `ps eww` hides env vars for sealed platform binaries** (`/bin/sleep`, `/bin/zsh`) even as root — a real fixture for testing an env-matching process scanner needs a self-re-exec'd test binary, not `/bin/sleep &`. → [notes](docs/agent-notes/macos-ps-env-hides-platform-binaries.md)

## The security boundary

The chat API has **no authentication**. A route is guarded by what its handler DOES, regardless of HTTP method — `GET /subscribers` is guarded because it hands out identifiers, `GET /poll` because it registers a channel.

- **Adding a mutating or identifier-aiming `/api/chat` route? Add it to `GUARDED_CHAT_PATHS`** (`packages/server/src/guard/paths.ts`) and to `internal/guard.GuardedPaths` on the Go side. A new route is unguarded until you do. Both sides also guard whole subtrees (`GUARDED_PREFIXES` / `guardedPrefixes`) — `/api/chat/plugin/`, `/api/chat/agents/`, `/api/debug/` — so anything added under those is guarded before you get there. `JSON_EXEMPT_PATHS` is a closed three-member list — do not grow it one bug report at a time. → [notes](docs/agent-notes/packages-server-is-a-standalone-bun.md)
- **`POST /api/chat/events` is the out-of-process ingress seam**, with a one-name-per-real-producer allowlist (`tool_event` today). Do not widen it to the panel-aiming events. → [notes](docs/agent-notes/out-of-process-producers-reach-the.md)
- **The live-command registry stores no free-form text** — verb, agent id, pid, flag *names*, outcome token. Never argv, values, paths, or error strings. → [notes](docs/agent-notes/the-live-command-registry-sees-only.md)

## Verbs that exist because a naive command lies

Reach for these instead of the hand-rolled equivalent.

| Instead of | Run | Why |
|---|---|---|
| trusting a green check | `parlay merge-gate <pr>` | exit 0 ready / 3 code / 4 needs-decision / 5 pending / 6 infra; precedence **code > pending > infra > reviewer-unavailable** → [notes](docs/agent-notes/never-merge-on-a-green-check-robots-jap6.md) |
| `git diff origin/main <branch>` | `parlay branch-audit` | two-dot diff reports a *behind* branch as having deleted merged work → [notes](docs/agent-notes/git-diff-origin-main-branch-is-robots-d988.md) |
| `gh pr view <n>` as landed-proof | `parlay landed <pr>` | gh resolves bare numbers against `upstream`; also never parse `git branch` output → [notes](docs/agent-notes/never-prove-a-fix-landed-with-robots-0a77.md) |
| re-tasking a registered pane | `parlay stale <agent-id>` | a finished pane still accepts messages and does the new work on the old transcript → [notes](docs/agent-notes/a-finished-pane-still-accepts-messages-robots-9d2w.md) |
| leaving finished agents running | `parlay sweep [--apply]` | firstmate structurally cannot see parlay agents; four hold-guards, each from a real incident → [notes](docs/agent-notes/finished-agents-are-only-collected-by-robots-6xq7.md) |
| killing the auto-spawner | `parlay mechanic off` | sentinel file, not launchd; no backlog replay on re-enable → [notes](docs/agent-notes/parlay-mechanic-on-off-status-is.md) |
| leaving a retiring agent's listener/spool/registry to time out | `parlay shutdown <id>` | idempotent teardown: kills the local listener, unregisters + tombstones server-side, resolves any parked long-poll immediately → [notes](docs/agent-notes/graceful-agent-shutdown-task-35ww.md) |

## Spawning, worktrees, and the relay

- **`treehouse get --lease` RESETS the slot it hands out** — run `bin/parlay-treehouse-guard` first, as both spawn paths do. `leased_at` must be strict RFC3339 or treehouse marks the whole pool leased. → [notes](docs/agent-notes/treehouse-get-resets-the-slot-it-robots-n8d9.md)
- **`treehouse` picks its pool from the process cwd** — always pin `cmd.Dir` / `(cd … && treehouse …)`. Repo identity is `git rev-parse --git-common-dir`, never `--show-toplevel`. → [notes](docs/agent-notes/treehouse-picks-its-pool-from-the-robots-d04t.md)
- **A spawned process outlives its spawner unless something ENDS it.** Own the child's death: watch your own `PPID`, background the pipeline so traps can fire, match whole command lines never `pgrep -f` regexes, and scope destructive sweeps. → [notes](docs/agent-notes/a-spawned-process-outlives-its-spawner-robots-3pvi.md)
- **A `stop()` that returns before its goroutine does is not a `stop()`** — and a reaped pid gets reissued, so a late callback kills someone else. Wait for the goroutine *and* re-check the stop signal after any blocking call. A `-race` failure is a report about production; CI runs `go test -race`. → [notes](docs/agent-notes/a-stop-that-returns-before-its.md)
- **Never make an agent's reply channel one process.** Respawn don't propagate; resume where delivery stopped; report on stdout. → [notes](docs/agent-notes/the-listen-stream-is-supervised-never-robots-gv6t.md)
- **A registration is not a listener, and a spool is not delivery.** Liveness = registry ∩ process table. → [notes](docs/agent-notes/a-registration-is-not-a-listener-robots-jkwc.md)
- **Arming a listener is a takeover, not an addition** — one live poll loop per channel, matched by `ps`, failing toward "not a duplicate". → [notes](docs/agent-notes/arming-a-listener-is-a-takeover-robots-fgyz.md)
- **The relay is a per-runtime-dir singleton bound to ONE server.** `PARLAY_SERVER` alone does not scope it. Unix socket paths cap at 104 bytes. → [notes](docs/agent-notes/the-relay-is-a-per-runtime-robots-buu8.md)
- **A 410 tombstones the spool, not just the in-memory loop.** `resumeFromSpools` re-registers every `*.chan` file on the next relay restart, so a retired agent's dead spool must be renamed out of that glob or it resurrects (and re-earns 410) every restart. → [notes](docs/agent-notes/relay-resume-tombstones-retired-spools-task-0n80i.md)
- **The canonical runtime dir is RESERVED** — a wrong-server relay in it is a fleet outage. Never let an ambient env var configure an installed singleton; a liveness probe is not a correctness probe. → [notes](docs/agent-notes/the-canonical-runtime-dir-is-reserved-robots-93xu.md)
- **"Not answering /health" ≠ "not running"** — never force-restart a relay. → [notes](docs/agent-notes/not-answering-health-not-running-never-robots-mpr3.md)
- **`mechanic-dispatch`'s canonical source is `tools/mechanic-dispatch/`**, not the `~/.local/bin` copy. Every launch must name its `--bead`. → [notes](docs/agent-notes/mechanic-dispatch-canonical-source-lives-in.md)
- **The `subprocess` launcher is a herdr-free escape hatch** in `tools/cli/internal/spawn`, selected by `--subprocess` / `PARLAY_SPAWN_LAUNCHER` / config (`gascity` is its accepted, deprecated pre-rename spelling). Its stdin charter delivery is an explicitly unverified assumption. → [notes](docs/agent-notes/subprocess-launcher-a-herdr-free-escape.md)
- **The agent startup-prompt template is single-source**: real files in `tools/cli/internal/spawn/launch-templates/`, repo-root `launch-templates/*` are symlinks to them, and `prompt.go` (`go:embed`) reads those bytes. Edit the real file, never the symlink. → [notes](docs/agent-notes/startup-prompt-template-is-single-source.md)

## Architecture pointers

- **`packages/server`** is a standalone Bun `serve()` app owning `/api/chat/*`. Live history is `~/exchange/chat-history.jsonl` — do not clobber. → [notes](docs/agent-notes/packages-server-is-a-standalone-bun.md)
- **`packages/go-server`** is the Go rewrite. Its spec is `docs/api-contract.md` — `docs/scope-go-server.md` has never existed. Always `git log --all` before concluding a cited doc is missing. → [notes](docs/agent-notes/go-rewrite-of-packages-server-use.md)
- **`tools/cli`** is the Go CLI `bin/parlay` execs — every verb, with no exceptions (the last gap, `spawn-account`, closed post-T-08; verb-by-verb table: `docs/go-cli-parity.md`). It cites `docs/scope-go-cli.md` and `docs/plan-go-migration-tickets.md`, neither of which exists. The TS source those docs told you to check against is gone too, deleted with `packages/cli` in T-08 — reach it with `git log --all -- packages/cli/src/<file>.ts`, and treat it as archaeology rather than a spec. → [notes](docs/agent-notes/go-cli-tools-cli-the-packages.md)
- **`tools/cli/internal/spawn` is the ONLY spawner, in-process behind `parlay spawn`** — `tools/parlay-bin`, `bin/parlay-spawn`, `PARLAY_SPAWN_IMPL`/`spawnImpl` and the `PARLAY_SPAWN_VIA_CLI` handshake are all deleted (task-42qot), so there is no second binary, no PATH resolution order, and no way around the model/beads gates. Anything execing a spawn runs `parlay spawn`. `docs/scope-go-spawn.md` is now a historical record of the reconciliation, not a live map. → [notes](docs/agent-notes/go-spawner-folded-into-tools-cli.md)
- **`herdr agent start` types its trailing argv after the KIND's canonical executable** — so `--kind` decides what launches, and the multi-line charter cannot ride there (herdr refuses to encode a newline): `agent prompt` delivers it, and a failed delivery rolls the tab back. The post-launch watchdog is a DETACHED `parlay spawn-watchdog` child, one arm per launcher — a goroutine cannot outlive `parlay spawn`. → [docs/launcher.md](docs/launcher.md)
- **`internal/httpc` has exactly one timeout-less client**, `UnboundedClient`, with exactly one legitimate caller (`monitor.pollOnce`). Do not widen it. → [notes](docs/agent-notes/internal-httpc-has-exactly-one-timeout-robots-gxlb.md)
- **A verb missing from `parlay commands`? Check `$PARLAY_STATE_HOME/command-report-unsupported` first** — commandreport caches a real 404 per-server for 1h, and its client's keep-alive-free transport is deliberate. → [notes](docs/agent-notes/commandreport-caches-a-404-on-disk.md)
- **Two-arg `git merge-tree` is not a predicate** — use `--write-tree` against `<ref>^{tree}`. A gate with no test is indistinguishable from a gate that has never run. → [notes](docs/agent-notes/two-arg-git-merge-tree-is-robots-ceon.md)
- **Publishable packages use flat `parlay-<part>` names; the `@parlay` scope is never published.** Only `packages/input` is public. → [notes](docs/agent-notes/publishable-packages-use-flat-unscoped-parlay.md)
- **Remote debug log + on-screen mobile console** for phone-only triage. → [notes](docs/agent-notes/remote-debug-log-on-screen-mobile.md)
- **`tools/cli/internal/staleness` is representation-plane RECORD staleness** (#128 §21–§24: Dagster version pairing — derived by comparison, never eagerly cascaded; reads carry nothing; budgeted passes) — a different concept from `parlay stale`/`sweep` agent-worktree staleness; never entangle them. → [docs/staleness-model.md](docs/staleness-model.md)
- **`tools/cli/internal/supersession` is the supersession policy** (#128 §13–§19: records superseded never mutated, SemVer bump validated against a classified changeset floor, severity mandates a reprocessing requirement; major = staleness source; superseding a captain-acted-on record is never silent). → [docs/supersession.md](docs/supersession.md)
- **Input surfaces enroll via source contracts**: canonical `contracts/sources/*.json`, validated by `tools/cli/internal/sourcecontract`; the go-server events-ingress allowlist is DERIVED from the embedded mirror (`packages/go-server/internal/sourcecontracts`) — enroll a contract, never hand-edit `ingressEvents`. → [docs/source-contracts.md](docs/source-contracts.md)
- **`tools/cli/internal/capability` is the interface-capability engine** (#128 §65–§74, grill Q2d: a surface declares via `?caps=` on the SSE connect which presentation commands it accepts; delivery gated at the broadcast choke points; a declaration only subtracts — undeclared clients are legacy, byte-identical). The Go package is normative; the TS mirror `packages/server/src/capability.ts` must stay in lockstep, and the go-server mirror `packages/go-server/internal/capability` is pinned byte-identical by its own sync test. → [docs/interface-capabilities.md](docs/interface-capabilities.md)
- **Crew status is dual-gated: `PARLAY_CREW_STORE` enables dual-write, `PARLAY_CREW_READ_BEADS=1` flips readers** — both unset is byte-identical legacy; the status file's bytes and crew-state's exit codes are frozen contracts, and `parlay status-migrate` against the live agents root is captain-gated (robots-lor). → [notes](docs/agent-notes/crew-status-two-gate-rollout.md)
- **`parlay route` hardens by arithmetic, not a flag** — Beta(1,1) posterior over *captain-only* feedback; un-hardening is just a correction; only exit 0 acts. Model: `docs/routing.md`. → [notes](docs/agent-notes/route-hardening-is-arithmetic-over.md)
- **`city/` is parlay's authored Gas City city + pack source, not a live city** — never run city-mutating `gc` verbs against it with the default `GC_HOME`; validate against a copy with `GC_HOME` redirected. → [notes](docs/agent-notes/city-is-the-authored-gas-city-source.md)
- **The pinned gc cannot use the captain's bd fork** — a gc city store needs an upstream `bd` (schema/config skew fails `session new` both directions); sandbox recipe + lifecycle traps in the gated test. → [notes](docs/agent-notes/pinned-gc-speaks-upstream-bd-not-the-fork.md)
- **Parlay-launched agents (`parlay spawn`/`parlay claim`) get a launch record** (`AgentInfo.launchedBy`/`startedAt`) and are idle-reaped after 2h (`PARLAY_AGENT_IDLE_TIMEOUT_MS`) via the `parlay shutdown` primitive — firstmate-spawned agents carry no `launchedBy` and are never touched. → [notes](docs/agent-notes/idle-reap-parlay-launched-agents-task-4dz9.md)

## Port-ticket archaeology

Historical per-ticket notes. Open these only when working on that specific area — they explain why a port is bug-for-bug faithful to a TS quirk.

- [B5 `status`/`crew-state`/`supervise`/`context-check`](docs/agent-notes/go-cli-ticket-b5-status-crew.md) · [B6 `robots-watch`/`robots-tail`](docs/agent-notes/go-cli-ticket-b6-robots-watch.md) · [B7 `doctor`/`health`](docs/agent-notes/go-cli-ticket-b7-doctor-health.md) · [B8 `resolve-handoff`/`say-guard`](docs/agent-notes/go-cli-ticket-b8-resolve-handoff.md) · [B9 `launch`/`drawdown`/`idle`](docs/agent-notes/go-cli-ticket-b9-launch-drawdown.md) · [B10 coverage/parity close-out](docs/agent-notes/go-cli-ticket-b10-coverage-parity.md)
- [C3 drafts/uploads/settings](docs/agent-notes/go-server-ticket-c3-drafts-uploads.md) · [C6 launchd deploy tooling](docs/agent-notes/go-server-ticket-c6-parlay-server.md)

One rule from that workstream still applies to new work: a "port X" ticket may already be done by an earlier ticket's broader scope — grep `internal/` first.

**The TS↔Go parity harness is gone, and with it the safety net.** `packages/cli` was retired in T-08, so `tools/cli/parity/run.sh` had nothing left to diff against and was deleted with it. Two consequences: Go-only verbs no longer need a `GO_ONLY_VERBS` entry (there is no help diff to redden), and the harness that used to catch a ported command's dropped flag no longer runs — **a dropped flag is not a degraded flag, it is a hard exit** callers may be discarding, and nothing checks for one automatically now. Any remaining `parity/run.sh` mention in `docs/agent-notes/` is archaeology describing how it worked, not a live instruction.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.

**Size is a correctness property here.** This file loads into every session's context window, so bytes spent here are bytes no agent can spend on the actual task. It was once 124 KB (~31k tokens) and had to be split. Keep it under ~20 KB:

- A new lesson gets **one line** here — the rule and its consequence — plus the full rationale in a new `docs/agent-notes/<slug>.md`.
- Prefer rewriting or replacing an existing line over adding a new one.
- When a note goes obsolete, delete both the line and its doc.
