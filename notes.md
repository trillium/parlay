# Onboarding work: what changed, and the evidence

This branch makes the first hour in this repo readable instead of source-bound. Six
pieces, in the order a newcomer meets them:

| Piece | What it is |
|---|---|
| `bin/parlay-preflight` (+ `bin/parlay-preflight.test.sh`) | One bash-only command that checks the whole prerequisite set and names **every** missing thing in one run with its exact `fix:` command. Step 0 of the README path, and the command to re-run when a machine misbehaves. |
| `docs/traps.md` | `AGENTS.md`'s incident record reordered into the nine stages a newcomer walks through. All 65 `docs/agent-notes/` notes appear, none dropped, none rewritten, every link target unchanged. |
| `README.md` Quickstart | One ordered five-step path with a real first command, an isolated-fleet step, one health surface named in both roles, and success/failure criteria on every step. |
| `.github/workflows/ci.yml` (new hygiene step) | Keeps the two lines above from rotting: every `docs/agent-notes/*.md` must be placed in the ordering, and every local link in the five documents of the path must resolve. |
| `docs/agent-notes/a-relay-that-cannot-name-its-server-passes.md` | A newly observed fact (below): the relay check that protects against cross-instance enrollment is inert against a relay older than its own `server` field. Linked from `traps.md` stage 4. |
| `.gitignore` | `.pi/` ignored, and the harness task-log files an auto-commit had picked up are untracked — so no harness scratch file can be committed again. |

## What a newcomer can now do without reading source

1. **Run `./bin/parlay-preflight`** and be told everything the machine needs at once, with
   the exact remedy for each, rather than one blocker per failed build.
2. **Run `./examples/bootstrap-sandbox.sh`** and watch the server, CLI, registry, history, the
   reply path and `doctor` work in a throwaway sandbox whose `LIMITS` line names what it does
   *not* cover (delivery and the relay); then steps 2–4 give a served message, a read-back, a
   health verdict, and three predicted non-zero exits (voice engine; `doctor`; `doctor deploy`).
3. **Know where to look when it breaks**: the same `./bin/parlay-preflight` an operator runs, then `docs/traps.md` at the stage they are in.

## What was reordered, or corrected, and why

- **`docs/traps.md` is new** (250 lines): `AGENTS.md` and `docs/agent-notes/` are in incident
  order — right to maintain, wrong to meet. It orders the same facts by when they bite:
  before you run anything → first server and message → an agent that receives → a second
  instance or fleet → when something looks wrong → spawning → changing the code → landing the
  change → only if you go deeper.
- **The README Quickstart gained a step and was renumbered** (step 1 is now the sandbox,
  server → 2, CLI → 3, talk/health → 4, panel → 5), every internal step reference updated,
  and every step now states what success and failure look like.
- **The `-state-dir` advice was corrected.** It read "fully isolated from live state", which
  overclaims: `-state-dir` moves the server's store and nothing else — `HOME`, the agent
  store, the TTS cache and the listener layer are untouched. It now says exactly that and
  points at the sandbox for a whole fleet.
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
  transcript, is `docs/agent-notes/a-relay-that-cannot-name-its-server-passes.md`. The
  stale-relay half needs a current relay build — a build decision, so documented, not patched.

## What now keeps the path from rotting

The docs-index gate scans `docs/*.md` only, so a note under `docs/agent-notes/` that no
document links is invisible to every gate — one existed (`bun-test-ts-gate-is-repo-wide.md`)
until `traps.md` was written — and a broken relative link is equally invisible, since GitHub
renders it as ordinary text. One new hygiene step closes both, deriving each side from the
filesystem rather than a list that goes stale, and resolving each link against the directory
of the file that contains it (done by hand once, resolving against the repo root reported 117
false failures). Scope is the newcomer path — `README.md`, `AGENTS.md`, `docs/README.md`,
`docs/traps.md`, `examples/README.md` — deliberately not all of `docs/`, which also holds
dated snapshots (`ux-eval-*`, `dogfood/`) whose value is being what was true that day.
Proved to fail, not just to pass. The run block was extracted from the workflow YAML (so the
tested text is the shipped text) and run against a faithful copy of the tree with one note
written and never placed and one link pointed at a file that does not exist:
```
$ bash /tmp/extracted-step.sh       # control, the real tree
65 agent-notes are placed in docs/traps.md; 238 links across 5 documents resolve   (exit 0)

$ bash /tmp/extracted-step.sh       # one unplaced note + one dangling link
::error file=docs/agent-notes/an-orphan-note.md::docs/agent-notes/an-orphan-note.md is not placed in docs/traps.md — put it at the stage where a newcomer meets it
::error::these links in the newcomer path do not resolve:
README.md -> docs/traps.md.bak      (×5; the link that was broken)
exit=1
```

## Harness scratch was committed once, and now cannot be

