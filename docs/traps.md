# Traps, in the order they will bite you

[`AGENTS.md`](../AGENTS.md) records this repository's incidents in the order they happened — right to maintain, wrong to meet. This file is the same knowledge in the order you will meet it. **Nothing here is new:** each trap is one line — what it is, and when it bites — linking the note under [`agent-notes/`](agent-notes/) that owns the rationale (the link says so where that rationale lives in a longer document instead).

Read the stage you are in. [`../README.md`](../README.md) is the command-by-command path this ordering hangs off, `./bin/parlay-preflight` is its step 0 and the command to re-run when a machine misbehaves, and [`../VISION.md`](../VISION.md) with [`../VISION-answers.md`](../VISION-answers.md) say what parlay *is*.

## 1. Before you have run anything

What the machine needs, and the shape of the tree. `./bin/parlay-preflight` names every
missing prerequisite at once rather than one per attempt.

- **Four Go modules, no root `go.work`.** Run `go` from inside the module it names; a repo-root-relative path dies
  with *"go.mod file not found in current directory or any parent directory"*. →
  [four modules](agent-notes/go-rewrite-of-packages-server-use.md)
- **`CGO_ENABLED=0` for `tools/cli`.** Without it the beads dependency's embedded-Dolt tree drags in a C++ ICU
  binding and dies on `'unicode/regex.h' file not found`, on your first CLI build or test. →
  [`docs/status-lift-topology.md`](status-lift-topology.md)

## 2. Your first server and your first message

`cd packages/go-server && go run ./cmd/parlay-server`, then
`./bin/parlay send --demo --force "hello"` and `./bin/parlay history 5` from the clone
root. No account, no tunnel, no panel needed. The Quickstart's step 1 is the same thing
in a throwaway sandbox (`./examples/bootstrap-sandbox.sh`) — the fastest way to see the server, CLI, registry,
history and reply path work without touching your own state; its `LIMITS` line names what is not covered: delivery and the relay.

- **Never `pkill -f` a parlay-looking process on a host with a live install** — the production server is a launchd
  job (`com.parlay.go-server` today; `com.parlay.chat-server` for the retired Bun server), so kill a test server by
  port or pid. And it reads and writes `~/.parlay` by default
  (history, registry, drafts, settings, uploads), so a second server without `-state-dir`/`PARLAY_STATE_HOME`
  collides with a live install. → [the server process](agent-notes/packages-server-is-a-standalone-bun.md)
- **The panel bundle is resolved once, at server start.** A server started before `packages/client/dist` exists
  keeps the bare `dist` fallback and answers `503` on `/` for the rest of its life — after the build, restart it. →
  [assets resolve once](agent-notes/assets-resolve-once-at-server-start.md)

## 3. An agent that actually receives what you sent

`parlay listen --agent demo --name Demo --legacy-poll` needs nothing else; without it you need
`tools/relay/build.sh` first, and an already-running host relay is used instead (stage 4).

- **A registration is not a listener, and a spool is not delivery.** Liveness is the registry intersected with the
  host's process table, so a tab can look live and be deaf. →
  [registration ≠ listener](agent-notes/a-registration-is-not-a-listener-robots-jkwc.md)
- **Never make an agent's reply channel one process** — respawn rather than propagate, resume where delivery
  stopped, report on stdout. → [the listen stream](agent-notes/the-listen-stream-is-supervised-never-robots-gv6t.md)
- **A finished pane still accepts messages**, so your reply reaches an agent that finished an hour ago. Check
  `parlay stale <agent-id>` first. →
  [a finished pane](agent-notes/a-finished-pane-still-accepts-messages-robots-9d2w.md)
- **A retired agent's spool must be renamed out of the `*.chan` glob**, or `resumeFromSpools` re-registers it on
  every relay restart. → [spool tombstones](agent-notes/relay-resume-tombstones-retired-spools-task-0n80i.md)

## 4. A second instance, or a fleet, without breaking your first

Use `examples/bootstrap-sandbox.sh` rather than hand-rolling a sandbox: the hard parts are
the couplings no single environment variable covers.

