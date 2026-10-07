# Onboarding work: what changed, and the evidence

This branch makes the first hour in this repo readable instead of source-bound. Seven pieces, in the order a newcomer meets them:

| Piece | What it is |
|---|---|
| `bin/parlay-preflight` (+ `bin/parlay-preflight.test.sh`) | One bash-only command that checks the whole prerequisite set and names **every** missing thing in one run with its exact `fix:` command. Step 0 of the README path, and the command to re-run when a machine misbehaves. |
| `docs/traps.md` | `AGENTS.md`'s incident record reordered into the nine stages a newcomer walks through. All 66 `docs/agent-notes/` notes appear, none dropped, none rewritten, every link target unchanged. |
| `README.md` Quickstart | One ordered five-step path with a real first command, an isolated-fleet step, one health surface named in both roles, a delivered first routed message, and success/failure criteria on every step. |
| `.github/workflows/ci.yml` (new hygiene step) | Keeps the two lines above from rotting: every `docs/agent-notes/*.md` must appear in the ordering as a **link target** (a prose mention is not placement), every one of the five path documents must exist, and every local link in them must resolve. |
| `docs/agent-notes/a-relay-that-cannot-name-its-server-passes.md` · `docs/agent-notes/assets-resolve-once-at-server-start.md` | Two newly observed facts, each linked from its `traps.md` stage: the relay check against cross-instance enrollment is inert against a relay older than its own `server` field (stage 4); and the server's panel-assets directory is chosen once at startup, so a server started before the bundle exists serves `503` for the rest of its life (stage 2). |
| `.gitignore` | `.pi/` ignored, and the harness task-log files an auto-commit had picked up are untracked — so no harness scratch file can be committed again. |

## What a newcomer can now do without reading source

1. **Clone the repo, then run `./bin/parlay-preflight`** — step 0's two commands, in that order — and be told everything the machine needs at once, with the exact remedy for each, rather than one blocker per failed build.
2. **Run `./examples/bootstrap-sandbox.sh`** and watch the server, CLI, registry, history, the reply path and `doctor` work in a throwaway sandbox whose `LIMITS` line names what it does *not* cover (delivery and the relay); steps 2–5 then give a served message, a read-back, a health verdict, a live listener that receives your message, a panel that loads, and three predicted non-zero exits (voice engine; `doctor`; `doctor deploy`).
3. **Know where to look when it breaks**: the same `./bin/parlay-preflight` an operator runs, then `docs/traps.md` at the stage they are in.

## What was reordered, or corrected, and why

- **`docs/traps.md` is new** (246 lines): `AGENTS.md` and `docs/agent-notes/` are in incident order — right to maintain, wrong to meet. It orders the same facts by when they bite: before you run anything → first server and message → an agent that receives → a second instance or fleet → when something looks wrong → spawning → changing the code → landing → going deeper.
- **The README Quickstart gained a step and was renumbered** (step 1 is now the sandbox, server → 2, CLI → 3, talk/health → 4, panel → 5), every internal step reference updated, and every step now states what success and failure look like. **Step 0 now opens with the clone itself**: it used to open with `./bin/parlay-preflight` a full section above the file's only `git clone`, so the path's first command was one a newcomer could not run (`No such file or directory`, exit 127); the clone moved up, and the later prereq block keeps only its optional `bun install`.
- **The prerequisite block contradicted the path.** It said "Every command in this Quickstart is Go" while step 5 is `bun run build`; it now says steps 0–4 are Go and Bun is needed for the panel (step 5) or the git hooks.
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
- **Step 5's panel claim was false on a fresh clone.** It read *"the server found the bundle by
  itself … so the command in step 2 works unchanged."* It does not: the assets directory is
  resolved **once, when the server starts**, and on a fresh clone the gitignored bundle is absent, so the step-2 server binds a bare `dist` and answers `503` for the rest of its life —
  building it in step 5 changes nothing about that process. Executed on a fresh clone, in that
  order: `assets: dist` at boot, `bun run build` exit 0, `GET /` → `503`; the README's own `503`
  diagnostic ("the bundle is not built") was therefore also wrong. The README now says to Ctrl-C
  and re-run step 2 (or build before step 2, or pass an absolute `-assets-dir`), makes `200` step
  5's success criterion, and splits the diagnostic into "never built" versus "built after this
  server started"; `docs/command-server.md` records the startup-only resolution and the
  relative-`-assets-dir` behaviour (`../client/dist` works, `packages/client/dist` does not), and
  `docs/traps.md` stage 2 places the new note.