Nothing under `.pi/`, `.gnhf/` or any other harness scratch directory may be committed, and
it was already broken on this branch: the auto-commit that landed `docs/traps.md` captured
eight `.pi/tasks/<session>/*.json` + `*.output` files, because `.pi/` was not ignored. Fixed
earlier by untracking them (`git rm --cached`, files kept on disk) and adding `.pi/` to
`.gitignore` with the incident recorded next to the entry. `.gnhf/` needs no entry: the
harness already excludes `.gnhf/runs/` through `.git/info/exclude`. Files added in one commit
and removed in another do not appear in a PR's net diff, so the PR is clean without a rewrite.
## What was deliberately left alone

- `AGENTS.md`, `docs/agent-notes/*` and every existing incident record: no fact was changed;
  `AGENTS.md` gained the preflight-as-first-command paragraph and the new CI gate in its CI line.
- `examples/bootstrap-sandbox.sh`: reused as-is, not reimplemented — it already encodes the
  isolation recipe, including the hardcoded paths.
- Deployment scripts, `internal/guard.GuardedPaths`, and every public endpoint shape.
- `tools/monitor/parlay-monitor.sh`: its tolerance for a relay that cannot report its
  upstream is deliberate and documented in the code; changing it is a product decision, not an
  onboarding one, so it is documented and linked instead.
- `docs/ux-eval-2026-08-30.md`: a dated field report citing the README's old step numbers;
  rewriting a historical record to match today's numbering would falsify evidence.

## Verification

### The configured stop condition, literally, at the repo root

```
$ go build ./... && go vet ./... && gofmt -l . | (! grep .) && go test ./... && make test-bdd
pattern ./...: directory prefix . does not contain main module or its selected dependencies
exit=1
$ for m in tools/cli tools/relay packages/go-server packages/spawn-profiles; do   # same chain, per module
  (cd $m && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...); done
tools/cli exit=0 · tools/relay exit=0 · packages/go-server exit=0 · packages/spawn-profiles exit=0
$ gofmt -l .            → (no output, exit 0)      $ make test-bdd → exit 0, 7+7 scenarios passed
$ bash bin/parlay-preflight.test.sh → 20 passed, 0 failed, exit 0
```

The repo is four Go modules with no root module, so `./...` at the root cannot resolve.
Pre-existing, unrelated to this branch, and **not gamed**: a vacuous root module would exit 0
while testing nothing, and a root `go.work` does not fix it either (verified here, then
removed). The per-module equivalent plus the rest of the chain is at the end of this section;
the baseline before any change was identical, with no known-red on this box, including
`make test-bdd`.

### The newcomer path, executed end to end (steps 0–5, clean `HOME`)