- **A test instance needs four redirects, not one** — `HOME`, `PARLAY_STATE_HOME`, `PARLAY_AGENT_HOME`, `PAI_DIR` —
  and there is no `PARLAY_DATA_DIR`. The listener layer is a *fifth* surface nothing redirects. →
  [use the sandbox](agent-notes/need-a-real-parlay-instance-to.md)
- **Arming a listener is a takeover, not an addition.** One live poll loop per agent *id*, matched host-wide with no
  server or state-dir discrimination: two instances reusing an id evict each other, and the loser is registered but
  deaf. → [listener takeover](agent-notes/arming-a-listener-is-a-takeover-robots-fgyz.md)
- **The relay is a per-user singleton bound to one upstream server for life**, so a second instance's enroll looks
  live and delivers nothing. The probe that refuses that is conclusive only when the relay can report its upstream: a
  relay predating that field answers `{"ok":true}` alone, is let through, and the enroll goes to the relay's own
  server — so on a host that already runs one, prefer `--legacy-poll` or your own `PARLAY_RELAY_RUNTIME`. →
  [the relay singleton](agent-notes/the-relay-is-a-per-runtime-robots-buu8.md) ·
  [a relay that cannot name its server](agent-notes/a-relay-that-cannot-name-its-server-passes.md)
- **The canonical runtime dir is reserved** — a relay belonging to another server parked in it is a fleet outage.
  Give a second instance its own `PARLAY_RELAY_RUNTIME`. →
  [the runtime dir](agent-notes/the-canonical-runtime-dir-is-reserved-robots-93xu.md)
- **The name you typed decides the state dir.** `parlay-dev` moves client state to `~/.parlay-dev` but still reaches
  the same `:4242`, and registration is server-side. →
  [fleet vs core](agent-notes/fleet-vs-core-split-parlay-dev.md)

## 5. When something looks wrong

Four verbs, four questions: `./bin/parlay-preflight` (this machine), `parlay health` (the
running instance your CLI points at), `parlay doctor deploy` (a launchd host), and
`parlay doctor` alone (one *agent's* self-diagnosis, which fails from a plain shell by
design).

- **"Not answering `/health`" is not "not running."** Never force-restart a relay. →
  [never force-restart](agent-notes/not-answering-health-not-running-never-robots-mpr3.md)
- **A verb missing from `parlay commands` is a cached answer, not a fact** — check
  `$PARLAY_STATE_HOME/command-report-unsupported` first. →
  [commandreport cache](agent-notes/commandreport-caches-a-404-on-disk.md)
- **A check must probe the path the product runs**, never a second implementation of it — which is why `health`
  reports on the server your CLI is actually pointed at. →
  [doctor/health B7](agent-notes/go-cli-ticket-b7-doctor-health.md)
- **Debugging from a phone is a supported mode** — remote debug log, on-screen console. →
  [mobile triage](agent-notes/remote-debug-log-on-screen-mobile.md)

## 6. Spawning your first background agent

`parlay spawn` launches an agent that enrols as a live tab. It needs a model — there is no
default — and launches into a herdr terminal unless told otherwise.

- **There is exactly one spawner, in-process behind `parlay spawn`** — the bash spawner and its `PARLAY_SPAWN_IMPL`
  escape hatch are gone, so the model and beads gates cannot be routed around. →
  [the only spawner](agent-notes/go-spawner-folded-into-tools-cli.md)
- **`--model` is mandatory with no silent fallback:** omit it and `spawn` exits 2 with *"refusing to spawn — no
  model was chosen"*. `--profile` and `--no-pii` are the other two ways through the gate. →
  [`docs/launcher.md`](launcher.md)
- **The charter cannot ride on herdr's argv** — it refuses to encode a newline, so `agent prompt` delivers the
  multi-line charter and a failed delivery rolls the tab back. → [`docs/launcher.md`](launcher.md)
