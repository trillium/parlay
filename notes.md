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

### 2. `parlay timeline` — "what happened", on one axis

Before it, answering "what happened to crew-1 between 08:00 and 09:00" meant
reading the relay's `delivery.log` by hand, reading `audit.log` by hand, calling
`GET /api/chat/commands` by hand, and interleaving three vocabularies by eye —
then reading source to know what each one meant. One read-only command now
merges them, queryable by agent (`--agent`/`--channel` or a bare id), by time
window (`--since`/`--until`, RFC3339 or `90s`/`45m`/`2h`/`3d`), by outcome
(`--outcome queued,dropped,…`) and by count (`--limit N`, `0` = all):

```
parlay timeline — oldest first; 6 matching event(s)
  asked: everything the records still hold
  runtime /tmp/ptlA.PzWpdi · server http://127.0.0.1:1

2026-10-07T07:05:22Z  2h00m ago  superseded   crew-1   msg m-1 (user) from captain — an earlier hand-over of this message id — it was handed over 2 time(s) and only the newest one is the live delivery (a channel replay, not a second message)
2026-10-07T07:05:22Z  2h00m ago  queued       crew-1   msg m-1 (user) from captain — still in the agent's spool — nothing in this fleet acknowledges a read, so this is queued, not delivered
2026-10-07T07:05:22Z  2h00m ago  dropped      crew-1   msg m-9 — the append to the agent's spool FAILED — this message did not reach the agent …
2026-10-07T07:05:22Z  2h00m ago  enrolled     crew-1   channel claimed by actor a1b2c3d4
2026-10-07T08:35:22Z  30m01s ago  ended        crew-1   the channel stopped being polled — reason=channel-gone; 2 line(s) were still in the spool at that moment, unproven-consumed
2026-10-07T08:35:22Z  30m01s ago  refused      crew-1   register-denied by actor none — another caller held the channel; the attempt is recorded, never silent

  shown: queued=1 · dropped=1 · superseded=1 · ended=1 · enrolled=1 · refused=1

sources
  delivery ledger (read) 4 delivery event(s) read (oldest first); …
  audit log (read)       2 control-plane action(s) (register/unregister/denied) read · …
  relay control socket (unreachable) no answer at /tmp/ptlA.PzWpdi/relay.sock — the relay is not running (or uses another runtime dir). The trails above are files and were still read; only the relay's live state is unknown
  command registry (unreachable) could not ask http://127.0.0.1:1/api/chat/commands — the server did not answer (…); commands are unknown, not absent
```

Four questions it answers that nothing answered before:

- **What did the relay actually deliver, to whom, and when?** Every hand-over,
  per agent, with the outcome the records can prove — and the two facts no
  other surface has: whether the line is *still* in the spool, and whether a
  message was handed over more than once (`superseded`).
- **Where did history get lost?** A `rotated` marker sits in the trail at the
  point of loss, and the generation before it is read too, so a short history
  is visibly short rather than quietly short.
- **What did the last command do, and did it succeed?** `send → failed ·
  exit=1 · outcome=error · took=1.2s`, or `running-for=` when a record has no
  duration yet.
- **Did anything answer at all?** The `sources` footer, the header's
  "newest N of M matching", and exit 1 when nothing was observable.

**The one rule it will not break:** nothing in this fleet acknowledges that a
message was read — no receipt, no consumer cursor the sender can see, and the
relay's poll loop never learns what the monitor tailing the spool consumed. So
`delivered` is deliberately **not an outcome** (`--outcome delivered` is a
usage error), and `queued` means exactly "spooled, and the line is still in the
spool", verified against the spool file rather than assumed. The footer of
every human-readable run restates it.

**It is read-only and cannot slow delivery.** It opens files, asks the local
socket GETs only, and issues one GET. A test asserts every control-socket
request was a `GET` and that both trails plus the spool are byte-identical
after a run.

### 3. The relay's data plane became queryable (iteration 1)

`{runtime-dir}/delivery.log` — one JSON line per `spooled`, `spool-failed`,
`delivery-ended` and `rotated` event, identifiers and a clock only, never a
message body — readable with `GET /delivery?limit=N&agent=<id>` on the relay's
local control socket, and now surfaced in `parlay explain`. Writers: every
place the relay is the only witness (spool append, both terminal 410/`gone`
paths, `POST /unregister`, process shutdown). It is best-effort and cannot
touch delivery: an unwritable ledger is logged and dropped, pinned by a test
that makes the ledger unwritable and asserts the spool still receives the
message.

