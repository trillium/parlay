# Onboarding work: what changed, and the evidence

This branch makes the first hour in this repo readable instead of source-bound. Five
pieces, in the order a newcomer meets them:

| Piece | What it is |
|---|---|
| `bin/parlay-preflight` (+ `bin/parlay-preflight.test.sh`) | One bash-only command that checks the whole prerequisite set and names **every** missing thing in one run with its exact `fix:` command. Step 0 of the README path, and the command to re-run when a machine misbehaves. |
| `docs/traps.md` | `AGENTS.md`'s incident record reordered into the nine stages a newcomer walks through. All 64 `docs/agent-notes/` notes appear, none dropped, none rewritten, every link target unchanged. |
| `README.md` Quickstart | One ordered five-step path with a real first command, an isolated-fleet step, one health surface named in both roles, and success/failure criteria on every step. |
| `docs/agent-notes/a-relay-that-cannot-name-its-server-passes.md` | A newly observed fact (below): the relay preflight that protects against cross-instance enrollment is inert against a relay older than its own `server` field. Linked from `traps.md` stage 4. |
| `.gitignore` | `.pi/` ignored, and the harness task-log files an auto-commit had picked up are untracked — so no harness scratch file can be committed again. |

## What a newcomer can now do without reading source

1. **Run `./bin/parlay-preflight`** and be told everything the machine needs at once, with the exact remedy for each, rather than one blocker per failed build.
2. **Run `./examples/bootstrap-sandbox.sh`** and watch the whole stack work — server, CLI, registry, history, the reply path, `doctor` — in a throwaway sandbox, with a `LIMITS` block saying what it did *not* prove; then follow steps 2–4 literally and get a served message, a read-back, a health verdict and two predicted non-zero exits (the optional voice engine; `doctor`'s agent-only self-check).
3. **Know where to look when it breaks**: the same `./bin/parlay-preflight` an operator runs, then `docs/traps.md` at the stage they are in.

## What was reordered, or corrected, and why

- **`docs/traps.md` is new** (250 lines). `AGENTS.md` and `docs/agent-notes/` are in incident order, which is right to maintain and wrong to meet; `traps.md` orders the same facts by when they bite: before you run anything → first server and message → an agent that receives → a second instance or fleet → when something looks wrong → spawning → changing the code → landing the change → only if you go deeper.
- **The README Quickstart gained a step and was renumbered** (step 1 is now the sandbox, server → 2, CLI → 3, talk/health → 4, panel → 5), every internal step reference updated, and every step now states what success and failure look like.
- **The `-state-dir` advice was corrected.** It previously read "To keep a dev run fully isolated from live state", which overclaims: `-state-dir` moves the server's store and nothing else — `HOME`, the agent store, the TTS cache and the listener layer are untouched. The step now says exactly that and points at the sandbox for a whole fleet.
- **The health surface is one surface, named in both roles.** `./bin/parlay-preflight` is the machine half, `./bin/parlay health` the running-instance half, and the README says so in step 0, step 4 and "when a step fails". The preflight is state-aware: with a parlay instance already answering on the resolved port it prints `Ready. A parlay instance already answers on 127.0.0.1:4242 — nothing to start.`
- **The relay follow-on paragraph was corrected, and `doctor deploy`'s fresh-clone behaviour is now stated** — see the next section; executing the path proved the first paragraph's absolute claim false on any machine that already runs a relay, and showed that `doctor deploy` exits 1 on a fresh clone for the same not-installed voice engine `health` reports.

## What executing the path found: a relay that cannot name its server

Running the README's own follow-on commands in a clean `HOME` produced a healthy-looking
enroll against the wrong upstream:

```
$ ./bin/parlay monitor --agent demo            # CLI resolved http://localhost:4242
parlay-monitor: preflight OK — canonical relay is up for 'demo'
parlay-monitor: streaming 'demo' from …/T/parlay/demo.chan
```

`tools/monitor/parlay-monitor.sh` refuses a cross-instance enroll by comparing the CLI's
server against the one the relay reports in `GET /health` over `relay.sock`;
`tools/cli/internal/monitor/monitor.go` passes `PARLAY_SERVER=config.ServerURL()` into that
script, so the CLI's half is always present. The other half was missing: this host's
canonical relay (a September build, started with `-server http://macbook:31337`) answers
`{"ok":true}` and nothing else, and an absent answer is deliberately *not* a mismatch —
refusing on unknown would break every relay older than the field. So the check was skipped,
the enroll went through the host-wide relay, and the upstream it polls was refusing
connections. The `and polling <url>` suffix the script appends once it *has* verified an
upstream is the visible difference; the README now says to read that line and gives the two
remedies (`--legacy-poll`, or the instance's own `PARLAY_RELAY_RUNTIME`). The stale-relay
half needs a current relay build — a build decision, not a document one — so it is recorded
in the new note rather than patched around.

## Harness scratch was committed once, and now cannot be

The constraint is that nothing under `.pi/`, `.gnhf/` or any other harness scratch directory is ever committed. It was already broken on this branch: the auto-commit that landed `docs/traps.md` also captured eight `.pi/tasks/<session>/*.json` + `*.output` task-log files, because `.pi/` was not ignored and nothing else kept them out. This was fixed in an earlier iteration by untracking them (`git rm --cached`, files kept on disk) and adding `.pi/` to `.gitignore` with the incident recorded next to the entry. `.gnhf/` needs no such entry here: the harness already excludes `.gnhf/runs/` through `.git/info/exclude`.

## What was deliberately left alone

- `AGENTS.md`, `docs/agent-notes/*` and every existing incident record: no fact was changed. `AGENTS.md` gained one paragraph naming the preflight as the first command.
- `examples/bootstrap-sandbox.sh` itself: reused as-is, not reimplemented — it already encodes the isolation recipe, including the hardcoded paths.
- Deployment scripts, `internal/guard.GuardedPaths`, and every public endpoint shape.
- `tools/monitor/parlay-monitor.sh`: its tolerance for a relay that cannot report its upstream is deliberate and documented in the code; changing it is a product decision, not an onboarding one. It is documented and linked instead.
- `docs/ux-eval-2026-08-30.md`: a dated field report that cites the README's old step numbers. Rewriting a historical record to match today's numbering would falsify evidence.
- A root `go.work`: not added. It would make repo-root-relative module paths work, but it contradicts the README, `AGENTS.md` and CI doctrine that all state there is no root workspace, and adds a `go.work.sum` to maintain.

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
tools/cli exit=0 · packages/go-server exit=0 · tools/relay exit=0 · packages/spawn-profiles exit=0
$ gofmt -l .        → (no output)
$ make test-bdd ; echo exit=$?
... 7 scenarios (7 passed), 21 steps (21 passed) ... PASS    exit=0
```

Baseline before any change: identical — all four modules exit 0, `gofmt -l .` empty,
`make test-bdd` exit 0. No known-red on this box.

### The newcomer path, executed end to end (steps 0–5, clean `HOME`)

Run literally as the README writes it, with `HOME` at an empty directory and the Go caches
pinned at the real ones (a fresh *parlay* state, not a fresh module download). This box has
a live install; `find ~/.parlay -newermt -30 minutes` afterwards → nothing.

```
$ ./bin/parlay-preflight
  ok    HOME          /tmp/parlay-newcomer.cgGbdT
  ok    CLI state     /tmp/parlay-newcomer.cgGbdT/.parlay (PARLAY_STATE_HOME: writable)
  ok    server port   127.0.0.1:4242 is free (this is where the CLI expects the server)
  ok    isolation     /tmp/parlay-newcomer.cgGbdT/.parlay is not the live default state home, or holds no agents
12 checks: 12 ok, 0 warning(s), 0 failure(s).
exit=0

$ ./examples/bootstrap-sandbox.sh
== starting packages/go-server on http://127.0.0.1:61534
2026/10/07 02:59:04 parlay-server: listening on http://127.0.0.1:61534 (state dir: …, assets: …)
sent to helm — id m1
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
  LIMITS of this run — what the PASSes above do not say: UNCOVERED (delivery, the relay), BOUND (the unauthenticated port while up)
all checks passed — port 61534, sandbox /var/folders/…/T/parlay-example.lLK8hV
exit=0                       # and the sandbox was removed on exit

$ cd packages/go-server && go run ./cmd/parlay-server        # step 2
2026/10/07 03:00:17 parlay-server: listening on http://127.0.0.1:4242 (state dir: /tmp/parlay-newcomer.cgGbdT/.parlay, assets: dist)
$ curl -sS -i http://127.0.0.1:4242/health
HTTP/1.1 200 OK
{"agents":0,"messages":0,"ok":true}

$ ./bin/parlay remote                                        # step 3
http://localhost:4242 (source: default)
$ ./bin/parlay                                               # step 4
agents: 0 registered
$ ./bin/parlay send --demo --force "hello"
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
$ ./bin/parlay doctor --json          # schema parlay.doctor/v1, verdict FAIL, 2 pass / 3 warn / 1 fail / 1 unknown
$ ./bin/parlay doctor deploy ; echo exit=$?
UNKNOWN no com.parlay.* LaunchAgents installed — nothing to inventory
FAIL  eval-engine 127.0.0.1:4343 — port connection refused through the full deadline (service down or wedged)
exit=1                                # now stated in the README

$ curl -sS -i http://localhost:4242/ | head -1               # step 5, before building the bundle
HTTP/1.1 503 Service Unavailable

$ ./bin/parlay reply --agent demo "on it"                    # follow-ons
said as demo (id m1)
$ ./bin/parlay listen --agent demo --name Demo --legacy-poll # native path, no relay
parlay listen: registering 'demo' … / announced — arming monitor …
parlay monitor (legacy poll) — server http://localhost:4242 channel demo
$ ./bin/parlay monitor --agent demo                          # the relay path — see above
parlay-monitor: preflight OK — canonical relay is up for 'demo'
```

Every step behaved as the README says except the relay-path one, which is what led to the
correction above. Cleanup: the server was killed by port, the `demo` spool this run created
in the host-wide runtime dir was removed, the temp `HOME` and sandbox were deleted. Live
install residue, recorded rather than hidden: the shared relay's `.ensure-up.ok` freshness
marker was refreshed (a TTL cache, harmless); nothing was registered anywhere, because the
relay's upstream is not running.

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

`bin/parlay-preflight.test.sh` (250 lines, 20 assertions) is what makes the preflight a
check rather than a decoration: a healthy synthetic checkout (exit 0), the Go version floor
as a real comparison, a live instance answering `/health` (reported up, **not** told to
start a server), six simultaneous blockers named in **one** run with the summary's own count
asserted equal to the FAIL lines printed, and `env -i PATH=/nonexistent` against the real
checkout. It is wired into CI's hermetic shell job.

### Links, and claims checked against the code

```
README.md 33 local · docs/traps.md 76 · docs/README.md 40 · examples/README.md 2 · AGENTS.md 74
TOTAL: 225 links, 225 local, 0 broken   (resolved against each file's dir; CI docs-index gate locally: scanned=30 fail=0)
```

Claims were checked against the code, not against another document: `bootstrap-sandbox.sh`'s
redirects, ten `run_check` calls, `--keep` and `LIMITS` block; `go-server`'s `/health` body;
`tools/cli/go.mod`'s `go 1.26.5` floor; the launchd label in
`packages/go-server/deploy/lib.sh` (`com.parlay.go-server`, not the retired
`com.parlay.chat-server` that `AGENTS.md` still named — corrected earlier on this branch);
`tools/monitor/parlay-monitor.sh`'s upstream comparison and
`tools/cli/internal/monitor/monitor.go`'s `PARLAY_SERVER=config.ServerURL()`, both quoted in
the new note; and the relay's `/health` handler in `tools/relay/relay_control.go` with its
pinned test. The step 2/3/5 criteria were run too: a held port prints `listen failed: …
address already in use`, `parlay remote` prints `source: default`, and `GET /` answers `503`
with the `bun run build` body (`404` when `-assets-dir` points at an empty directory).

### Line budget

The repo enforces a 250-line limit only for staged `*.ts` files (`tools/hooks/pre-commit`),
so markdown has no enforced budget. Self-imposed anyway: every file added or touched on
this branch is ≤ 250 lines (`bin/parlay-preflight` 249, `bin/parlay-preflight.test.sh`
250, `docs/traps.md` 250, the new agent note 38, `.gitignore` 74, this file 250).

## Left undone, with reasons

- **The PR is open, not merged**: <https://github.com/trillium/parlay/pull/314>,
  `gnhf/objective-make-a-new-904428` → `main`. Its net diff is the ten files listed at the
  top of this write-up (`README.md`, `AGENTS.md`, `docs/README.md`, `docs/traps.md`,
  `docs/agent-notes/a-relay-that-cannot-name-its-server-passes.md`, `bin/parlay-preflight`,
  `bin/parlay-preflight.test.sh`, `.github/workflows/ci.yml`, `.gitignore`, `notes.md`).
  The `.pi/` scratch files an early auto-commit captured were added in one commit and
  removed in another, so they do not appear in the pull request's net diff at all.
- **The branch was brought level with `main`** (`git merge origin/main`, two docs-only
  commits), clearing `parlay merge-gate 314`'s `behind-base` finding — a real blocker, since
  a behind branch's checks ran against an older merge and GitHub does not re-run them when
  the base moves.
- **CodeRabbit review was requested** (`gh-axi pr comment 314 --body "@coderabbitai
  review"`); on this repo the bot never runs on its own, so that comment is the only route
  to gate-visible review evidence. All four CI checks report `pass`. The review itself had
  not posted a body when this was written, so `parlay merge-gate 314` still reports
  `no-review-evidence` alongside `head-not-pushed` for the commits the harness had not yet
  handed to the remote.
- **The harness commits after the turn and never pushes**, so the tip a turn can push is
  always one commit behind what it just wrote; the remaining edits need one more push, and
  then a re-read of the gate.
- **`docs/traps.md` completeness is not gated.** The CI docs-index gate covers `docs/*.md`
  only, so a future `docs/agent-notes/*.md` that no document links is invisible to every
  gate. `traps.md` is currently the only thing that makes every note reachable; a gate for
  that would be a new CI check, which this task was not asked to add.
- **The README below the Quickstart is still reference material** (system map, layout,
  worked config, development, publishing). It was left in place rather than moved: it is
  reachable, accurate, and the task was to add a path, not to prune the manual.
- **A root `go.work`** — see "what was deliberately left alone".
