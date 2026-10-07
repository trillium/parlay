# Onboarding work: what changed, and the evidence

This branch makes the first hour in this repo readable instead of source-bound. Four
pieces, in the order a newcomer meets them:

| Piece | What it is |
|---|---|
| `bin/parlay-preflight` (+ `bin/parlay-preflight.test.sh`) | One bash-only command that checks the whole prerequisite set and names **every** missing thing in one run with its exact `fix:` command. Step 0 of the README path, and the command to re-run when a machine misbehaves. |
| `docs/traps.md` | `AGENTS.md`'s incident record reordered into the nine stages a newcomer walks through. All 64 `docs/agent-notes/` notes appear, none dropped, none rewritten, every link target unchanged. |
| `README.md` Quickstart | One ordered five-step path with a real first command, an isolated-fleet step, one health surface named in both roles, and success/failure criteria on every step. |
| `.gitignore` | `.pi/` ignored, and the harness task-log files an auto-commit had picked up are untracked — so no harness scratch file can be committed again. |

## What a newcomer can now do without reading source

1. **Run `./bin/parlay-preflight`** and be told everything the machine needs at once, with the exact remedy for each, rather than one blocker per failed build.
2. **Run `./examples/bootstrap-sandbox.sh`** and watch the whole stack work — server, CLI, registry, history, the reply path, `doctor` — in a throwaway sandbox, with a `LIMITS` block saying what it did *not* prove.
3. **Start a real server, point the CLI at it, send a message, read it back**, and know that `./bin/parlay health` exits 1 for the optional voice engine, not for a broken install.
4. **Know where to look when it breaks**: the same `./bin/parlay-preflight` an operator runs, then `docs/traps.md` at the stage they are in.

## What was reordered, and why

- **`docs/traps.md` is new** (247 lines). `AGENTS.md` and `docs/agent-notes/` are in incident order, which is right to maintain and wrong to meet; `traps.md` orders the same facts by when they bite: before you run anything → first server and message → an agent that receives → a second instance or fleet → when something looks wrong → spawning → changing the code → landing the change → only if you go deeper.
- **The README Quickstart gained a step and was renumbered** (step 1 is now the sandbox, server → 2, CLI → 3, talk/health → 4, panel → 5), every internal step reference updated, and every step now states what success and failure look like.
- **The `-state-dir` advice was corrected.** It previously read "To keep a dev run fully isolated from live state", which overclaims: `-state-dir` moves the server's store and nothing else — `HOME`, the agent store, the TTS cache and the listener layer are untouched. The step now says exactly that and points at the sandbox for a whole fleet.
- **The health surface is one surface, named in both roles.** `./bin/parlay-preflight` is the machine half, `./bin/parlay health` the running-instance half, and the README says so in step 0, step 4 and "when a step fails". The preflight is state-aware: with a parlay instance already answering on the resolved port it prints `Ready. A parlay instance already answers on 127.0.0.1:4242 — nothing to start.` instead of telling you to start a server you already have.

## Harness scratch was committed once, and now cannot be

The constraint is that nothing under `.pi/`, `.gnhf/` or any other harness scratch directory is ever committed. It was already broken on this branch: the auto-commit that landed `docs/traps.md` also captured four `.pi/tasks/<session>/*.json` + `*.output` task-log files, because `.pi/` was not ignored and nothing else kept them out. This iteration untracked them (`git rm --cached`, files kept on disk) and added `.pi/` to `.gitignore` with the incident recorded next to the entry. `.gnhf/` needs no such entry here: the harness already excludes `.gnhf/runs/` through `.git/info/exclude`.

## What was deliberately left alone

- `AGENTS.md`, `docs/agent-notes/*`, and every existing incident record: no fact was changed. `AGENTS.md` gained one paragraph naming the preflight as the first command.
- `examples/bootstrap-sandbox.sh` itself: reused as-is, not reimplemented — it already encodes the isolation recipe, including the hardcoded paths.
- Deployment scripts, `internal/guard.GuardedPaths`, and every public endpoint shape.
- `docs/ux-eval-2026-08-30.md`: a dated field report that cites the README's old step numbers. Rewriting a historical record to match today's numbering would falsify evidence.
- A root `go.work`: not added. It would make repo-root-relative module paths work, but it contradicts the README, `AGENTS.md` and CI doctrine that all state there is no root workspace, and adds a `go.work.sum` to maintain. A deliberate decision, not an oversight.

