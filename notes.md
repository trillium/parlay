# Onboarding work: what changed, and the evidence

This branch makes the first hour in this repo readable instead of source-bound. Seven
pieces, in the order a newcomer meets them:

| Piece | What it is |
|---|---|
| `bin/parlay-preflight` (+ `bin/parlay-preflight.test.sh`) | One bash-only command that checks the whole prerequisite set and names **every** missing thing in one run with its exact `fix:` command. Step 0 of the README path, and the command to re-run when a machine misbehaves. |
| `docs/traps.md` | `AGENTS.md`'s incident record reordered into the nine stages a newcomer walks through. All 66 `docs/agent-notes/` notes appear, none dropped, none rewritten, every link target unchanged. |
| `README.md` Quickstart | One ordered five-step path with a real first command, an isolated-fleet step, one health surface named in both roles, and success/failure criteria on every step. |
| `.github/workflows/ci.yml` (new hygiene step) | Keeps the two lines above from rotting: every `docs/agent-notes/*.md` must appear in the ordering as a **link target** (a prose mention is not placement), every one of the five path documents must exist, and every local link in them must resolve. |
| `docs/agent-notes/a-relay-that-cannot-name-its-server-passes.md` | A newly observed fact: the relay check that protects against cross-instance enrollment is inert against a relay older than its own `server` field. Linked from `traps.md` stage 4. |
| `docs/agent-notes/assets-resolve-once-at-server-start.md` | A newly observed fact (below): the server's panel-assets directory is chosen once at startup, so a server started before the bundle exists serves `503` for the rest of its life. Linked from `traps.md` stage 2. |
| `.gitignore` | `.pi/` ignored, and the harness task-log files an auto-commit had picked up are untracked — so no harness scratch file can be committed again. |

## What a newcomer can now do without reading source

1. **Run `./bin/parlay-preflight`** and be told everything the machine needs at once, with
   the exact remedy for each, rather than one blocker per failed build.
2. **Run `./examples/bootstrap-sandbox.sh`** and watch the server, CLI, registry, history, the reply path and `doctor` work in a throwaway sandbox whose `LIMITS` line names what it does *not* cover (delivery and the relay); steps 2–5 then give a served message, a read-back, a health verdict, a panel that loads, and three predicted non-zero exits (voice engine; `doctor`; `doctor deploy`).
3. **Know where to look when it breaks**: the same `./bin/parlay-preflight` an operator runs, then `docs/traps.md` at the stage they are in.

## What was reordered, or corrected, and why

- **`docs/traps.md` is new** (246 lines): `AGENTS.md` and `docs/agent-notes/` are in incident
  order — right to maintain, wrong to meet. It orders the same facts by when they bite: before
  you run anything → first server and message → an agent that receives → a second instance or
  fleet → when something looks wrong → spawning → changing the code → landing → going deeper.
- **The README Quickstart gained a step and was renumbered** (step 1 is now the sandbox,
  server → 2, CLI → 3, talk/health → 4, panel → 5), every internal step reference updated,
  and every step now states what success and failure look like.
- **The `-state-dir` advice was corrected.** It read "fully isolated from live state", which
  overclaims: `-state-dir` moves the server's store and nothing else — `HOME`, the agent store,
  the TTS cache and the listener layer are untouched. It says that and points at the sandbox.
- **The health surface is one surface, named in both roles.** `./bin/parlay-preflight` is the
  machine half, `./bin/parlay health` the running-instance half; step 0, step 4 and "when a
  step fails" all say so. The preflight is state-aware: with an instance already answering on
  the resolved port it prints `Ready. A parlay instance already answers on 127.0.0.1:4242 —
  nothing to start.`
- **The relay follow-on paragraph was corrected.** Running the path proved its absolute claim
  false: `parlay monitor`/`listen` without `--legacy-poll` enroll into whatever relay the host
  is already running, and that check cannot refuse when the relay is too old to report its
  upstream in `/health` — so an enroll into the wrong server looks live (observed: `preflight
  OK — canonical relay is up for 'demo'`, no `and polling <url>`). The README now says to read
  that suffix and gives both remedies; the full finding, with the code on both sides and the
  transcript, is `docs/agent-notes/a-relay-that-cannot-name-its-server-passes.md`.