### 4. `parlay liveness` — "who has gone quiet, and is that heartbeat absent or merely expired?" (iteration 4)

`parlay explain` answers that for one agent; `parlay launch` reports
live/ghost/offline but has no clock and no last-activity notion. One read-only
table now answers it for the fleet, with four columns that are four different
facts:

```
parlay liveness — 4 agent(s) in the fleet · silence window 10m
server          http://127.0.0.1:34517
relay runtime   /tmp/plv.DPg6yg/rt
sources
  registry + presence    read — GET /api/chat/subscribers answered — 4 registered agent(s), 3 presence row(s)
  process table          read — 0 live listener process(es) on this host
  relay                  unreachable — no answer at /tmp/plv.DPg6yg/rt/relay.sock — the relay is not running (or uses another runtime dir). Its delivery trail is a FILE and is still read below; only the relay's live state is unknown
  delivery ledger        read — 2 delivery event(s) read (oldest first); the relay's own rotation is recorded in the trail, so a shortened history says so

AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-3               ghost     no row                 3h00m      status "blocked" 3h00m ago
crew-4               ghost     expired (3h00m ago)    3h00m      channel activity 3h00m ago
crew-2               ghost     never observed         unknown    no dated record
crew-1               ghost     fresh (32s ago)        no         channel activity 32s ago

notes
  crew-2               heartbeat  a presence row exists with no lastSeen — the server has never observed activity on this channel (absent, not expired)
  crew-2               silence    no dated activity record exists (looked at: the server's presence row for this channel) — silence is unmeasurable here, not zero
  crew-4               heartbeat  the server's last recorded activity on this channel is 3h00m old — past the 10m window (expired, not absent)
  …

2 of 4 silent beyond 10m · 2 with no heartbeat record (never observed or no presence row)
```

The four fixture agents have no listener process, so they correctly read
`ghost` — that is the process-table half of `STATE` doing its job.

Questions it answers that nothing answered before:

- **Which agents are silent, and since when.** One column, measured over the
  newest dated record of ANY source — the server's channel stamp, the agent's
  own status file, the relay's delivery trail — so an agent that is working
  without talking (crew-4 above: expired channel stamp, status line 20s old) is
  not falsely reported silent. `--silent` narrows to the rows whose activity is
  older than the window or whose channel has no heartbeat record at all, and
  `--silent-for 10s` asks the same question with a tighter window.
- **Absent vs expired.** A stamp can expire; a presence row with no `lastSeen`
  and an absent row CANNOT. They print as `never observed` and `no row`, carry
  no parsed time at all, and `SILENT` is `unknown` with the reason — never `0`,
  and never an invented age.
- **What the last observed activity was.** Which record saw it, what it
  recorded, and how long ago.
- **Blind vs drifted vs deaf.** `STATE` is the registry intersected with the
  process table, and a process table that could not be read leaves a registered
  agent `live` with a caveat note instead of becoming a `ghost` — a wrong ghost
  sends an operator to clear the registration of a working agent.
- **One agent, or the whole fleet.** A bare id or `--agent <id>` filters to one
  row; `--json` carries the closed vocabularies (`state`, `heartbeat`,
  `silence`, `last_source`, each source's `state`) verbatim plus the same note
  sentences the text mode prints.

It reads no spool and no resume cursor on purpose: those are per-agent facts
(`parlay explain` owns them), and opening N spool files to answer "is this
channel being heard from" would make a fleet-wide read slower for a fact that
belongs to the per-agent surface. Its four sources are the registry snapshot,
the process table, the relay's control socket, and the two durable files (the
delivery ledger, the status files).

## What each new surface degrades to, and how it says so

Doc-level contracts, all indexed in [`docs/README.md`](docs/README.md):
[`docs/explain.md`](docs/explain.md), [`docs/timeline.md`](docs/timeline.md),
[`docs/liveness.md`](docs/liveness.md) and [`docs/relay.md`](docs/relay.md)
(plus `tools/relay/NOTES.md`).

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
| Nothing observable at all (`explain`) | stderr `parlay explain: nothing was observable about <id> — the server did not answer at <url> and no local relay record or status file exists`, exit **1** (the only non-zero outcome besides usage) |

`parlay timeline` adds the file-first half of the same table — the modes where
the relay is dead and the answer still has to exist:

| Degraded mode | What `parlay timeline` prints |
|---|---|
| Relay not running | rows are still printed (the trail is a file); footer: `relay control socket (unreachable)  no answer at <sock> — the relay is not running (or uses another runtime dir). The trails above are files and were still read; only the relay's live state is unknown` |
| Ledger never written | `delivery ledger (absent)  no ledger — this relay has never recorded a delivery event. That is NOT the same as 'nothing was delivered': an older relay build has no ledger at all` |
| Ledger unreadable | `delivery ledger (unreadable)  could not read it (<err>) — the trail exists and its contents are unknown` |
| Ledger recording switched off | `delivery recording (off)  PARLAY_RELAY_DELIVERY_LOG=0 in the relay's environment — it is recording nothing now, so any events below predate the switch-off` (only a live socket can say this; the file is still read) |
| Rotated ledger | `rotated generation (read)  <n> event(s) from the generation before the last rotation — read as well, so rotation shortens the trail only where the 'rotated' marker says so`, plus a `rotated` row: `every event older than this line is gone, so a trail that starts at this marker is not a quiet fleet` |
| Spooled, but the spool cannot be read | outcome `unknown`: `the relay spooled it, but the spool could not be read (no spool at <path> — never created here, or removed; if every agent reads this way, the relay and this CLI may be using different runtime dirs), so whether the line is still waiting is not observable from here` |
| Spool line still present | outcome `queued`: `still in the agent's spool — nothing in this fleet acknowledges a read, so this is queued, not delivered` |
| Spool line gone | outcome `left-spool`: `no longer in the agent's spool — read or pruned, and nothing in this fleet records which` |
| Message handed over twice | outcome `superseded`: `an earlier hand-over of this message id — it was handed over 2 time(s) and only the newest one is the live delivery (a channel replay, not a second message)` |
| Sprig append failed | outcome `dropped`: `the append to the agent's spool FAILED — this message did not reach the agent` |
| Old server, no command registry (404) | `command registry (unsupported)  this server answered 404 — it is older than the live-command registry, so no invocation is recorded anywhere` — distinct from `(unreachable) … commands are unknown, not absent` |
| Relay bound to another server | `relay control socket (read) up — polling <other>, runtime <dir> · WARNING this relay polls <other>, NOT the server this CLI targets (<url>): nothing sent to <url> reaches this relay` |
| Timestamp missing or unparseable | the row is kept with `?` in the time column and `atKnown:false` in `--json` (never midnight, never dropped); inside a window it is kept and flagged, because a window cannot date it |
| List truncated by `--limit` | `newest 2 of 5 matching event(s) (--limit 0 shows all)` — the count is of MATCHES, before the limit |
| Unrecognised event/action name (a newer relay) | outcome `unknown`: `unrecognised delivery event "throttled" — this reader predates it; the record exists and is not classified` (an audit action likewise) |
| Spool reconciliation past the cap | outcome `unknown`: `this trail mentions <n> agents and one timeline read reconciles at most 64 spools; narrow with --agent to get a per-message answer` |
| Nothing observable at all (`timeline`) | stderr `parlay timeline: nothing was observable — no delivery ledger, no audit log, the relay at <sock> did not answer, and no command registry at <url>`, exit **1**; the header says `no event matched. No record answered at all, so an empty timeline means nothing was observable — see sources.` instead of a bare empty list |

`parlay liveness` keeps the same rule with a different set of sources — two of
its four degraded modes are about a record that is absent rather than stale:

| Degraded mode | What `parlay liveness` prints |
|---|---|
| Server unreachable | `registry + presence    unreachable — no answer from <url> — registration and channel activity are UNKNOWN, not absent (an unreachable server is not an empty fleet)`; per row `STATE unknown` with `the server did not answer, so registration is unknown — this is not the same as offline` and `HEARTBEAT unknown` with `the server did not answer, so channel activity is unknown (not absent, and not fresh)`. Exit stays **0** when a local record answered. |
| Process table unreadable | `process table          unreadable — the process table could not be read, so a dead listener cannot be ruled out — registered agents are NOT reported as ghosts on a failed probe`; every registered agent stays `STATE live` with `registered; the process table could not be read, so a listener cannot be confirmed OR ruled out` |
| Relay not running | `relay                  unreachable — no answer at <sock> — the relay is not running (or uses another runtime dir). Its delivery trail is a FILE and is still read below; only the relay's live state is unknown`; `LAST OBSERVED ACTIVITY` still comes from the ledger (`relay spool-failed 1h30m ago`) |
| Relay bound to another server | the relay line gains `· WARNING this relay polls <other>, NOT the server this CLI targets (<url>)` |
| Heartbeat expired vs absent | `expired (3h00m ago)` **vs** `never observed` (a presence row with no `lastSeen`) **vs** `no row` (no presence row at all) **vs** `unknown` (the server did not answer, or the stamp does not parse). Only the two ages carry a parsed stamp at all. |
| No dated record anywhere | `SILENT unknown` plus `no dated activity record exists (looked at: <what was consulted>) — silence is unmeasurable here, not zero` |
| No local home for the id | `no agent home for this id on this host, so its status file could not be consulted either — run this where the agent runs to see local activity` |
| Ledger never written | `delivery ledger        absent — no ledger — this relay has never recorded a delivery event. That is NOT the same as 'nothing was delivered': an older relay build has no ledger at all` |
| Nothing observable at all | `no agents to report — the server lists none and this host has no agent homes`, stderr `parlay liveness: nothing was observable — the server did not answer at <url> and no relay trail or agent home exists under <agents-root>`, exit **1** |
| `--silent` with nothing to show | `no agent is silent beyond 10m0s, and none is missing a heartbeat record` — an explicit empty, not a bare table |