## Verification

### The configured stop condition, literally, at the repo root

```
$ go build ./... ; echo exit=$?
pattern ./...: directory prefix . does not contain main module or its selected dependencies
exit=1
```

This repo is four Go modules with no root module, so `./...` at the root cannot resolve. That is pre-existing, unrelated to this branch, and **not gamed**: a vacuous root module would make the command exit 0 while proving nothing. The per-module equivalent, plus the rest of the chain, is what was actually run:

```
$ for m in tools/cli packages/go-server tools/relay packages/spawn-profiles; do (cd $m && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...); echo "$m exit=$?"; done
tools/cli exit=0
packages/go-server exit=0
tools/relay exit=0
packages/spawn-profiles exit=0
$ gofmt -l .
(no output)
$ make test-bdd ; echo exit=$?
... 7 scenarios (7 passed), 21 steps (21 passed) ... PASS
exit=0
```

Baseline before any change: identical — all four modules exit 0, `gofmt -l .` empty,
`make test-bdd` exit 0. No known-red on this box.

Re-run after merging `main` (two docs-only commits): identical, plus
`bin/parlay-preflight.test.sh` 20 passed / 0 failed and 223 local links / 0 broken.

### The newcomer path, executed end to end

```
$ ./bin/parlay-preflight
parlay-preflight — can this machine and checkout run parlay?
  ok    checkout      .../objective-make-a-new-904428 — 4 Go modules, bin/parlay present
  ok    go            go1.26.5 (tools/cli requires 1.26.5)
  ok    CLI state     /Users/mini1/.parlay (PARLAY_STATE_HOME: writable)
  ok    server port   127.0.0.1:4242 is free (this is where the CLI expects the server)
  warn  isolation     /Users/mini1/.parlay/agents holds 7 agent dir(s) — this is a live install, not a scratch one
        fix: to experiment without touching it: examples/bootstrap-sandbox.sh (redirects HOME, PARLAY_STATE_HOME, PARLAY_AGENT_HOME and PAI_DIR into a temp dir)
12 checks: 11 ok, 1 warning(s), 0 failure(s).
$ echo $?
0

$ ./examples/bootstrap-sandbox.sh
== starting packages/go-server on http://127.0.0.1:50734
2026/10/07 02:14:57 parlay-server: listening on http://127.0.0.1:50734 (state dir: .../data, assets: .../assets)
sent to helm — id m1
== parlay history — read it back
[09:14:58] you          hello from bootstrap-sandbox.sh
== checks
  PASS  registry served the seeded agents
  PASS  message round-tripped through the server
  PASS  message persisted to the state dir's messages.jsonl
  PASS  reply path routed --agent helm onto the helm channel
  PASS  server URL resolved from config.json
  PASS  identity.md read back with frontmatter stripped
  PASS  launch spec discovered for both agents, both reported ghost (registered, no listener)
  PASS  seeded history served on both channels
  PASS  doctor PASSes identity, registry membership, reachability
  PASS  server persisted only into the state dir it was given
  LIMITS of this run — what the PASSes above do not say: ... (UNCOVERED: delivery and the relay; BOUND: the unauthenticated port while it was up)
all checks passed — port 50734, sandbox .../parlay-example.hadLNR
$ echo $?
0

$ (cd packages/go-server && go build -o $TMP/parlay-server ./cmd/parlay-server)
$ (exec env HOME=$TMP $TMP/parlay-server -addr 127.0.0.1:4242 -state-dir $TMP/data -pai-dir $TMP/pai -assets-dir $TMP/assets) &
parlay-server: listening on http://127.0.0.1:4242 (state dir: $TMP/data, assets: $TMP/assets)
$ ./bin/parlay
parlay @ http://localhost:4242
subscribers: 0 panel client(s), 0 poller(s)
agents: 0 registered
messages: 0 messages
$ ./bin/parlay send --demo --force "hello"
sent to demo — id m0
$ ./bin/parlay history 5
[09:19:10] you          hello
$ ./bin/parlay health ; echo exit=$?
ok    server http://localhost:4242 — 0 client(s), 0 poller(s), 0 agent(s)
FAIL  eval-engine http://127.0.0.1:4343 — connect: connection refused
      the voice engine is OPTIONAL for the CLI + server ... This line is about the engine, not about your install.
exit=1
$ ./bin/parlay doctor ; echo exit=$?
FAIL  PARLAY_AGENT_ID is not set
PASS  server reachable at http://localhost:4242
exit=1
$ curl -fsS http://127.0.0.1:4242/health
{"agents":0,"messages":1,"ok":true}
```