- **Step 5's panel claim was false on a fresh clone, and fixing it is this iteration's work.**
  It read *"the server found the bundle by itself … so the command in step 2 works unchanged."*
  It does not: the assets directory is resolved **once, when the server starts**, and on a
  fresh clone the bundle is gitignored and absent, so the step-2 server binds a bare `dist` and
  answers `503` for the rest of its life — building it in step 5 changes nothing about that
  process. Executed on a fresh clone, in that order: `assets: dist` at boot, `bun run build`
  exit 0, `GET /` → `503`; the README's own `503` diagnostic ("the bundle is not built") was
  therefore also wrong. The README now says to Ctrl-C and re-run step 2 (or build before step 2,
  or pass an absolute `-assets-dir`), makes `200` step 5's success criterion, and splits the
  diagnostic into "never built" versus "built after this server started";
  `docs/command-server.md` records the startup-only resolution and the relative-`-assets-dir`
  behaviour (`../client/dist` works from `packages/go-server`, `packages/client/dist` does not);
  `docs/traps.md` stage 2 places the new note. No code changed — the resolution is a deliberate
  product behaviour, so the document was fixed rather than the server.

## What now keeps the path from rotting

The docs-index gate scans `docs/*.md` only, so a note under `docs/agent-notes/` that no
document links is invisible to every gate (one existed until `traps.md` was written), and a
broken relative link renders as ordinary text on GitHub. One new hygiene step closes both,
deriving each side from the filesystem and resolving each link against the directory of the
file that contains it (against the repo root it reported 117 false failures). Scope is the
newcomer path — `README.md`, `AGENTS.md`, `docs/README.md`, `docs/traps.md`,
`examples/README.md` — deliberately not all of `docs/`, which also holds dated snapshots whose
value is being what was true that day. Proved to fail, not just to pass: the run block is
extracted from the workflow YAML (tested text = shipped text) and was run in a fresh copy of
the tree for each shape — control, an unplaced note, a prose-only mention, a missing path
document, a dangling link, and an empty `agent-notes/` (12/12 assertions). The two cases
CodeRabbit raised against the first version (prose counted as placement; a missing document
skipped silently) are named and fatal now.

## Harness scratch was committed once, and now cannot be

Nothing under `.pi/`, `.gnhf/` or any harness scratch directory may be committed, and this branch
broke it once: the auto-commit that landed `docs/traps.md` captured eight `.pi/tasks/<session>/*.json`
+ `*.output` files because `.pi/` was not ignored. Fixed by untracking them (`git rm --cached`,
files kept) and adding `.pi/` to `.gitignore`; `.gnhf/` needs none, as `.git/info/exclude` covers
`runs/`. A file added in one commit and removed in another leaves no trace in a PR's net diff.

## What was deliberately left alone

- `AGENTS.md`, `docs/agent-notes/*` and every existing incident record: no fact was changed
  (the preflight paragraph and the CI-gate line are the only additions).
- `examples/bootstrap-sandbox.sh`: reused as-is, not reimplemented — it already encodes the
  isolation recipe, including the hardcoded paths.
- Deployment scripts, `internal/guard.GuardedPaths`, and every public endpoint shape.
- `tools/monitor/parlay-monitor.sh`: its tolerance for a relay that cannot report its upstream
  is deliberate and documented in the code — a product decision, not an onboarding one.
- The server's startup-time asset resolution: documented, not made lazy. What a mid-flight asset
  swap should do is a product decision; the new note says so and warns against a drive-by fix.
- `docs/ux-eval-2026-08-30.md`: a dated field report citing the README's old step numbers, and
  the record of the walk-up fix this iteration's finding shows was incomplete. Rewriting it would
  falsify evidence; the new note points at it instead.
- The README below the Quickstart (system map, layout, worked config, development, publishing):
  reachable and accurate, so left in place.
- No root `go.work`: it would not make the stop condition below pass, and it contradicts the
  README, `AGENTS.md` and CI doctrine that all assert there is none.

## Verification

### The configured stop condition, literally, at the repo root

```
$ go build ./... && go vet ./... && gofmt -l . | (! grep .) && go test ./... && make test-bdd
pattern ./...: directory prefix . does not contain main module or its selected dependencies
exit=1
$ for m in tools/cli tools/relay packages/go-server packages/spawn-profiles; do   # the same chain, per module
  (cd $m && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...); done
tools/cli exit=0 · tools/relay exit=0 · packages/go-server exit=0 · packages/spawn-profiles exit=0
$ gofmt -l .            → (no output, exit 0)      $ make test-bdd → exit 0, 17+7 scenarios passed
$ bash bin/parlay-preflight.test.sh → 20 passed, 0 failed, exit 0
```