Run literally as the README writes it, `HOME` at an empty directory, Go caches pinned at the
real ones. This box has a live install; `find ~/.parlay -newermt -30 minutes` afterwards →
nothing. (The sandbox's ten PASS lines are re-wrapped here, never reworded.)

```
$ ./bin/parlay-preflight
  ok    HOME /tmp/parlay-newcomer.cgGbdT · CLI state …/.parlay (writable) · server port 127.0.0.1:4242 free
  ok    isolation …/.parlay is not the live default state home, or holds no agents
12 checks: 12 ok, 0 warning(s), 0 failure(s).        exit=0
$ ./examples/bootstrap-sandbox.sh
2026/10/07 02:59:04 parlay-server: listening on http://127.0.0.1:61534 (state dir: …, assets: …)
sent to helm — id m1
  PASS  registry served the seeded agents · message round-tripped through the server · message
        persisted to the state dir's messages.jsonl · reply path routed --agent helm onto the helm channel
  PASS  server URL resolved from config.json · identity.md read back with frontmatter stripped
  PASS  launch spec discovered for both agents, both reported ghost (registered, no listener)
  PASS  seeded history served on both channels
  PASS  doctor PASSes identity, registry membership, reachability · server persisted only into
        the state dir it was given
  LIMITS of this run — what the PASSes above do not say: UNCOVERED (delivery, the relay), BOUND (the unauthenticated port while up)
all checks passed — port 61534, sandbox /var/folders/…/T/parlay-example.lLK8hV      exit=0
$ cd packages/go-server && go run ./cmd/parlay-server        # step 2
2026/10/07 03:00:17 parlay-server: listening on http://127.0.0.1:4242 (state dir: /tmp/parlay-newcomer.cgGbdT/.parlay, assets: dist)
$ curl -sS -i http://127.0.0.1:4242/health
HTTP/1.1 200 OK
{"agents":0,"messages":0,"ok":true}
$ ./bin/parlay remote                                        # step 3
http://localhost:4242 (source: default)
$ ./bin/parlay send --demo --force "hello"                   # step 4
sent to demo — id m0
$ ./bin/parlay history 5
[10:00:18] you          hello
$ ./bin/parlay health ; echo exit=$?
ok    server http://localhost:4242 — 0 client(s), 0 poller(s), 0 agent(s)
FAIL  eval-engine http://127.0.0.1:4343 — connect: connection refused   # optional engine, not your install
exit=1
$ ./bin/parlay doctor ; echo exit=$?
FAIL  PARLAY_AGENT_ID is not set
PASS  server reachable at http://localhost:4242
exit=1
$ ./bin/parlay doctor deploy ; echo exit=$?
UNKNOWN no com.parlay.* LaunchAgents installed — nothing to inventory
FAIL  eval-engine 127.0.0.1:4343 — port connection refused through the full deadline (service down or wedged)
exit=1                                # now stated in the README
$ curl -sS -i http://localhost:4242/ | head -1               # step 5, before building the bundle
HTTP/1.1 503 Service Unavailable
```

Every required step behaved as the README says. The follow-on commands were run too —
`reply --agent demo` (`said as demo (id m1)`), `listen --agent demo --legacy-poll` (registers,
announces, arms the native poller), and `monitor --agent demo` (the relay path, the one place
the path's own text was wrong). Cleanup: the server was killed by port, the `demo` spool this
run created in the host-wide runtime dir was removed, the temp `HOME` and sandbox were
deleted, and no agent was left registered anywhere. Live-install residue, recorded rather
than hidden: the shared relay's `.ensure-up.ok` freshness marker was refreshed (a TTL cache).

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
Fix the 4 failure(s) above, then run this again — nothing else works until they are gone
exit=1
```

`bin/parlay-preflight.test.sh` (250 lines, 20 assertions) is what makes the preflight a check
rather than a decoration: a healthy synthetic checkout (exit 0), the Go version floor as a
real comparison, a live instance answering `/health` (reported up, **not** told to start a
server), six simultaneous blockers named in **one** run with the summary's own count asserted
equal to the FAIL lines printed, and `env -i PATH=/nonexistent` against the real checkout. It
is wired into CI's hermetic shell job.

### Links, and claims checked against the code

```
README.md 33 local · AGENTS.md 74 · docs/README.md 40 · docs/traps.md 76 · examples/README.md 2
TOTAL: 238 links scanned, 225 local, 0 broken (resolved against each file's dir)
CI docs-index gate locally: scanned=30 fail=0
```

Claims were checked against the code, not against another document: `bootstrap-sandbox.sh`'s
redirects, its ten `run_check` calls, `--keep` and `LIMITS` block; `go-server`'s `/health`
body; `tools/cli/go.mod`'s `go 1.26.5` floor; the launchd label in
`packages/go-server/deploy/lib.sh` (`com.parlay.go-server`, not the retired
`com.parlay.chat-server` that `AGENTS.md` still named — corrected earlier on this branch);
`parlay-monitor.sh`'s upstream comparison and `monitor.go`'s `PARLAY_SERVER=config.ServerURL()`;
`relay_control.go`'s `/health` handler with its pinned test; and the new hygiene step's YAML,
parsed and run as extracted. The step 2/3/5 criteria were run too: a held port prints
`listen failed: … address already in use`, `parlay remote` prints `source: default`, and
`GET /` answers `503` with the `bun run build` body (`404` with an empty `-assets-dir`).
### Line budget

The repo enforces a 250-line limit only for staged `*.ts` files (`tools/hooks/pre-commit`),
so markdown has no enforced budget. Self-imposed anyway: every file added or touched on this
branch is ≤ 250 lines (`bin/parlay-preflight` 249, its harness 250, `docs/traps.md` 250, the
new note 38, `.gitignore` 74, this file). The new gate is a step inside
`.github/workflows/ci.yml` rather than a new script, because the job it extends already has
the checkout and needs no toolchain.

## Where this stands

- **The PR is open, not merged**: <https://github.com/trillium/parlay/pull/314>,
  `gnhf/objective-make-a-new-904428` → `main`; the branch is pushed to `c50a1fe` and the PR
  body re-written from this file. Net diff: the ten files above, plus `notes.md`.
- **The branch was brought level with `main`** (`git merge origin/main`), clearing
  `parlay merge-gate 314`'s `behind-base` finding — a real blocker, since a behind branch's
  checks ran against an older merge and GitHub does not re-run them when the base moves.
  CodeRabbit review was requested, and re-requested after this push (`gh-axi pr comment 314
  --body "@coderabbitai review"`); on this repo the bot never runs on its own, so that comment
  is the only route to gate-visible review evidence. Its one finding — the sandbox coverage
  claim overstated what `./examples/bootstrap-sandbox.sh` tests — is fixed here.
- **The harness commits after the turn and never pushes**, so the tip a turn can push is
  always one commit behind what it just wrote: the newest edits need one more push.
  `parlay merge-gate 314` is otherwise clean and blocks only on that one review thread.
- **The README below the Quickstart is still reference material** (system map, layout, worked
  config, development, publishing): reachable and accurate, so left in place — the task was to
  add a path, not to prune the manual.