Both non-zero exits are the two the README now predicts in advance (the optional voice
engine, and `doctor`'s agent-only self-check).

### The preflight demonstrated failing, on a deliberately broken environment

```
$ env -i PATH=/nonexistent /bin/bash bin/parlay-preflight ; echo exit=$?
parlay-preflight — can this machine and checkout run parlay?
  ok    checkout      .../objective-make-a-new-904428 — 4 Go modules, bin/parlay present
  FAIL  go            not on PATH — every parlay command builds the Go CLI on first run
        fix: install Go 1.26.5+ (https://go.dev/dl · macOS: brew install go · Debian/Ubuntu: apt-get install golang-go)
  FAIL  git           not on PATH — parlay spawn/guard/teardown all shell out to git
        fix: macOS: xcode-select --install · Debian/Ubuntu: apt-get install git
  warn  curl          not on PATH — the Quickstart's step 1 sandbox (examples/bootstrap-sandbox.sh) needs it; the server-and-CLI path does not
        fix: macOS: preinstalled at /usr/bin/curl · Debian/Ubuntu: apt-get install curl
  warn  bun           not on PATH — only the optional chat panel (packages/client) needs it
  FAIL  HOME          unset — the CLI resolves ~/.parlay, ~/.claude/PAI and the agent store from $HOME
        fix: export HOME=/path/to/your/home, then re-run
  FAIL  CLI state     /nonexistent/.parlay is not writable — config.json, agents/ and skills live here
        fix: chmod u+wx "/nonexistent/.parlay" (or export PARLAY_STATE_HOME=/some/writable/dir)
  ok    build dir     .../tools/cli/bin (bin/parlay builds the CLI binary here)
  ok    relay runtime /tmp/parlay (PARLAY_RELAY_RUNTIME)
  ok    server port   127.0.0.1:4242 is free (this is where the CLI expects the server)
  ok    engine port   127.0.0.1:4343 is free (optional: parlay eval serve)
  ok    isolation     no state home to check (HOME and PARLAY_STATE_HOME are both unset)
12 checks: 6 ok, 2 warning(s), 4 failure(s).
Fix the 4 failure(s) above, then run this again — nothing else works until they are gone.
exit=1

$ bash bin/parlay-preflight.test.sh
... 20 passed, 0 failed.
```

The harness (`bin/parlay-preflight.test.sh`, 250 lines) is what makes the preflight a
check rather than a decoration. It runs four cases against synthetic checkouts: a healthy
one (exit 0, zero failures, and it states what to do next); the version floor as a real
comparison (`GOTOOLCHAIN=local` below the floor is a named FAIL, the same Go with `auto`
is a non-blocking WARN); a live parlay instance answering `/health` (exit 0, reported up,
and **not** told to start a server); six simultaneous blockers in one deliberately broken
environment (exactly six FAILs named in one run, each with its `fix:`, plus the summary's
own count asserted equal to the FAIL lines printed); and `env -i PATH=/nonexistent`
against the real checkout. It is wired into CI's hermetic shell job.

### Links, and claims checked against the code

```
README.md: 39 links, 32 local checked, 0 broken
docs/traps.md: 75 links, 75 local checked, 0 broken
docs/README.md: 44 links, 40 local checked, 0 broken
examples/README.md: 4 links, 2 local checked, 0 broken
AGENTS.md: 74 links, 74 local checked, 0 broken
link check: PASS (223 local links, 0 broken)

$ (CI docs-index gate, run locally)  scanned=30 fail=0   → PASS
```

Claims in the new path were checked against the code, not against another document:
`bootstrap-sandbox.sh`'s redirects, its ten `run_check` calls, its `--keep` flag and its
`LIMITS` block; `go-server`'s `/health` body (`{"agents":…,"messages":…,"ok":true}`);
`tools/cli/go.mod`'s `go 1.26.5` floor; and the launchd label in
`packages/go-server/deploy/lib.sh` (`com.parlay.go-server`, not the retired
`com.parlay.chat-server` that `AGENTS.md` still named — corrected in an earlier iteration
of this branch). The success/failure criteria added to steps 2, 3 and 5 were each run:

```
$ parlay-server -addr 127.0.0.1:4242 ...            # step 2, success
parlay-server: listening on http://127.0.0.1:4242 (state dir: …, assets: …)
$ parlay-server -addr 127.0.0.1:4242 ...            # step 2, second server on a held port
parlay-server: listen failed: listen tcp 127.0.0.1:4242: bind: address already in use
$ ./bin/parlay remote                               # step 3
http://localhost:4242 (source: default)
$ curl -sS -i http://127.0.0.1:4243/ | head -1      # step 5, no -assets-dir, dist missing
HTTP/1.1 503 Service Unavailable
$ curl -sS -i http://127.0.0.1:4244/ | head -1      # step 5, -assets-dir at an empty dir
HTTP/1.1 404 Not Found
```

(The 404 body still names the build command and the `bun run build` line. The README's
`503` claim is about the documented path, where no `-assets-dir` is passed.)

### Line budget

The repo enforces a 250-line limit only for staged `*.ts` files (`tools/hooks/pre-commit`),
so markdown has no enforced budget. Self-imposed anyway: every file added or touched on
this branch is ≤ 250 lines (`bin/parlay-preflight` 249, `bin/parlay-preflight.test.sh`
250, `docs/traps.md` 247, `.gitignore` 74, this file 236).

## Left undone, with reasons

- **The branch is pushed and the PR is open, not merged**: <https://github.com/trillium/parlay/pull/314>,
  `gnhf/objective-make-a-new-904428` → `main`, created with
  `gh-axi pr create --base main --head gnhf/objective-make-a-new-904428 --body-file .pi/pr-body.md`.
  Its diff is exactly the nine files listed at the top of this write-up; the `.pi/`
  scratch files were added in one commit and removed in another, so they do not appear
  in the pull request's net diff at all (verified with `gh pr diff 314 --name-only`).
- **The branch was brought level with `main`** (`git merge origin/main`, two docs-only
  commits: the `jsonExemptPaths` identifier correction in `AGENTS.md` and the round-2/3
  vision answers). This cleared `parlay merge-gate 314`'s `behind-base` finding, which
  is otherwise a real blocker — a behind branch's checks ran against an older merge and
  GitHub does not re-run them when the base moves. The merge was clean and both intents
  survive in `AGENTS.md` (the preflight paragraph at line 7, `jsonExemptPaths` at 44).
- **CodeRabbit review was requested** (`gh-axi pr comment 314 --body "@coderabbitai
  review"`) — on this repo the bot never runs on its own, so that comment is the only
  route to gate-visible review evidence. All four CI checks then reported `pass`;
  `parlay merge-gate 314` was `BLOCKED (1)`, its only item the review still running,
  and the gate advises not to push while a review is in flight (a push restarts it).
- **One more push is needed to carry this revision of `notes.md`.** The harness commits
  the working tree after the turn ends, so the pushed tip (`7d65d2d`) does not yet
  contain the edits written above it; the next iteration pushes them and re-reads the
  gate.
- **`docs/traps.md` completeness is not gated.** The CI docs-index gate covers `docs/*.md`
  only, so a future `docs/agent-notes/*.md` that no document links is invisible to every
  gate. `traps.md` is currently the only thing that makes all 64 reachable; a gate for
  that would be a new CI check, which this task was not asked to add.
- **The README below the Quickstart is still reference material** (system map, layout,
  worked config, development, publishing). It was left in place rather than moved: it is
  reachable, accurate, and the task was to add a path, not to prune the manual.
- **A root `go.work`** — see "what was deliberately left alone".
