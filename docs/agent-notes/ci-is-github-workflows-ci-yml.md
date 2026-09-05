# CI is `.github/workflows/ci.yml` — and a green check was never proof before it

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Until this landed the repo had no `.github` directory at all: the only PR check
was CodeRabbit, which reports conclusion `pass` even when its account-wide rate
limit meant it never read the diff (see the merge-gate section above — that verb
exists because of the same lie). A triage of the open-PR backlog found 23/23 PRs
green and 3 provably broken.

Four parallel jobs, each pinned to action commit SHAs with `permissions:
contents: read` and no `pull_request_target`: **go** (build/vet/test/gofmt over
all five modules), **bun** (tests for `packages/{input,client,server,cli}` and
`tools/gate-tag` — which gets no `bun install`, having no `package.json` and no
dependencies — plus typecheck for `packages/input` and `tools/split-test`),
**shell** (nine hermetic harnesses, preceded by a `git`/`jq`/`curl`/`python3`
presence check so a binary missing from the rolling runner image fails the step
instead of letting a harness skip itself green — `python3` is on that list
because `bin/context-reset.test.sh` needs a real pty), **hygiene** (conflict
markers, 2 MiB tracked-file ceiling measured on the tracked *blob* via `git
ls-tree -l`, never `stat` on the worktree path, which would follow this repo's
tracked symlinks).
Both hygiene gates distinguish "the tool failed" from "the tree is clean" —
`git grep`'s status 2+ and a failed or empty `git ls-tree` each fail the step.
Read the file's own comments for per-step rationale rather than re-deriving it.

Four things worth knowing before editing it:

- **Only pull-request runs share a concurrency group.** PR runs key on
  `github.ref` with `cancel-in-progress`, so a new push supersedes the old run.
  Push-to-main runs key on `github.run_id` — a group of one each. That is not
  interchangeable with `cancel-in-progress: false`: runs sharing a group key
  evict each other while still *pending*, so a burst of landings on the shared
  `refs/heads/main` key would drop the middle commits' runs before they started.
  Only a unique key guarantees every landed commit gets a verdict.
- **`gofmt` in CI is not a duplicate of `TestGofmtClean`.** That test
  (`tools/cli/internal/commands/gofmt_test.go`) resolves its root to the
  tools/cli module, so it guards one module of the `GO_MODULES` list; the CI
  step covers the rest. Two modules have external dependencies and go.sum
  files (tools/cli, packages/spawn-profiles); the other two
  (packages/go-server, tools/relay) are pure stdlib.
- **Every test step redirects `$HOME`, and it is load-bearing, not ceremony.**
  `packages/cli`'s tests resolve `join(homedir(), ".parlay", "agents", …)`
  directly and really do create it; several Go tests write `~/.parlay`,
  `~/.config/bd`, `~/.beads`. A hosted runner throws `$HOME` away, but this must
  also be safe on a self-hosted one — see the `uninstall.sh --purge` incident
  above, where a smoke test permanently deleted the live `~/.parlay`. Because Go
  derives `GOCACHE`/`GOMODCACHE` from `$HOME`, the go job pins those to explicit
  paths *before* the redirect; drop that step and the cache silently evaporates.
- **Deliberately not in CI**, because they drive live or macOS-only state:
  `tools/relay/deploy/{ensure-up,install}.test.sh` (launchctl/PlistBuddy),
  `tools/cli/parity/run.sh` (stands up a real go-server fixture; the parity
  harness was retired with `packages/cli` in T-08, so this entry is archaeology),
  `examples/bootstrap-sandbox.sh` (same class as the previous entry — it stands
  up a real `packages/server` fixture; it has also not been trial-run to the
  bar stated at the end of this bullet), and
  `packages/client`'s `bun run build` (its `build.ts` POSTs to the captain's
  live `:31337` — see the packages/client note above). Also not enforced:
  `tools/hooks/pre-commit`'s 250-line ceiling on staged `.ts` files — it is a
  staged-diff check, not a whole-tree one, so it does not map onto a CI job; it
  is named here so a contributor whose commit the hook rejects can find the
  rule written down. Every shell harness in the job cleared a hermeticity bar
  before it was added — see the `shell` job's own comment in `ci.yml` for what
  each one had to show; that is the bar for adding another.

The `go` job's artifact guard is `git status --porcelain` being empty after
a full `go build ./...`, not a filename list, so a newly added main package
cannot reintroduce the 9.6 MB `tools/relay/relay` binary that one PR committed —
that path is now gitignored alongside the other Go modules' binary paths
(the pre-existing `packages/eval-engine/eval-engine` entry went away when that
module merged into tools/cli, task-0ke9).