- **The path had no step that delivers.** Steps 4–5 left a newcomer holding a message on a
  channel nobody reads (step 4's own `doctor` output says `monitor not listening`) plus an
  optional panel, so the objective's *first routed message* was only implied by a bullet list. A
  new **"Your first routed message"** subsection now sits between steps 4 and 5: the command
  (`listen --agent demo --name Demo --legacy-poll`, which needs no relay), the three lines success
  prints, the `CHAT_MSG` line that *is* delivery, the plain `send --demo` that needs no `--force`
  once `listen` registered the agent, and the two hazards (takeover by agent id; nothing left in
  `$TMPDIR/parlay`). Step numbering is unchanged, so no other document's step references move.

## What now keeps the path from rotting

The docs-index gate scans `docs/*.md` only, so a note under `docs/agent-notes/` that no document links
is invisible to every gate (one existed until `traps.md` was written), and a broken relative link
renders as plain text on GitHub. One new hygiene step closes both, deriving each side from the
filesystem and resolving each link against its containing directory, scoped to the newcomer path
(`README.md`, `AGENTS.md`, `docs/README.md`, `docs/traps.md`, `examples/README.md`) rather than all of
`docs/`, which holds dated snapshots. Proved to fail, not just to pass: the `run:` block is extracted
from the YAML (tested text = shipped text) and run against six tree shapes — control, an unplaced note,
a prose-only mention, a missing path document, a dangling link, an empty `agent-notes/` (12/12).

## What was deliberately left alone

- `AGENTS.md`, `docs/agent-notes/*` and every existing incident record: no fact was changed (the
  preflight paragraph, the CI-gate line and — on review — one one-line pointer to the new
  asset-resolution note are the only additions).
- `examples/bootstrap-sandbox.sh`: reused as-is, not reimplemented — it already encodes the
  isolation recipe, including the hardcoded paths.
- Deployment scripts, `internal/guard.GuardedPaths`, every public endpoint shape, and
  `tools/monitor/parlay-monitor.sh` (its tolerance for a relay that cannot report its upstream is
  deliberate and documented in the code — a product decision, not an onboarding one).
- The server's startup-time asset resolution: documented, not made lazy. What a mid-flight asset
  swap should do is a product decision; the new note says so and warns against a drive-by fix.
- `docs/ux-eval-2026-08-30.md` (a dated field report citing the README's old step numbers) and the
  README below the Quickstart: not rewritten — the first would falsify evidence, the second is
  reachable and accurate. No root `go.work`: it would not make the stop condition pass.

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

The repo is four Go modules with no root module, so `./...` at the root cannot resolve. It is
pre-existing and **not gamed** — both repairs were tried here and removed: a root module alone
still fails, a root `go.work` fails every module that predates its `go` directive, and adding both
makes `go build ./...` exit 0 while printing `matched no packages`, only for `go vet ./...` to exit
1 with `no packages to vet`. No known-red on this box: `make test-bdd` was green before the first
commit and every iteration since.

### The newcomer path, executed end to end on a fresh clone with a clean `HOME`

Step 0 run from an empty directory: `git clone --branch gnhf/objective-make-a-new-904428 https://github.com/trillium/parlay` exits 0, `./bin/parlay-preflight` in that clone prints `12 checks: 12 ok, 0 warning(s), 0 failure(s).` and exits 0, and `./bin/parlay-preflight` in a directory that is not a clone prints `No such file or directory` (exit 127) — what the old step 0 opened with, a full section above the file's only `git clone`. The branch flag is the point: `main` has no `bin/parlay-preflight` yet, so step 0 becomes runnable there when this PR lands and not before.

`git clone` of this branch, `HOME` at an empty directory, `GOCACHE`/`GOMODCACHE` pinned at the real
ones. Steps 0–5 run literally as the README writes them:

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

And the leg the objective's headline names — a message an *agent* actually receives, which step 1's
sandbox `LIMITS` declares uncovered — run on this branch in a kept sandbox (`--keep`), with the
server restarted inside it so the listener has something to poll:

```
$ ./examples/bootstrap-sandbox.sh --keep                      # up, all checks passed, sandbox kept
$ # server started in that sandbox on its port (HOME + -state-dir inside it), then:
$ parlay listen --agent demo --name Demo --legacy-poll        # the README's new command, literally
parlay listen: registering 'demo' …
parlay listen: announced — arming monitor …
parlay monitor (legacy poll) — server http://127.0.0.1:53542 channel demo
$ parlay send --demo "first routed message from a newcomer"   # plain send: listen registered demo
sent to demo — id m5      exit=0
CHAT_MSG|m5|user|first routed message from a newcomer          ← delivery: what the agent reads
$ ps -ax -o pid,ppid,command | grep 'listen --agent demo'    # one process, no monitor child:
32988 32942 …/bin/parlay listen --agent demo --legacy-poll
$ kill -TERM 32988 → exited after TERM; kill -INT → exited after INT
$ ls "${TMPDIR:-/tmp}/parlay"    → relay.sock srv-35be36e4e6 srv-b522444a3e   # untouched
```

Every step behaved as the (now corrected) README says, and so does the delivery leg: the listener
received the message and exits on SIGTERM/SIGINT, `--legacy-poll` wrote nothing into the host
relay's runtime dir, and the `assets: dist` boot line is the whole of the step-5 bug. Cleanup:
every server and listener verified gone with `ps`, the kept sandbox and both temp directories
removed, and the live `~/.parlay` untouched (`find … -newermt '-3 hours'` → nothing).

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
comparison, a live instance answering `/health` (reported up, **not** told to start a server), six
simultaneous blockers named in **one** run with the summary's own count asserted equal to the FAIL
lines printed, and `env -i PATH=/nonexistent` against the real checkout. CI runs it.

### Links, and claims checked against the code

```
$ bash /tmp/gate-step.sh        # the shipped hygiene step, extracted from .github/workflows/ci.yml
66 agent-notes are placed in docs/traps.md; 241 links across 5 documents resolve      exit=0
$ bash /tmp/docsindex-step.sh   # the other shipped hygiene step
all 30 top-level docs are indexed in docs/README.md                                   exit=0
```

Both `run:` blocks were pulled out of the YAML with a few lines of PyYAML rather than retyped, so
the block that was executed is byte-for-byte the block CI ships. Claims were checked against the
code, not against another document: `bootstrap-sandbox.sh`'s redirects, its ten `run_check` calls
and `LIMITS` block; `defaultAssetsDir()` / `repoAssetsDir()` and `static.Handler`'s per-request
stat in `packages/go-server`; `go-server`'s `/health` body; `tools/cli/go.mod`'s `go 1.26.5` floor;
the launchd label in `packages/go-server/deploy/lib.sh` (`com.parlay.go-server`, not the retired
`com.parlay.chat-server` that `AGENTS.md` still named); `parlay-monitor.sh`'s upstream comparison
and `monitor.go`'s `PARLAY_SERVER=config.ServerURL()`; `relay_control.go`'s `/health` handler with
its pinned test; and the new delivery subsection's claims — `listen --legacy-poll` as one process,
SIGTERM/SIGINT stopping it, and no spool in the runtime dir. The `-assets-dir` claims in the new
note and in `docs/command-server.md` were run, not inferred: from `packages/go-server`,
`-assets-dir ../client/dist` → `200`, `-assets-dir packages/client/dist` → `503`.

### Line budget

Enforced only for staged `*.ts` files (`tools/hooks/pre-commit`), so markdown has no budget; self-imposed
anyway and met: `bin/parlay-preflight` 249, its harness 250, `docs/traps.md` 246, the agent notes ≤ 43,
this file. The gate is a step inside `ci.yml`, not a new script.

## Where this stands

- **The PR is open and deliberately not merged**: <https://github.com/trillium/parlay/pull/314>,
  `gnhf/objective-make-a-new-904428` → `main`; it carries the delivery step, the preflight and the
  hygiene gate. Its live facts — head, checks, review, merge-gate verdict — are the PR's own state
  and are deliberately not restated here, because a status recorded in this file is stale the moment
  the harness commits after the turn. Every review finding so far is fixed on the branch.
- **The branch tip is one documentation commit ahead of the remote by construction** — the harness
  commits after the turn, so a turn's edit travels as the next push, re-pinning the review and
  spending one more ~1-hour window.
- **The stop condition is unmet and cannot be met for this build topology** — literal output above.
- **No harness scratch is in the PR**: an auto-commit once captured eight `.pi/tasks/**` files, so
  `.pi/` and `.gnhf/` are tracked-ignored now; the net PR diff contains none of them.

---

## Runtime observability work (branch notes for PR #313 follow, unmodified)
The section above is main's onboarding account (PR #314), which landed while this branch was in flight.

# notes.md — making parlay's runtime observable without reading its source

Status: **in progress** (this file is maintained across the run; see "Left undone").

## What an operator can now answer that they could not before

### 1. `parlay explain <agent-id>` — "why is this agent not answering?"

One read-only command, one screen, six sources. Before it, this took four
commands plus source-reading:

```
parlay explain crew-1
server          http://127.0.0.1:60533
relay runtime   /…/rt1

registration    registered — name Crew One, color #abc
channel         last observed 12m ago (2026-10-07T08:37:56Z)
crew state      working · source: status · building the parser
status file     working [key=parser]: building the parser [last written 0s ago]
pane age        started 2026-10-07T05:37:56Z (3.2h ago)
relay           up — polling http://127.0.0.1:60533, runtime /…/rt1
relay enroll    polling this agent
queue           2 line(s) queued in /…/rt1/crew-1.chan, unconfirmed-consumed (nothing in the fleet acknowledges a read); resume cursor m-2
delivery        3 of the last 20 ledger event(s), oldest first:
                  2026-10-07T08:37:56Z  spooled msg m-1 role=user
                  2026-10-07T08:47:56Z  SPOOL FAILED for msg m-9 — it did not reach the agent
                  2026-10-07T08:48:56Z  delivery ended — reason=channel-gone spoolLines=2
commands        2 record(s) for this agent, newest first:
                  failed   send             ended 2m ago · took 1.2s · exit 1 error
                  running  listen           started 12m ago · took 12m00s
last error      `parlay send` ended failed exit 1 outcome error (2m ago)
```

Questions it answers that previously required reading code:

- **Blind vs drifted.** `channel last observed 12m ago` (an agent that went
  quiet) and `row present, lastSeen absent — the server has never observed
  activity on this channel` (an agent that was never heard from) are different
  lines. A missing `presence` row is a third line. None collapses into another.
- **Registered-but-deaf.** The relay's `/health` names the upstream it is bound
  to, so a relay that answers but polls a different chat server prints
  `WARNING: this relay polls X, NOT the server this CLI targets (Y)` instead of
  "relay up". This is the failure mode `docs/monitor.md` describes as the agent
  appearing live and taking nothing.
- **What is actually queued, and whether anyone is going to read it.** Queue
  lines, the resume cursor a monitor restarting here would use (`NONE` means a
  restart would replay the channel's backlog), and an explicit
  `no relay is answering, so these lines have no writer` when the spool exists
  and the relay does not.
- **What was handed over, and how it ended.** The relay's delivery ledger
  (`spooled` / `spool-failed` / `delivery-ended` / `rotated`) is now readable
  from the CLI, in the ledger's own vocabulary — `spooled` is never printed as
  "delivered".
- **Wedged work.** A command whose heartbeats stopped shows as
  `dropped … (heartbeats stopped without an end report)` and becomes the
  `last error` line.
- **Consistency by construction.** The crew-state line comes from the SAME
  `reconcileCrewState` function and the SAME registry read as `parlay
  crew-state`, so `explain` and `crew-state` cannot report different enrollment
  for one agent in one instant.

### 2. The relay's data plane became queryable (iteration 1)

`{runtime-dir}/delivery.log` — one JSON line per `spooled`, `spool-failed`,
`delivery-ended` and `rotated` event, identifiers and a clock only, never a
message body — readable with `GET /delivery?limit=N&agent=<id>` on the relay's
local control socket, and now surfaced in `parlay explain`. Writers: every
place the relay is the only witness (spool append, both terminal 410/`gone`
paths, `POST /unregister`, process shutdown). It is best-effort and cannot
touch delivery: an unwritable ledger is logged and dropped, pinned by a test
that makes the ledger unwritable and asserts the spool still receives the
message.

## What each new surface degrades to, and how it says so

`README`-level contract: [`docs/explain.md`](docs/explain.md) (also indexed in
[`docs/README.md`](docs/README.md)) and [`docs/relay.md`](docs/relay.md).

The governing rule is **an absence is never reported as a healthy value**. Every
line below is real output from `tools/cli` against a private fixture (a private
chat server, a private relay socket, a private agent home — the live fleet was
never touched).

| Degraded mode | What it prints |
|---|---|
| Relay not running (no control socket) | `relay  no answer at <sock> — the relay is not running (or is using another runtime dir), so relay enrollment and the delivery trail are unknown`; `relay enroll  unknown — the relay did not answer GET /agents`; `delivery  unknown — the relay did not answer, so what was handed over is not observable from here` |
| Spool exists, no writer | `queue  2 line(s) queued in <spool>, unconfirmed-consumed (…); resume cursor m-2; no relay is answering, so these lines have no writer` |
| Relay up, bound to another server | `WARNING: this relay polls http://somewhere-else:4242, NOT the server this CLI targets (http://127.0.0.1:60533) — anything sent to http://127.0.0.1:60533 does not reach this relay` |
| Ledger never written | `delivery  no ledger at <path> — this relay has never recorded a delivery event; that is NOT the same as 'nothing was delivered'` |
| Ledger switched off | `delivery  recording is OFF in the running relay (PARLAY_RELAY_DELIVERY_LOG=0) — nothing is being written to <path>` |
| Ledger rotated (history lossy) | `ledger rotated (size-cap) — history before this line lives in delivery.log.1` |
| Old relay, no `/delivery` route (404) | `delivery  unknown — the relay did not answer …` (a 404 is "could not ask", never an empty trail) |
| Heartbeat stale vs missing | `channel  last observed 2.0h ago (<stamp>)` **vs** `channel  row present, lastSeen absent — the server has never observed activity on this channel` **vs** `channel  no presence row in the server's snapshot — never observed on this channel (or not registered)` |
| Server unreachable | `registration  unknown — the server did not answer <url>`; `channel  unknown — …`; `commands  unknown — the server did not answer /api/chat/commands`; `crew state  working · source: status-degraded · … (relay unreachable; status may be stale)`. Exit stays **0** because the relay and the local records still answered. |
| Status file absent / unreadable / unparseable | `status file  nothing recorded` / the reader's own `unreadable`/`unparseable` detail (the frozen crew-state contract) |
| Relay did not answer, so enrollment unknown | `relay enroll  NOT polled by this relay — whether the server registry lists it is unknown (the server did not answer)` — a failed server read is never rendered as "not registered either" |
| Nothing observable at all | stderr `parlay explain: nothing was observable about <id> — the server did not answer at <url> and no local relay record or status file exists`, exit **1** (the only non-zero outcome besides usage) |

Exit codes: `0` at least one source answered (including bad news), `1` nothing
observable, `2` usage. An unknown flag is a hard exit, never silently ignored.

## Tests

- `tools/cli/internal/relayctl/relayctl_test.go` — runtime-dir/socket
  resolution, spool absent vs empty vs readable, the five `SpoolCursor` rules
  copied from the relay's `lastSpooledID` (id required, role must be
  `user`/`agent`, tail-only read), a 404 from `/delivery` treated as "could not
  ask", and the limit/agent query the client actually sends.
- `tools/cli/internal/commands/explain_test.go` — end-to-end against a fixture
  chat server + a fixture relay socket + a fixture agent home: the full story,
  each degraded mode above, the missing-vs-stale `lastSeen` split, the
  registered-but-deaf warning, dropped-command wedging, last-error source
  precedence, and the exit-code contract. These fail before the change (the
  verb did not exist); the healthy-story assertion in particular reddens if any
  section is dropped or if a "spooled" entry is ever relabelled "delivered".
- Both new test files also pin the **read-only** promise structurally:
  `TestEveryControlReadIsAGet` (in `relayctl`) and the fixture assertion in the
  healthy-story test record the HTTP method of every request the client makes
  to the relay's socket — which also serves `POST /register` and
  `POST /unregister` — and fail if anything but `GET` is used. That is the test
  that says the observability surface cannot mutate or slow a delivery path.
- Every relay-ledger test from iteration 1 still pins "a broken ledger is only
  possible when no spool has ever accepted a message".

## Verification

The stop condition is written for a single Go module, but this repository is
**four separate modules with deliberately no root `go.work`** (`.github/workflows/ci.yml`
iterates a `GO_MODULES` list and proves it equals `find . -name go.mod`). Run
verbatim at the repo root:

```
$ go build ./... && go vet ./... && gofmt -l . | (! grep .) && go test ./... && make test-bdd
pattern ./...: directory prefix . does not contain main module or its selected dependencies
exit 1
```

The equivalent per module (`CGO_ENABLED=0`, the same list CI uses) is green, with
`make test-bdd` green:

```
$ for m in tools/cli tools/relay packages/go-server packages/spawn-profiles; do
    (cd $m && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./...)
  done && make test-bdd
tools/cli:      ok github.com/trillium/parlay/tools/cli/internal/commands 49.7s (+ every other package ok)
tools/relay:    ok github.com/trillium/parlay/tools/relay 2.9s
packages/go-server: ok parlay/go-server/internal/{store,handlers,guard,bus,…}
packages/spawn-profiles: ok parlay/spawn-profiles/cmd/validate
make test-bdd:  PASS (evalengine + spawn suites)
```

`go build ./...`, `go vet ./...` and `go test ./...` were also run with
`-race` on the two touched packages (`internal/commands`, `internal/relayctl`).

**Known-red baseline:** none. `make test-bdd` is green on this box, as it was at
the start of the run — there is no pre-existing red to carry.

## Line budget

This repository enforces no per-file line budget (only a 2 MiB tracked-blob
ceiling and a docs-index gate) — the 250-line cap on every new **production**
file is my choice. New test files follow the package's own existing convention
instead: `internal/commands` test files run 216–2055 lines and
`internal/relayctl/relayctl_test.go` ends at 220.

## Deliberately not built

- **No new HTTP route.** Every read `explain` performs already exists
  (`GET /api/chat/subscribers`, `GET /api/chat/commands`, and the relay
  socket's `GET /health|/agents|/delivery`), so `internal/guard.GuardedPaths`
  is untouched and no guard classification/test was needed. A `GET
  /api/chat/explain` would have to re-implement enrollment, presence and
  command filtering server-side for no added truth.
- **No `--json` on `explain`.** The machine-readable halves already exist
  (`parlay commands --json`, the subscribers snapshot, the relay's `/delivery`);
  a third schema would be another thing to keep in sync for a surface an
  operator reads by eye at 2am.
- **No message bodies anywhere new.** The ledger stores identifiers, a role, a
  clock and a count — never text, a path, or an error string — and `explain`
  prints what the ledger holds plus the agent's own status line (which is
  already durable state, not new retention).
- **No dashboard/panel surface, no query language, no new store.** The
  timeline that answers the 2am question is text, and the durable records it
  reads are the ones the relay and the server already keep.
- **Not widened:** `JSON_EXEMPT_PATHS`, `GuardedPaths`, `internal/httpc`'s
  timeout-less client, the spool's `CHAT_MSG` line format, and any deployment
  script or public endpoint shape.

## Left undone (with the reason)

- **The final PR.** The run's orchestrator owns commits, so this iteration did
  not push or open a PR. When the loop finishes, push the branch and
  `gh-axi pr create --base main --head <branch>`; do not merge.
- **A single cross-agent timeline** (objective item 1: queryable by channel,
  window and outcome, distinguishing delivered from queued from dropped from
  superseded). The durable material for it now exists: the relay's delivery
  ledger (per-agent, filterable) and the server's command registry. What is
  missing is a reader that merges those two into one time-ordered view and a
  defined answer for "superseded" (the chat server has no supersession record
  today — `internal/supersession` is representation-plane and does not touch
  chat history). `parlay explain` is the per-agent slice of that timeline; a
  fleet-wide `parlay timeline` is the next unit and should reuse `relayctl`
  rather than invent a second relay client.
- **Fleet-wide liveness** (objective item 2's operator surface): which agents
  are silent, since when, and last observed activity for each. Every
  ingredient is now reader-accessible (presence rows, relay enrollment,
  spool/cursor state), but the fleet view has not been built.
- **`parlay commands` does not read the relay.** It reports only the server's
  live-command registry, so a delivery that never reached an agent is invisible
  there. `explain` bridges that for one agent; `commands --agent <id>` could
  too, by joining the ledger, but that changes an existing surface's contract
  and deserves its own decision.