- **The startup-prompt template is single-source**: the real files are in
  `tools/cli/internal/spawn/launch-templates/` and the repo-root copies are symlinks. →
  [single-source template](agent-notes/startup-prompt-template-is-single-source.md)
- **`subprocess` is the herdr-free escape hatch**, whose stdin charter delivery is an explicitly unverified
  assumption. → [subprocess launcher](agent-notes/subprocess-launcher-a-herdr-free-escape.md)
- **`treehouse get --lease` RESETS the slot it hands out** — it checks `origin/main` out over whatever branch that
  slot held. Run `bin/parlay-treehouse-guard` first, as both spawn paths do. →
  [slot reset](agent-notes/treehouse-get-resets-the-slot-it-robots-n8d9.md)
- **`treehouse` picks its pool from the process cwd** and has no `--repo` flag, so pin the child's cwd, and identify
  the repo with `git rev-parse --git-common-dir`. →
  [cwd-selected pool](agent-notes/treehouse-picks-its-pool-from-the-robots-d04t.md)
- **Retire an agent with `parlay shutdown <id>`** rather than letting its listener, spool and row time out — but the
  server-side half does less than the note claims, so it is complete only for a local agent. →
  [graceful shutdown](agent-notes/graceful-agent-shutdown-task-35ww.md)
- **Finished agents are only collected by `parlay sweep`** — four hold-guards, each from a real incident. →
  [only sweep collects](agent-notes/finished-agents-are-only-collected-by-robots-6xq7.md)
- **Nothing reaps an agent row by age** — retirement is explicit, a registry row is not liveness, and the
  idle-timeout variable is read nowhere. →
  [no idle reaper](agent-notes/idle-reap-parlay-launched-agents-task-4dz9.md)
- **`handoff` is not a command this repo installs.** A runtime string naming it must branch on
  `resolvehandoff.StoreAvailable` and name the portable substitute, or `tools/cli/handoff_prescription_test.go`
  fails the build. → [never prescribe it](agent-notes/never-prescribe-the-handoff-store.md)

## 7. Changing the code

Run a suite from inside its package, keep the modules separate, and expect harnesses to
guard themselves. This is where a green result is most likely to be lying to you.

- **CI is `.github/workflows/ci.yml`** — jobs go/shell/hygiene, a 2 MiB tracked-blob ceiling, a docs-index gate, and
  a gate that fails any committed `*.sh` reaching for a deleted artifact. →
  [CI is this workflow](agent-notes/ci-is-github-workflows-ci-yml.md)
- **`bun test` only works from inside a package directory** — a DOM suite at the root fails with *"document is not
  defined"* while passing in-package. → [run it in-package](agent-notes/bun-test-only-works-from-inside.md)
- **The bun job's `*.test.ts` coverage gate is repo-wide**, and it fails loudly rather than skipping — a new test
  file elsewhere can turn it red. → [repo-wide TS gate](agent-notes/bun-test-ts-gate-is-repo-wide.md)
- **`make test-bdd` runs the Gherkin scaffold in `features/`**, and there is no `packages/eval-engine` — the matcher
  is `tools/cli/internal/evalengine/`. → [the eval engine path](agent-notes/bdd-scaffold-and-eval-engine-path.md)
- **Never assert on elapsed time across a subprocess** — bun's startup jitter exceeds any bound loose enough not to
  flake, so the assertion cannot fail. → [timing assertions](agent-notes/a-timing-assertion-loose-enough-not.md)
- **A probe written as `VAR=$(cmd)` is not best-effort** under `set -euo pipefail`; write `VAR="$(cmd)" || VAR=""`.
  → [best-effort probes](agent-notes/a-best-effort-probe-written-as-robots-dcag.md)
- **Shell harnesses must self-isolate** — developer PATH shims leak into every subprocess; one `sleep` guard turned
  a harness red in five places that were all the shim. →
  [self-isolating harnesses](agent-notes/shell-harnesses-self-isolate-robots-buu8.md)
- **A `stop()` that returns before its goroutine does is not a `stop()`**, and a reaped pid gets reissued. CI runs
  `go test -race`; a race failure is a report about production. →
  [stop() and reaped pids](agent-notes/a-stop-that-returns-before-its.md)