Exit codes: `0` at least one source answered (including bad news), `1` nothing
observable, `2` usage. An unknown flag is a hard exit, never silently ignored.

## Tests

- `tools/cli/internal/liveness/liveness_test.go` — the pure classifier, with no
  clock, files or network: an unreadable registry is `unknown` and never
  `offline`; a failed process-table probe never becomes `ghost`; the four
  heartbeat shapes stay four (only the two ages carry a parsed stamp);
  `HeartbeatFor` is the channel record's own age and never the silence
  duration; a fresh status file beats a three-hour-old channel stamp (the
  false-alarm case); silence expires on the NEWEST record of any source; no
  dated record is `unknown` with the list of what was looked at, not `0`; a
  future stamp is not negative silence; the default clock and window.
- `tools/cli/internal/commands/liveness_test.go` — end-to-end against private
  fixtures (a private server, a private runtime dir with a real `delivery.log`,
  private agent homes, an injected process table): the four-agent fleet table
  with all four row shapes; `--silent` and `--silent-for`; the
  absent-heartbeat-is-not-expired pin; relay down with the trail still
  answering; server down with local activity still answering (exit 0) and the
  `unknown ≠ offline` pin; nothing observable at all (exit 1); a failed
  process-table probe; the relay-bound-elsewhere warning; the `--json`
  envelope; the read-only assertion (every control-socket request a `GET`, and
  the ledger plus the status file byte-identical afterwards); four usage
  errors; and the single-agent filter.
- **Tests that bite.** The honesty rules were mutation-checked rather than
  assumed. Four separate mutations each turn tests red: making `never observed`
  fall through to the stamp path (7 tests), measuring silence from the channel
  stamp alone (6 tests), reporting `ghost` on a failed process-table probe (2
  tests), and not counting a local roster as an observable source (1 test).
  `explain_test.go`'s `explainRun` was split into a shared `verbRun` so both
  verbs' tests own their pipes identically (the exit path panics through a test
  double, and a panic unwinding through `captureStdout` leaks a goroutine).
- `tools/cli/internal/timeline/timeline_test.go` — the pure classify/select
  layer, with no I/O: spooled-still-present → `queued` and never "delivered";
  spooled-but-gone → `left-spool`; an unreadable or never-looked-up spool →
  `unknown` with a reason (never `left-spool`); an earlier hand-over of a
  message id → `superseded` while the newest stays live; supersession computed
  in read order so an unparseable stamp cannot reorder it; `spool-failed` →
  `dropped` and never superseded; `delivery-ended` with an explicit `0` vs an
  absent `spoolLines`; an unrecognised event name kept as `unknown` rather than
  dropped; lifecycle/refusal mapping; command detail (exit code, outcome,
  duration, `running-for`); the undated-keeps-its-row rule; and the filter
  matrix (agent, outcome, window, newest-N-kept-in-order, empty-not-nil).
- `tools/cli/internal/relayctl/relayctl_trail_test.go` — the file readers:
  absent is not empty and not an error; both rotation generations read in order;
  corrupt lines skipped and counted; unreadable is not absent; the spool reader
  returns ids only (a test asserts no message body can leak into the id set),
  counts duplicates, and falls back to the `.retired` generation.