The repo is four Go modules with no root module, so `./...` at the root cannot resolve.
Pre-existing, unrelated to this branch, and **not gamed**: a vacuous root module would exit 0
while testing nothing, and a root `go.work` does not fix it either (verified on this branch,
then removed). The baseline before any change was identical, with no known-red on this box —
including `make test-bdd`, which was green before the first commit and every iteration since.

### The newcomer path, executed end to end on a fresh clone with a clean `HOME`

`git clone` of this branch, `HOME` at an empty directory, `GOCACHE`/`GOMODCACHE` pinned at the
real ones. Steps 0–5 run literally as the README writes them:

```
$ ./bin/parlay-preflight
  ok    checkout      /private/tmp/fm-fresh-clone — 4 Go modules, bin/parlay present
  ok    go            go1.26.5 (tools/cli requires 1.26.5)      ok    git · curl · bun
  ok    HOME /tmp/fm-newcomer10 · CLI state … (writable) · build dir tools/cli/bin · relay runtime
  ok    server port 127.0.0.1:4242 is free · engine port 127.0.0.1:4343 is free (optional)
  ok    isolation     /tmp/fm-newcomer10/.parlay is not the live default state home, or holds no agents
12 checks: 12 ok, 0 warning(s), 0 failure(s).      exit=0
$ ./examples/bootstrap-sandbox.sh
  PASS  registry served the seeded agents · message round-tripped through the server · message persisted
        to the state dir's messages.jsonl · reply path routed --agent helm onto the helm channel
  PASS  server URL resolved from config.json · identity.md read back with frontmatter stripped
  PASS  launch spec discovered for both agents, both reported ghost (registered, no listener)
  PASS  seeded history served on both channels · doctor PASSes identity, registry membership, reachability
  PASS  server persisted only into the state dir it was given
  LIMITS  UNCOVERED (delivery, the relay) · BOUND (the unauthenticated port while up)
all checks passed — port 64791      exit=0
$ cd packages/go-server && go run ./cmd/parlay-server                              # step 2
parlay-server: listening on http://127.0.0.1:4242 (state dir: /tmp/fm-newcomer10/.parlay, assets: dist)
$ ./bin/parlay remote                                                              # step 3
http://localhost:4242 (source: default)
$ ./bin/parlay send --demo --force "hello" ; ./bin/parlay history 5                # step 4
sent to demo — id m0
[12:59:16] you          hello
$ ./bin/parlay health ; echo exit=$?
ok    server http://localhost:4242 — 0 client(s), 0 poller(s), 0 agent(s)
FAIL  eval-engine http://127.0.0.1:4343 — connect: connection refused   # optional engine, not your install
exit=1
$ cd packages/client && bun run build                                              # step 5
dist/parlay-agent.js + index.html + pulse-agent.js + index.js + plugins built      exit=0
$ curl -sS -o /dev/null -w '%{http_code}\n' http://localhost:4242/
503          ← the bug: this server resolved its assets before the bundle existed
$ # Ctrl-C that server, re-run step 2 — what the corrected step 5 says to do
parlay-server: listening on http://127.0.0.1:4242 (… assets: /tmp/fm-fresh-clone/packages/client/dist)
$ curl -sS -o /dev/null -w '%{http_code}\n' http://localhost:4242/
200          ← the corrected success criterion; parlay-agent.js 200, /annotate/pulse-agent.js 200
$ # the other documented order, checked too: a server started with no bundle → 503; the same
$ # command started after the build → assets resolved to packages/client/dist, GET / → 200
```