- **A resettable `sync.Once` and a never-stopped test goroutine are data-race magnets** — a test-only cache reset
  needs a real mutex; stop the bridge via `t.Cleanup`. →
  [cache race](agent-notes/linkrewrite-cache-race-hub-goroutine-leak.md)
- **`internal/httpc` has exactly one timeout-less client** (`UnboundedClient`, one legitimate caller). Do not widen
  it. → [the unbounded client](agent-notes/internal-httpc-has-exactly-one-timeout-robots-gxlb.md)
- **On modern macOS `ps eww` hides env vars for sealed platform binaries**, even as root, so an env-matching
  scanner's fixture is a self-re-exec'd test binary. →
  [ps hides env](agent-notes/macos-ps-env-hides-platform-binaries.md)
- **`Bun.spawn` without an explicit `env` snapshots the environment at Bun startup**, and a signal-killed spawn does
  not reject its awaited promises. → [Bun.spawn gotchas](agent-notes/bun-spawn-env-and-abort-gotchas.md)
- **A retry loop against a refused port is a CPU spin, not a poll** — the loop needs a floor. →
  [retry floors](agent-notes/lavish-poll-hot-spin-and-orphan-guards.md)
- **A spawned process outlives its spawner unless something ENDS it** — own the child's death: watch your own
  `PPID`, background the pipeline so traps fire, and match whole command lines rather than `pgrep -f` regexes. →
  [children outlive spawners](agent-notes/a-spawned-process-outlives-its-spawner-robots-3pvi.md)