- `tools/cli/internal/commands/timeline_test.go` — end-to-end against private
  fixtures (a private runtime dir, private trails, a private server): the full
  story across all four sources; relay down with the trail still answering;
  absent vs unreadable vs rotated vs switched-off; spool unreadable → `unknown`;
  the 404-vs-unreachable split; the another-server warning; filter and limit
  behaviour; the `--json` envelope including `atKnown:false` for a junk stamp;
  nine usage errors; and the exit-code contract.
- **Tests that bite.** The honesty rules were mutation-checked rather than
  assumed: making `classifySpooled` skip its unknown-spool branch and neutering
  the supersession check turns SIX tests red —
  `TestUnreadableSpoolIsUnknownNotGone`, `TestNoPresenceEntryAtAllIsUnknown`,
  `TestRepeatedHandOverSupersedesTheEarlierOne`,
  `TestSupersessionUsesReadOrderNotStamps` (package `timeline`), plus
  `TestTimelineAnswersWhatHappened` and
  `TestTimelineUnreadableSpoolIsUnknownNotGone` (package `commands`). The whole
  command is new, so every test in it fails before the change by construction.
- **Read-only, pinned structurally.** `TestTimelineIsReadOnly` records the HTTP
  method of every control-socket request (asserting `GET` only — the same socket
  serves `POST /register` and `POST /unregister`) and hashes both trails plus the
  spool before and after, failing if either changed.
- Earlier iterations, still green: `tools/cli/internal/relayctl/relayctl_test.go` — runtime-dir/socket
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
iterates a `GO_MODULES` list and proves it equals `find . -name go.mod`, so the
absence of a root `go.work` is itself enforced). Run verbatim at the repo root:

```
$ go build ./... && go vet ./... && gofmt -l . | (! grep .) && go test ./... && make test-bdd
pattern ./...: directory prefix . does not contain main module or its selected dependencies
exit 1
```

I deliberately did NOT add a root `go.work` to make that string exit zero: it
would also make `go build ./...` drop `relay` and `cli` executables (~9 MB) into
the repo root, and a committed one would trip CI's 2 MiB tracked-blob hygiene
gate. The honest equivalent is the same chain inside each module, which is the
form the stop condition names, and it is green:

```
$ for m in tools/cli tools/relay packages/go-server packages/spawn-profiles; do
    (cd $m && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... &&
             gofmt -l . | (! grep .) && CGO_ENABLED=0 go test ./...)
  done && make test-bdd
=== tools/cli ===            33 packages: 31 ok, 2 "no test files", 0 FAIL
      ok  github.com/trillium/parlay/tools/cli                       0.750s
      ok  github.com/trillium/parlay/tools/cli/internal/commands    33.601s
      ok  github.com/trillium/parlay/tools/cli/internal/liveness     0.271s
      ok  github.com/trillium/parlay/tools/cli/internal/timeline     …
      ok  github.com/trillium/parlay/tools/cli/internal/relayctl     …
      ok  github.com/trillium/parlay/tools/cli/internal/help         …
      … and 25 more, every one ok
=== tools/relay ===          ok  github.com/trillium/parlay/tools/relay  2.888s
=== packages/go-server ===   ok  parlay/go-server/internal/{atomicfile,bus,capability,guard,
                                 handlers,linkrewrite,remoteinput,sourcecontracts,static,store}
                                 + cmd/parlay-server  (11 packages, 0 FAIL)
=== packages/spawn-profiles === ok  parlay/spawn-profiles/cmd/validate  0.276s
$ make test-bdd
17 scenarios (17 passed) / 55 steps (55 passed)   ok internal/evalengine
 7 scenarios ( 7 passed) / 21 steps (21 passed)   ok internal/spawn
make test-bdd exit=0    (no MODULE-FAIL line, zero build/vet/gofmt failures)
```

`-race` on the two touched packages (`internal/liveness`, `internal/commands`) is
green as well — CI's Go job runs `-race` by default, and the exit-path test
helper owns its pipes so an exiting verb leaves no goroutine behind. On this box
that needs the ICU cgo flags the beads dependency's embedded-Dolt tree wants:

```
$ cd tools/cli && CGO_ENABLED=1 \
    CGO_CFLAGS=-I/opt/homebrew/opt/icu4c/include \
    CGO_CXXFLAGS=-I/opt/homebrew/opt/icu4c/include \
    CGO_LDFLAGS=-L/opt/homebrew/opt/icu4c/lib \
    go test -race ./internal/liveness/... ./internal/commands/...
ok  github.com/trillium/parlay/tools/cli/internal/liveness   1.257s
ok  github.com/trillium/parlay/tools/cli/internal/commands  35.355s
```