Every step behaved as the (now corrected) README says. The `assets: dist` boot line is the
whole bug, and it is why the paragraph exists at step 2 as well as step 5. Cleanup: servers
killed by port, both temp directories removed, no server left listening (checked), the live
`~/.parlay` untouched (`find … -newermt '-3 hours'` → nothing) and no `demo.chan`-style spool
left in the host runtime dir (no listener was armed this iteration; the only touched file is the
shared relay's `.ensure-up.ok` freshness marker, a TTL cache).

### The preflight demonstrated failing, on a deliberately broken environment

```
$ env -i PATH=/nonexistent /bin/bash bin/parlay-preflight ; echo exit=$?
  FAIL  go            not on PATH — every parlay command builds the Go CLI on first run
        fix: install Go 1.26.5+ (https://go.dev/dl · macOS: brew install go · Debian/Ubuntu: apt-get install golang-go)
  FAIL  git           not on PATH — parlay spawn/guard/teardown all shell out to git
        fix: macOS: xcode-select --install · Debian/Ubuntu: apt-get install git
  warn  curl          not on PATH — the Quickstart's step 1 sandbox needs it; the server-and-CLI path does not
  warn  bun           not on PATH — only the optional chat panel (packages/client) needs it
  FAIL  HOME          unset — the CLI resolves ~/.parlay, ~/.claude/PAI and the agent store from $HOME
        fix: export HOME=/path/to/your/home, then re-run
  FAIL  CLI state     /nonexistent/.parlay is not writable — config.json, agents/ and skills live here
        fix: chmod u+wx "/nonexistent/.parlay" (or export PARLAY_STATE_HOME=/some/writable/dir)
12 checks: 6 ok, 2 warning(s), 4 failure(s).
Fix the 4 failure(s) above, then run this again — nothing else works until they are gone.
exit=1
```

`bin/parlay-preflight.test.sh` (250 lines, 20 assertions) is what makes the preflight a check
rather than a decoration: a healthy synthetic checkout (exit 0), the Go version floor as a real
comparison, a live instance answering `/health` (reported up, **not** told to start a server),
six simultaneous blockers named in **one** run with the summary's own count asserted equal to the
FAIL lines printed, and `env -i PATH=/nonexistent` against the real checkout. It is wired into
CI's hermetic shell job.

### Links, and claims checked against the code

```
$ bash /tmp/gate-step.sh        # the shipped hygiene step, extracted from .github/workflows/ci.yml
66 agent-notes are placed in docs/traps.md; 239 links across 5 documents resolve      exit=0
$ bash /tmp/docsindex-step.sh   # the other shipped hygiene step
all 30 top-level docs are indexed in docs/README.md                                   exit=0
```

Both `run:` blocks were pulled out of the YAML with a few lines of PyYAML rather than
retyped, so the block that was executed is byte-for-byte the block CI ships.

Claims were checked against the code, not against another document: `bootstrap-sandbox.sh`'s
redirects, its ten `run_check` calls and `LIMITS` block; `defaultAssetsDir()` /
`repoAssetsDir()` and `static.Handler`'s per-request stat in `packages/go-server`; `go-server`'s
`/health` body; `tools/cli/go.mod`'s `go 1.26.5` floor; the launchd label in
`packages/go-server/deploy/lib.sh` (`com.parlay.go-server`, not the retired
`com.parlay.chat-server` that `AGENTS.md` still named); `parlay-monitor.sh`'s upstream
comparison and `monitor.go`'s `PARLAY_SERVER=config.ServerURL()`; `relay_control.go`'s `/health`
handler with its pinned test; and the two hygiene steps, run as extracted from the YAML. The
`-assets-dir` claims in the new note and in `docs/command-server.md` were run, not inferred:
from `packages/go-server`, `-assets-dir ../client/dist` → `200`, `-assets-dir
packages/client/dist` → `503`.

### Line budget

The repo enforces 250 lines only for staged `*.ts` files (`tools/hooks/pre-commit`), so markdown
has no enforced budget; self-imposed anyway — every file added here is ≤ 250
(`bin/parlay-preflight` 249, its harness 250, `docs/traps.md` 246, both new notes ≤ 43,
`.gitignore` 74, this file). The gate is a step inside `ci.yml`, not a new script.

## Where this stands

- **The PR is open, not merged**: <https://github.com/trillium/parlay/pull/314>,
  `gnhf/objective-make-a-new-904428` → `main`. Remote head `35ec81b4` is gate-READY (checks green
  against the current base, a real CodeRabbit review covering that commit, no unresolved threads).
- **Two local commits are deliberately not pushed yet**: this file's revision, and the step-5
  correction above. Pushing either one alone re-pins the review to a commit that is no longer the
  head (`stale-review`, and CodeRabbit's free tier is roughly one review per hour), so the next
  iteration pushes the tip that contains both and spends one review window on it. Until then the
  PR body carries this document, including the correction.
- **The reviewed head carries the earlier work.** Iteration 8's gate hardening was pushed as
  `35ec81b4`, and CI run 37616868360 re-ran all four checks green *including the hardened step
  itself*: the Hygiene job printed `65 agent-notes are placed in docs/traps.md; 238 links across
  5 documents resolve`. All four review threads from the two rounds are resolved, each with an
  in-thread reply naming its fix and commit.