- **A mutating or identifier-aiming `/api/chat` route goes in `internal/guard.GuardedPaths`** — a test parses every
  route on the mux and fails the build if you skip it. →
  [the guard's route list](agent-notes/packages-server-is-a-standalone-bun.md)
- **A hand-rolled CSRF gate is not a boundary** — one that reimplemented the guard's content-type check accepted a
  forged cross-origin POST. → [hand-rolled CSRF](agent-notes/a-hand-rolled-csrf-gate-is-not-a-boundary.md)
- **`POST /api/chat/events` is the out-of-process ingress seam**, with a one-name-per-real-producer allowlist. Do
  not widen it. → [the ingress seam](agent-notes/out-of-process-producers-reach-the.md)
- **The live-command registry stores no free-form text** — verb, agent id, pid, flag *names*, outcome token; never
  argv, values, paths or error strings. → [what it sees](agent-notes/the-live-command-registry-sees-only.md)
- **`tools/cli` cites docs that do not exist in this checkout**, and the TypeScript source it was ported from is
  gone: reach it with `git log --all -- packages/cli/src/<file>.ts` and treat it as archaeology, not a spec. →
  [tools/cli's docs](agent-notes/go-cli-tools-cli-the-packages.md)
- **Publishable packages use flat, unscoped `parlay-<part>` names** — the `@parlay` scope is never published, and
  bare `parlay` on npm belongs to someone else. →
  [flat names](agent-notes/publishable-packages-use-flat-unscoped-parlay.md)

## 8. Landing the change

Work lands only through a PR whose checks passed. `origin/main` cannot be pushed to at all,
and a green check is not by itself evidence that anything reviewed the diff.

- **Never plan to push directly to `origin/main`.** Branch protection declines the push for every account including
  the owner — *"protected branch hook declined"* is the refusal, not a transient error — and never modify the
  protection. No note owns this one; the rule is in [`../AGENTS.md`](../AGENTS.md).
- **Never merge on a green check alone.** Run `parlay merge-gate <pr>`; CodeRabbit reports conclusion `pass` when it
  never ran, and it never auto-runs here because the repo has under ten stars, so commenting `@coderabbitai review`
  is the only gate-visible review evidence. →
  [never merge on green](agent-notes/never-merge-on-a-green-check-robots-jap6.md)
- **A `READY` gate is not evidence a review found nothing.** CodeRabbit posts findings it cannot attach to the diff
  as *"Outside diff range comments"* in the review body, where they create no thread the gate can see. →
  [review body](agent-notes/never-merge-on-a-green-check-robots-jap6.md)
- **A conflicted PR gets NO Actions runs — missing checks, not red ones.** Resolve the conflict; do not retrigger. →
  [conflicted PR](agent-notes/a-conflicting-pr-gets-no-actions.md)
- **`git diff origin/main <branch>` is not a question about the branch** — two-dot diff reports a *behind* branch as
  having deleted merged work. Run `parlay branch-audit`. →
  [branch audit](agent-notes/git-diff-origin-main-branch-is-robots-d988.md)
- **Never prove a fix landed with a bare `gh pr view`** — `gh` prefers an `upstream` remote over `origin`. Run
  `parlay landed <pr>`; never parse `git branch` output. →
  [landing proof](agent-notes/never-prove-a-fix-landed-with-robots-0a77.md)
- **Every worktree removal goes through `checkWorktreeGitSafety`**, and teardown refuses on lease, liveness, borrow,
  freshness, git state and stashes — `--force` bypasses only the inspectable git half. →
  [worktree safety](agent-notes/every-path-that-removes-a-worktree-robots-cncx.md) ·
  [teardown gates](agent-notes/teardown-gates-liveness-lift.md)
- **Two-arg `git merge-tree` is not a predicate.** Teardown's landed check silently never fired, so a squash-merged
  agent kept refusing to release — use `--write-tree` against `<ref>^{tree}`. →
  [two-arg merge-tree](agent-notes/two-arg-git-merge-tree-is-robots-ceon.md)

## 9. Only if you go deeper

Needed to run parlay: none of it. This is the record's archaeology and its opt-in planes.

- **A "port X" ticket may already be done** by an earlier ticket's broader scope — grep `internal/` first.
  Per-ticket notes: [`status`/`crew-state`](agent-notes/go-cli-ticket-b5-status-crew.md) ·
  [`robots-watch`](agent-notes/go-cli-ticket-b6-robots-watch.md) ·
  [`resolve-handoff`](agent-notes/go-cli-ticket-b8-resolve-handoff.md) ·
  [`launch`/`drawdown`](agent-notes/go-cli-ticket-b9-launch-drawdown.md) ·
  [coverage](agent-notes/go-cli-ticket-b10-coverage-parity.md) ·
  [server drafts](agent-notes/go-server-ticket-c3-drafts-uploads.md) ·
  [launchd deploy](agent-notes/go-server-ticket-c6-parlay-server.md)
- **`city/` is parlay's authored Gas City source, not a live city** — never run city-mutating `gc` verbs against it
  with the default `GC_HOME`. → [city](agent-notes/city-is-the-authored-gas-city-source.md) ·
  [pinned gc](agent-notes/pinned-gc-speaks-upstream-bd-not-the-fork.md) ·
  [the brain probe](agent-notes/gc-main-brain-probe.md)
- **`parlay mechanic on|off|status` is the kill switch** for the robots→mechanic auto-spawner — a sentinel file, not
  launchd, with no backlog replay on re-enable. → [kill switch](agent-notes/parlay-mechanic-on-off-status-is.md) ·
  [its canonical source](agent-notes/mechanic-dispatch-canonical-source-lives-in.md)
- **Crew status is dual-gated** (`PARLAY_CREW_STORE` writes, `PARLAY_CREW_READ_BEADS=1` reads); both unset is
  byte-identical legacy, and `parlay status-migrate` on the live agents root is captain-gated. →
  [crew status](agent-notes/crew-status-two-gate-rollout.md)
- **`parlay route` hardens by arithmetic over captain-only feedback, not by a flag** — only exit 0 acts. →
  [routing](agent-notes/route-hardening-is-arithmetic-over.md)

## Adding a rule

Add the fact to `AGENTS.md` (one line) and its rationale to a new
`docs/agent-notes/<slug>.md`, then place it here at the stage where it bites. This file is
an ordering, not a second source of truth: if it and a note disagree, the note wins and
this file has the bug.