Without those flags `-race` on `tools/cli` dies at compile time in
`github.com/dolthub/go-icu-regex/internal/icu` (`unicode/regex.h` not found),
which is a **pre-existing environment gap on macOS**, not a red test: CI's
ubuntu runner has the headers. Plain `CGO_ENABLED=0 go test` needs no flags and
is what the chain above uses.

**Known-red baseline: none.** `make test-bdd` was green on this box before this
iteration's work and after it; the literal root-command failure above is a
repository-shape fact (four modules, no root `go.work`), not a red test, and it
was the same before iteration 1.

## Line budget

This repository enforces no per-file line budget (only a 2 MiB tracked-blob
ceiling and a docs-index gate) — the 250-line cap on every new **production**
file is my choice. `parlay liveness` is split to stay under it:
`internal/liveness/liveness.go` (238, the vocabulary and `Classify`) +
`classify.go` (156, the three pure steps); the verb is `commands/liveness.go`
(180, flags, entry and ranking) + `liveness_sources.go` (207, the reads) +
`liveness_render.go` (177, the table and notes) + `liveness_json.go` (102, the
envelope). Earlier iterations: `internal/timeline` is
`timeline.go` (159, types and vocabulary) + `build.go` (166, classification) +
`select.go` (158, narrowing and ordering); the verb is `timeline.go` (188,
flags) + `timeline_sources.go` (177, the reads) + `timeline_notes.go` (84, the
sentences) + `timeline_when.go` (45) + `timeline_render.go` (233); the relay
readers are `relayctl_trail.go` (181) + `relayctl_spool.go` (89). New **test**
files follow the package's own existing convention instead:
`internal/commands` test files run 216–2055 lines and
`internal/relayctl/relayctl_test.go` ends at 220, so
`commands/timeline_test.go` (482) and `timeline/timeline_test.go` (329) are in
line with their neighbours.

## Harness scratch, and one accidental commit repaired

A hard constraint of this run is that nothing under `.pi/`, `.gnhf/` or any
other harness scratch directory is ever committed. Iteration 3's commit violated
it: `4aaaa1e` added four `.pi/tasks/**/*.output` files. This iteration deletes
them and adds `.pi/` and `.gnhf/` to `.gitignore`, with the reason written beside
the entries — the ignore rule is what makes the constraint structural rather
than remembered, and a gitignore alone would not have untracked the four files
already in the index (hence the explicit deletion). Neither directory is tracked
on `origin/main`, so nothing of the captain's is affected.

## Deliberately not built

- **No new HTTP route.** Every read `explain` and `timeline` perform already
  exists (`GET /api/chat/subscribers`, `GET /api/chat/commands`, the relay
  socket's `GET /health|/agents|/delivery`, and the relay's two trail files),
  so `internal/guard.GuardedPaths` is untouched and no guard
  classification/test was needed. A `GET /api/chat/timeline` would have to
  re-implement enrollment, presence, delivery and command filtering
  server-side for no added truth — and, worse, could only answer while the
  server was up, which is the opposite of what a 2am read needs.
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

- **The final PR.** The run's orchestrator owns commits, so the branch is pushed
  and the PR opened from the commits already made —
  <https://github.com/trillium/parlay/pull/313> (head
  `gnhf/objective-make-parla-ea8605`, base `main`, **not merged**). Because each
  iteration's work is committed after that iteration, the branch has to be
  pushed again at the end of any later iteration for the PR head to include it;
  the PR body says which surfaces its current head contains.
- **A `delivered` outcome, and therefore a read receipt.** Not built because it
  cannot be built honestly without a change to the delivery path itself: the
  monitor would have to acknowledge what it consumed (a new wire field, a new
  round trip, and a delivery path that now depends on an observability hop).
  That is exactly the regression the objective forbids, so the vocabulary stops
  at `queued` and says why in the footer of every run.
- **Supersession beyond message hand-over.** `superseded` here means "an earlier
  hand-over of a message id that was handed over again later", which is what
  the delivery trail can prove. `internal/supersession` is
  representation-plane (records, not chat) and is deliberately not entangled
  with it.
- **`parlay commands` does not read the relay.** It reports only the server's
  live-command registry, so a delivery that never reached an agent is invisible
  there. `explain` bridges that for one agent and `timeline` for a window;
  changing `commands`' own contract deserves its own decision.
- **No spool reconciliation past 64 agents in one pass** (named in the output,
  with `--agent` as the remedy) and **no `--json` on `explain`** (the
  machine-readable halves already exist; a third schema is another thing to
  keep in sync).
