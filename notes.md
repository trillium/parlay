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

Status: **in progress** — iterations 1–11 (see "Left undone" for what is not
done and the PR's head state).

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

### 5. The server's half of the story (iteration 5)

`parlay timeline` now also reads **the chat server's own history**
(`$PARLAY_STATE_HOME/messages.jsonl`), off disk, so the timeline spans the whole
life of a message: the server persisted it → the relay handed it to a spool →
queued / left-spool / dropped. Before this, the timeline could only see what
the relay was handed, so a message the server accepted and nothing ever
collected appeared **nowhere at all** — silence, where the truth was "the
server has it and nobody took it".

```
parlay timeline
2026-10-07T06:54:59Z  3h00m ago  queued       crew-1   msg m-1 (user) from captain — still in the agent's spool — nothing in this fleet acknowledges a read, so this is queued, not delivered
2026-10-07T06:54:59Z  3h00m ago  recorded     crew-1   msg m-1 (user) from captain — the chat server persisted this message on this channel; the relay's own hand-over line for this message is in this timeline — that line, not this one, says what happened next
2026-10-07T07:54:59Z  2h00m ago  unhanded     crew-1   msg m-2 (user) from captain — the chat server persisted this message on this channel and the relay's delivery trail — read in full, with no rotation — holds no hand-over for it: NOTHING picked this message up. Either no relay is enrolled for this channel, the relay polls a different chat server, or the hand-over failed without leaving a line. The message is still in the agent's history, so it can be resent

  shown: recorded=1 · unhanded=1 · queued=1
```

`--outcome unhanded` is now the 2am query for "what did we lose", and it is a
guarded claim rather than a guess. `unhanded` is only produced when a missing
hand-over line is *evidence*, which needs all of: a delivery trail that was
read, unrotated and untruncated; a message newer than the trail's own first
line; a message older than the relay's poll window (90s ≈ two of its 45s
long-polls); and a parseable stamp. Every weaker case stays `recorded` and names
the guard that stopped it — because the ledger is a **young record**, and on a
fleet whose relay predates it, absence of a hand-over is absence of a *record*.
That false accusation is the one failure mode this design is built around; the
old-relay, rotated and too-recent cases are all pinned by tests.

The reader is a **file** reader for the same reason the relay's trails are: at
2am the server is very often the thing that is dead, and `GET
/api/chat/history` cannot be asked of a dead process. It decodes five
identifiers — id, ts, channel, role, from — and has **no struct field for a
message body**, so a body cannot be carried into the timeline, a log, or a new
store by accident; a test pins the field list itself, not just this run's
output. Truncation (an 8 MiB tail, or a 2000-record cap) is reported in the
sources block, and a read of *this host's* state dir while the CLI targets
another host carries the "these records may be a different server's" caveat.

Questions it answers that previously required reading code or guessing:

- **Was it ever picked up?** A recorded message with no hand-over line, on a
trail that can legitimately say so, is `unhanded` — the first time this fleet
has been able to distinguish "lost" from "recent".
- **Is this a quiet fleet or a blind one?** `recorded` rows exist independently
  of the relay, so a timeline with server history and no relay events is a
  different answer from a timeline with neither.
- **Does the file I just read even belong to this server?** The warning on a
  remote target says which host's state dir was read and which server the CLI
  is pointed at.

### 6. `parlay explain` works when the relay is dead (iteration 6)

The single 2am command had one source that only worked while the thing it was
reporting on was still alive: the delivery trail was read over the relay's
control socket, so a dead relay printed

```
delivery        unknown — the relay did not answer, so what was handed over is not observable from here
```

for a ledger that was sitting on disk the whole time. The relay being dead is
the most common reason someone runs `parlay explain` at all, and a *dead
process* does not un-write a file it already appended to — `parlay timeline`
had been reading both generations of that ledger off disk for exactly this
reason. `explain` now does too:

```
relay           no answer at /…/rt1/relay.sock — the relay is not running (or is using another runtime dir), so relay enrollment is unknown; the delivery ledger is a FILE and is read from disk below
relay enroll    unknown — the relay did not answer GET /agents
queue           no spool file at /…/rt1/crew-1.chan — nothing is queued for this agent, or the relay is not running
delivery        read from disk (/…/rt1/delivery.log) because the relay did not answer — 3 of the last 20 ledger event(s), oldest first; whether recording is switched off right now is unknown
                  2026-10-07T08:07:09Z  spooled msg m-1 role=user from=captain
                  2026-10-07T08:37:09Z  SPOOL FAILED for msg m-9 — it did not reach the agent
                  2026-10-07T08:37:09Z  delivery ended — reason=channel-gone spoolLines=2
last error      relay could not spool message m-9 — it never reached the agent (2026-10-07T08:37:09Z)
```

What the change is careful about, each pinned by a test:

- **The socket is still asked first.** It is the only source that can answer two
  things a file cannot — whether recording is switched off *right now*
  (`PARLAY_RELAY_DELIVERY_LOG=0`) and, through `/health`, which server the relay
  was polling. When the answer comes from disk the line says so and says the
  recording state is unknown, instead of inheriting the socket's wording and
  implying a live relay vouched for the rows. A live relay answering also beats
  a stale ledger in the same runtime dir: the file is not consulted and the disk
  wording never appears.
- **Same filter, same vocabulary.** The agent filter mirrors the relay's own
  `GET /delivery?agent=` (filter, then keep the newest 20), so both sources pick
  the same rows for the same trail; another agent's traffic is not this agent's
  story; `spooled` is still never printed as "delivered". The disk rows are real
  evidence and feed `last error` — the `spool-failed` line above is why the run
  reports the message that never arrived.
- **A short trail is never printed as a quiet one.** `delivery.log.1` is read
  too and printed oldest-first, with `· 1 of them from the generation before the
  last rotation (older history is in <path>.1) · 1 corrupt line(s) skipped` and
  the `rotated` marker row (kept even though it names no agent, because it is
  the only evidence that everything older is gone). If ONE generation cannot be
  opened while the other answers, the rows that were read are still shown and
  the failing generation is named in the coverage note (`the active ledger could
  not be read in full (…) so this may be short` / `the previous generation (…)`
  `could not be read (…), so older history is unknown`) — masking readable rows
  behind "contents unknown" would be its own dishonesty. The `exists but could
  not be read` line is now reserved for a trail where *nothing* was readable.
- **Absence is still not health.** No ledger on disk is `no ledger on disk at
  <path> and the relay did not answer`, never "nothing was delivered"; a ledger
  that exists but cannot be opened is `exists but could not be read (…) — the
  file is there and what it holds is unknown, not empty`; and a ledger read but
  quiet for this agent is an ANSWER (exit 0), not "nothing was observable".
  Because the file fallback can always return a view, the exit-1 predicate now
  asks whether the ledger actually EXISTED rather than whether the reader ran.
- **The `relay` line stopped overclaiming.** It used to say the delivery trail
  was unknown when the relay did not answer, which this change makes false.

### 7. The server's roster survives the server (iteration 7)

The same substitution, one surface further: `registration` and `liveness`'s
`STATE` column were answered only by `GET /api/chat/subscribers`, so a dead
server turned the fleet table into a column of `unknown` and one agent's story
into "the server did not answer" — with the roster sitting in
`$PARLAY_STATE_HOME/agents.json` the whole time (a full snapshot, rewritten on
every change). Both verbs now read it, and name the substitution every time they
do. With the server dead the fleet table answers again:

```
  registry + presence    unreachable — no answer from http://127.0.0.1:1 — registration and channel activity are UNKNOWN, not absent (an unreachable server is not an empty fleet)
  registry (disk)        read — read /tmp/pdoc.w9fTru/state/agents.json because the server did not answer — 2 agent(s) in the roster the server last persisted, so registration below comes from DISK; presence is never written to disk, so every heartbeat stays unknown

AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-2               ghost     unknown                unknown    no dated record
crew-1               ghost     unknown                no         status "working" 1s ago
```

Four rules keep this honest, each pinned by tests that go red without it:

- **Registration is answered; presence is not.** The server keeps presence in
  memory only, deliberately, because a connection count that survived a restart
  would be lying — so `HEARTBEAT` stays `unknown` and its note says why
  (`presence is kept in memory … never written to disk`). The fallback cannot
  leak into the heartbeat column by construction: the reader's decode target has
  no field for it.
- **`offline` is reserved for a server that answered.** An id that is missing
  from the roster read off disk is `unknown` (`… whether it is enrolled right
  now is unknown, not settled`), because which state directory the server runs
  with is a separate configuration point from the one this CLI reads — the same
  rule the enrollment lookup already follows for a failed fetch.
- **The file belongs to a HOST.** `agents.json` is only this server's roster
  when the server is on this machine, so the fallback declines otherwise with
  `not-this-host — not consulted — the CLI targets another machine, whose
  registry lives with it`. Loopback addresses (any 127/8, `::1`, `localhost`)
  and this host's own name (mDNS `.local` folded) count as local; anything else
  is treated as another machine, which is the safe direction.
- **The live answer wins.** When the server answers, the file is not consulted
  and none of this wording appears — the same rule the delivery trail follows.

The fallback is a read, never a write: two runs of both verbs over one roster
leave it byte-identical (`TestRegistryFallbackIsReadOnly`).

### 8. `unhanded` now requires the relay to have been the delivery path (iteration 8)

`unhanded` is the one verdict in the timeline that **accuses** something:
"NOTHING picked this message up". Its guard list (iteration 5) covered every way
the *delivery trail* could be an incomplete record, but not a second failure
mode: a channel the relay was **never** the delivery path for can never have a
hand-over line either. That is exactly what `parlay listen --legacy-poll` looks
like from the relay's side — it polls the chat server directly, with no relay,
and what it consumes leaves no spool line, no ledger line and no record anywhere
in this fleet. So on any fleet that uses it, every message it delivered was
reported as lost, and `--outcome unhanded` (the 2am query for "what did we
lose") returned nothing but false positives.

The relay's own control-plane trail is the missing half. `audit.log` records
`register` when a monitor claims a channel and `unregister` when the claim is
released, so `parlay timeline` now builds **claim intervals per channel** and
only states the accusation when a claim covers the message's own time. The file
is read off disk in the same runtime dir as the ledger, so the guard survives
the relay dying, and every way it can fail falls toward silence:

| The relay ... | What the row now says |
|---|---|
| never claimed the channel (a direct poll) | `holds NO claim for this channel at all — the relay was never the delivery path for it, so no hand-over line was ever going to exist. An agent can receive messages without the relay (`parlay listen --legacy-poll` polls the chat server directly), and what such a poll consumed is recorded nowhere in this fleet` |
| released the channel before the message | `shows the relay's last claim on this channel ended at <ts>, before this message was recorded — the relay was not the delivery path for it, so a missing hand-over line is not evidence` |
| has a claim trail that cannot be read | `could not be read (<why>) … whether the relay was ever the delivery path for this channel is unknown — a missing hand-over line is not evidence` |
| has a claim trail that was truncated, or a claim that cannot be dated | named in the row; the verdict stays `recorded` for the same reason |
| claimed the channel and still never handed the message over | `unhanded` fires exactly as before — the accusation is unchanged where the evidence supports it |

`unhanded`'s own sentence also stopped offering an explanation the guard had
ruled out: it no longer says "Either no relay is enrolled for this channel, …"
(the claim trail now shows it was), leaving the two causes that remain — a relay
polling a different chat server, or a hand-over that failed without leaving a
line.

Verbatim, from the built CLI against a private runtime dir and a refused server
port (reproduced in full in [`docs/timeline.md`](docs/timeline.md)):

```
$ parlay timeline                                     # relay claimed crew-1, never handed m-2 over
2026-10-07T06:46:49Z  4h00m ago  enrolled     crew-1   channel claimed by actor fp1
2026-10-07T07:46:49Z  3h00m ago  queued       crew-1   msg m-1 (user) from captain — still in the agent's spool — …
2026-10-07T08:46:49Z  2h00m ago  unhanded     crew-1   msg m-2 (user) from captain — … holds no hand-over for it around the time its claim on this channel covers: NOTHING picked this message up. Either the relay polls a different chat server, or the hand-over failed without leaving a line. …

$ parlay timeline                                     # the relay never claimed crew-1 (a direct poll)
2026-10-07T08:46:49Z  2h00m ago  recorded     crew-1   msg m-2 (user) from captain — … The relay's claim trail (audit.log, read in full) holds NO claim for this channel at all — the relay was never the delivery path for it, so no hand-over line was ever going to exist. An agent can receive messages without the relay (`parlay listen --legacy-poll` polls the chat server directly), and what such a poll consumed is recorded nowhere in this fleet

$ parlay timeline --outcome unhanded                   # on that same fleet — the 2am "what did we lose"
parlay timeline — oldest first; 0 event(s) matched
  no event matched. At least one record answered and held no event for this question, so the fleet really is quiet over it (see sources for what was read).

$ parlay timeline                                     # the relay released crew-1 before the message
2026-10-07T06:46:49Z  4h00m ago  retired      crew-1   channel released by actor fp1
2026-10-07T08:46:49Z  2h00m ago  recorded     crew-1   msg m-2 (user) from captain — … shows the relay's last claim on this channel ended at 2026-10-07T06:46:49Z, before this message was recorded — the relay was not the delivery path for it …

$ parlay timeline                                     # no audit.log at all — enrollment UNKNOWN, not absent
  audit log (absent)     no audit log — no channel has ever been claimed or released through THIS relay's control socket, so enrollment has no local record here · /tmp/obsdemo/rt/audit.log
2026-10-07T08:46:49Z  2h00m ago  recorded     crew-1   msg m-2 (user) from captain — … The relay's claim trail (audit.log) could not be read (no audit trail at …/audit.log — this relay has never enrolled a channel here (an older relay build, or another runtime dir)), so whether the relay was ever the delivery path for this channel is unknown — a missing hand-over line is not evidence
```

Questions it answers that previously had a **wrong** answer:

- **"Did we lose a message, or was it delivered without the relay?"** Before
  this, `--outcome unhanded` could not tell them apart, and the honest answer for
  a `--legacy-poll` agent was the more alarming one. Now the claim trail decides
  it, and the row names the alternative path.
- **"Is this silence a defect?"** A channel the relay never claimed is not a
  defect in the relay, and the row no longer implies one. The relay's
  enrollment state, when it matters, is `parlay liveness`'s and `parlay
  explain`'s question — this row says what it can prove and stops.
- **"Has the relay's delivery trail gone quiet because it stopped polling?"**
  The claim intervals are *in the same timeline*, as `enrolled` / `retired`
  rows, so the release that explains the missing hand-over is visible next to it.

### 9. An unmeasured value is no longer printed as a measured one (iteration 9)

Both bugs here were found by running the three surfaces against the **live**
fleet on this box, where a real running relay predates the ledger:

```
$ parlay liveness          # before
  relay                  read — up — polling , runtime
$ parlay timeline          # before
  relay control socket (read) up — polling , runtime
$ parlay explain eph-…     # before
relay           up — polling unknown, runtime unknown
registration    … the server did not answer …
delivery        no ledger on disk at …/delivery.log and the relay did not answer — …
```

The same three commands on the same live box after the change (real output, not
a fixture) — one running relay, one story:

```
$ parlay liveness
  relay                  read — up — polling unknown, runtime unknown — this relay's /health reported neither, so which server it polls is UNKNOWN, not a mismatch

$ parlay timeline --limit 2
  relay control socket (read) up — polling unknown, runtime unknown — this relay's /health reported neither, so which server it polls is UNKNOWN, not a mismatch

$ parlay explain eph-6ab41a36
relay           up — polling unknown, runtime unknown — this relay's /health reported neither, so which server it polls is UNKNOWN, not a mismatch
delivery        because the relay did not serve GET /delivery (it answered /health, so it was up; its build may predate the ledger, or that one request failed) — no ledger on disk at …/delivery.log: this relay has never recorded a delivery event; that is NOT the same as 'nothing was delivered'
```

Three surfaces, one running relay, three different stories. `liveness` and
`timeline` printed two empty strings where a sentence expected values, which
reads as a measurement (`polling <nothing>`); `explain` said `up` two rows above
`the relay did not answer`. Both are the same failure this whole objective is
about — a claim the fleet never made — so they are fixed together:

- **A `/health` that reports no bindings is UNKNOWN.** All three surfaces now
  render the relay's own answer through one helper (`relay_health_note.go`), so
  one running relay is described identically by all three:
  `up — polling unknown, runtime unknown — this relay's /health reported neither,
  so which server it polls is UNKNOWN, not a mismatch`. The last clause matters:
  an operator who reads "unknown" as "wrong server" will re-enroll a relay that
  is polling correctly, and a mismatch warning is only ever raised when both
  URLs are comparably parseable.
- **A live relay that serves no `/delivery` is not a dead one.** `explain`'s
disk fallback for the delivery ledger now chooses its reason from what was
observed: `because the relay did not answer` only when no relay answered at all,
and `because the relay did not serve GET /delivery (it answered /health, so it
was up; its build may predate the ledger, or that one request failed)` when the
relay is up. The trail on disk is read either way, and the relay row above it
now agrees with the delivery row.

Both fixes are pinned by tests that fail against the pre-change code (the
mutation and its red output are recorded under "Tests" below), and both
new outputs are captured verbatim in [`docs/explain.md`](docs/explain.md),
[`docs/liveness.md`](docs/liveness.md) and [`docs/timeline.md`](docs/timeline.md).

Questions it answers that previously had a **misleading** answer:

- **"Is this relay polling my server?"** `up — polling unknown, runtime
  unknown` says the question was not answered, and says it is *not* a mismatch.
  Before, `polling , runtime ` invited exactly the wrong conclusion.
  *(Iteration 10 went further on the same line: the relay's `/agents` answer
  carries the same bindings, so "unknown" now appears only when neither route
  reports them — see section 10.)*
- **"Is the relay dead?"** The delivery row and the relay row can no longer
  disagree in the same screen: a run that says `relay up` says why the trail
  came from disk instead.

### 10. A binding the relay reported is never printed as unknown (iteration 10)

Iteration 9 fixed an *unmeasured* value printed as a measurement. Running the
same three surfaces against the same live fleet again found the mirror image:
a **measured** value printed as unknown, in the one place where the answer was
most consequential.

The relay running on this box answers `/health` with `{"ok":true}` alone — but
it reports `server` and `runtime` on `/agents`, and **all three commands already
read `/agents`** (for relay enrollment). So the truth was in a response the
command had in hand:

```
$ parlay liveness          # before iteration 10 (live box)
  relay                  read — up — polling unknown, runtime unknown — this relay's /health reported neither, so which server it polls is UNKNOWN, not a mismatch
```

The cost was not the wording. The registered-but-deaf warning is a COMPARISON
(the relay's binding against the server this CLI targets), and it read
`/health`'s empty string — so for exactly the relay builds that keep their
bindings on `/agents`, a relay pointed at a different chat server produced no
warning at all. That is the failure mode the comparison exists for: the agent
looks live and receives nothing.

After, real output (three surfaces, one running relay, one story):

```
$ parlay liveness
  relay                  read — up — polling http://macbook:31337, runtime /var/folders/…/T/parlay (this relay's /health omitted the server and runtime; its /agents answer reported it) · WARNING this relay polls http://macbook:31337, NOT the server this CLI targets (http://localhost:4242)

$ parlay timeline --limit 1
  relay control socket (read) up — polling http://macbook:31337, runtime /var/folders/…/T/parlay (this relay's /health omitted the server and runtime; its /agents answer reported it) · WARNING this relay polls http://macbook:31337, NOT the server this CLI targets (http://localhost:4242): nothing sent to http://localhost:4242 reaches this relay

$ parlay explain danny
relay           up — polling http://macbook:31337, runtime /var/folders/…/T/parlay (this relay's /health omitted the server and runtime; its /agents answer reported it)
                WARNING: this relay polls http://macbook:31337, NOT the server this CLI targets (http://localhost:4242) — anything sent to http://localhost:4242 does not reach this relay
```

What the merge does, in one rule (`relaySelfOf`, `relay_health_note.go`):

- `/health` wins every field it carries — it is the route the caller asked for
  the bindings — and `/agents` fills only what `/health` left empty (both
  routes answer from the same relay fields, `r.server`/`r.runtimeDir`).
- A filled-in value is printed **with its provenance**: `(this relay's /health
  omitted the server and runtime; its /agents answer reported it)`. The
  ordinary case — `/health` carried both — gains no prose at all, which is
  pinned by a test, because a note that fires on every row is noise an operator
  learns to skip.
- When **neither** route reports a value, it stays UNKNOWN, and the reason is
  chosen from what was observed: `neither its /health nor its /agents answer
  reported …` when `/agents` answered and omitted it, versus `its /health
  reported neither, and its /agents answer was not read` when that route never
  answered. "Nobody asked" may not borrow the wording of "the relay refused".
  Verified verbatim against a private relay fixture whose `/health` is
  `{"ok":true}` and whose `/agents` reports no bindings either:

  ```
  relay                  read — up — polling unknown, runtime unknown — neither its /health nor its /agents answer reported the server or its runtime dir, so neither is known — which server it polls is UNKNOWN, not a mismatch
  ```

- Nothing was added to a delivery path: this is one pure helper plus a second
  read of a route each command already called, and the monitor never consults
  these bindings.

Tests (all in `tools/cli/internal/commands/relay_health_note_test.go`): the
shape table grew to nine cases including the three merge shapes and the
not-read-versus-refused wording; a unit test pins the precedence rule itself
(`/health` wins); and four end-to-end tests cover the merge and the recovered
warning on all three surfaces. Disabling the merge turns **9 tests red** (3
subtests of the shape table, the precedence unit test, and all four end-to-end
tests) — the exact list is under "Tests" below. The new tests reference the new
`relaySelfOf`/`relayAgentsBinding` symbols, so they do not even compile against
the pre-change tree.

Docs re-captured from the built binary: [`docs/liveness.md`](docs/liveness.md)
C5 (both shapes, plus why a silent warning is the real defect),
[`docs/explain.md`](docs/explain.md) (the disk-trail block, re-captured, and the
`/agents`-only warning), [`docs/timeline.md`](docs/timeline.md) (both shapes),
and the per-verb `HELP` text for all three verbs.


### 11. The relay's own restart is on the timeline, and it is not agent activity (iteration 11)

A restart is the one event that explains a gap in deliveries, and nothing
recorded it: the spool replay that re-registers every channel was invisible, so
a restart looked exactly like a quiet fleet — and the 2026-07-17 shape (19
agents deaf until hand re-enrolled) left no durable trace anywhere. The relay's
data-plane ledger gained two events, so it now has **six**:

- `started` — fleet-wide (it names no agent), written by `main()` **after the
  control socket binds** and before the spool replay. After the bind is what
  makes the row mean "this relay came up and served": a second boot refused the
  socket by a live relay records nothing (pinned end-to-end), and writing it
  before the replay keeps an append-only trail's read order equal to its write
  order, which the reader depends on.
- `resumed` — one row per channel the replay brought up (`resumeFromSpools`),
  including a channel `-agents` had already registered, so SILENCE is
  unambiguous. Per channel rather than a count on `started`, because the
  operator's real question is "WHICH channel did not come back", and a count
  cannot be diffed against the `delivery-ended reason=shutdown` rows.

`parlay timeline` classifies both as their own outcomes, so the 2am query is one
line: `parlay timeline --outcome started,resumed`. Verbatim from the built
binary against a private runtime dir and a refused port (`exit 0`) — two channels
stopped with the relay at 08:30, the relay came back at 08:31, and **`crew-1`
has a `resumed` row while `crew-2` does not**:

```
2026-10-07T08:00:00Z  3h35m ago  queued       crew-1            msg m-1 (user) — still in the agent's spool — …
2026-10-07T08:30:00Z  3h05m ago  ended        crew-1            the channel stopped being polled — reason=shutdown; 1 line(s) were still in the spool at that moment, unproven-consumed
2026-10-07T08:30:00Z  3h05m ago  ended        crew-2            the channel stopped being polled — reason=shutdown; 0 line(s) were still in the spool at that moment, unproven-consumed
2026-10-07T08:31:00Z  3h04m ago  started      -                 the relay process started here: it took its control socket and began serving. Deliveries cannot flow from a relay that is not running, so a gap between two of these lines is a RESTART, not a quiet fleet — and a burst of them is a crash loop. It names no channel: what came back is the `resumed` rows
2026-10-07T08:32:00Z  3h03m ago  resumed      crew-1            the relay resumed polling this channel at its start, from the spool it found on disk — so this channel HAD A POLL LOOP from this instant. It is not proof the agent was listening, and not proof any queued line was read
2026-10-07T08:34:00Z  3h01m ago  queued       crew-1            msg m-2 (user) — still in the agent's spool — …

  shown: queued=2 · ended=2 · started=1 · resumed=1
```

What the rows do NOT say, deliberately:

- **No verdict is made from a missing `resumed` row.** `crew-2` is left as its
  last known fact. A resume can fail (the error goes to the relay's own stderr,
  not into an identifier-only trail) and a ledger written before these events
  existed holds no such rows at all, so absence is a question to follow up, not
  a claim that a channel stayed deaf. Pinned by a test that asserts the words
  `never came back`, `did not come back`, `was not resumed` and `is deaf` appear
  nowhere in the output.
- **`resumed` is polling, never a read** — the sentence says so, and a test
  forbids `delivered`/`READ` in its text.
- **One restart row, no agent** (`started`), which is why `--outcome
  started,resumed` is the query rather than a fleet-wide row repeated per agent.

#### The consequence in `parlay liveness`: a restart is not agent activity

Both new rows name an agent in one case (`resumed`) or carry a fresh stamp, and
`liveness` measures `SILENT` over the newest dated record of ANY source — so
without a rule, a restarted relay would have reported a fleet that had been deaf
for three hours as active two minutes ago. **A relay-process event is therefore
not counted as the agent's activity**: `resumed` and `delivery-ended
reason=shutdown` are excluded from the clock (and `delivery-ended
channel-gone`/`unregister` are not — that is the channel really ending, the last
thing known about it), and the exclusion is stated on the row instead of being
left invisible. Verbatim (server refused, relay socket absent, this agent's
spooled traffic three hours old):

```
AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-1               unknown   unknown                3h00m      relay spooled 3h00m ago

notes
  crew-1               state      the server did not answer, so registration is unknown — this is not the same as offline
  crew-1               heartbeat  the server did not answer, so channel activity is unknown (not absent, and not fresh)
  crew-1               relay      the relay resumed polling this channel when it started at 2026-10-07T11:40:36Z — that is the RELAY's own event, not this agent's activity, so it was NOT counted toward the silence above: a restart writes one for every channel it was polling, and counting it would report a deaf fleet as freshly active
```

`SILENT` stays `3h00m` instead of collapsing to `0s`, the sentence is in the
text notes **and** in `--json` (`relay_note`), and `parlay explain` shows the
same pair for one agent — its `delivery` block reads `delivery ended —
reason=shutdown spoolLines=1`, then `resumed polling at relay start — the relay
registered this channel's poll loop then. Polling, not delivery: it does not say
the agent read anything`.

Tests and mutations:

- `tools/relay/startup_test.go` — `TestResumeFromSpoolsRecordsEveryChannelItBroughtUp`
  (one `resumed` row per channel brought up, none for a file the walk skipped,
  never a `started` row from the walk, every row stamped),
  `TestResumeRecordsAChannelAlreadyHeldByFlag` (silence stays unambiguous), plus
  the ledger assertions added to the existing end-to-end
  `TestControlSocketBindsBeforeSpoolResume` (`started` is the **first** line,
  exactly one of them, and all 40 channels have exactly one `resumed` row) and
  `TestABootThatCannotTakeTheSocketRecordsNoStart` (a second relay refused the
  socket records no new `started` row).
- `tools/cli/internal/timeline/timeline_test.go` — `TestRelayStartAndResumeAreTheirOwnOutcomes`
  (the fleet-wide row names no agent and says RESTART; `resumed` claims POLLING
  and must not contain `delivered`/`READ`).
- `tools/cli/internal/commands/timeline_test.go` — `TestTimelineTellsTheRestartStory`
  (end to end: the restart story on one axis, `--outcome started,resumed`
  narrowing to exactly two rows, the absence of any "did not come back" claim,
  and `--json` carrying `started`/`resumed` with the start agentless).
- `tools/cli/internal/commands/liveness_test.go` — `TestLivenessDoesNotCountTheRelaysOwnRestartAsAgentActivity`
  (SILENT measured from the agent's own traffic, the relay rows absent from the
  activity column, and the explanatory note present).
- Mutation-verified, each restoring green immediately after: removing both relay
  hooks (`r.recordStarted()`/`r.recordResumed()`) turns **4 relay tests red**
  (`no ledger at … after resuming two channels`, `delivery ledger … does not
  exist`, `first ledger line = [], want "started"`, `the first relay recorded 0
  "started" row(s), want 1`); disabling the two classification cases turns the
  timeline unit test red (`started outcome = "unknown", want "started"`) and the
  end-to-end restart test red; making `relayProcessEvent` return false turns the
  liveness test red (5 missing assertions, recorded above).

Docs: [`docs/relay.md`](docs/relay.md) (six events, per-event table, and a
fourth honest limit on silence), [`docs/timeline.md`](docs/timeline.md) (the
outcome rows plus an "A relay restart, verbatim" section),
[`docs/explain.md`](docs/explain.md) (the `resumed` row block),
[`docs/liveness.md`](docs/liveness.md) (section F, the exclusion table),
`tools/relay/NOTES.md`, and the per-verb `HELP` text for `timeline`, `explain`
and `liveness`.


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
| Relay not running (no control socket) | `relay  no answer at <sock> — the relay is not running (or is using another runtime dir), so relay enrollment is unknown; the delivery ledger is a FILE and is read from disk below`; `relay enroll  unknown — the relay did not answer GET /agents` — and the delivery section is **not** lost: see the four file-fallback rows below |
| Relay down, ledger on disk | `delivery  read from disk (<path>) because the relay did not answer — 3 of the last 20 ledger event(s), oldest first; whether recording is switched off right now is unknown`, then the rows in the ledger's own vocabulary. The disk rows feed `last error` too: `relay could not spool message m-9 — it never reached the agent (<ts>)` |
| Relay down, no ledger on disk | `delivery  because the relay did not answer — no ledger on disk at <path>: this relay has never recorded a delivery event; that is NOT the same as 'nothing was delivered'` |
| Relay down, ledger present but unreadable | `delivery  because the relay did not answer — its ledger at <path> exists but could not be read (<err>): the file is there and what it holds is unknown, not empty` |
| Live relay up, but its build serves no `/delivery` route (404) | the relay line says `up` and the trail on disk is still read, with the reason naming what happened instead of contradicting that line: `delivery  read from disk (<path>) because the relay did not serve GET /delivery (it answered /health, so it was up; its build may predate the ledger, or that one request failed) — 3 of the last 20 ledger event(s), oldest first; whether recording is switched off right now is unknown` (iteration 9; before it the same run said `because the relay did not answer`, two rows under a `relay up` line) |
| Running relay whose `/health` reports no bindings (an older build) | iteration 10: the value is taken from the relay's own `/agents` answer, with the route named — `relay  up — polling http://macbook:31337, runtime /var/folders/…/T/parlay (this relay's /health omitted the server and runtime; its /agents answer reported it) · WARNING this relay polls http://macbook:31337, NOT the server this CLI targets (http://localhost:4242)`. When BOTH routes are silent it stays unknown and says so once: `up — polling unknown, runtime unknown — neither its /health nor its /agents answer reported the server or its runtime dir, so neither is known — which server it polls is UNKNOWN, not a mismatch`, and when `/agents` was never read the reason changes to `its /health reported neither, and its /agents answer was not read`. (Iteration 9 gave the unknown wording; before that `liveness` and `timeline` printed `up — polling , runtime ` — two empty strings where a measurement belongs — and the mismatch warning was dropped for any relay that reports its bindings only on `/agents`) |
| Relay down, ledger read but quiet for this agent | `delivery  read from disk (<path>) because the relay did not answer — ledger present, no events for this agent; whether recording is switched off right now is unknown` (this is an ANSWER, so exit stays 0 — it is not "nothing was observable") |
| Relay down, ledger rotated / corrupt | `… oldest first · 1 of them from the generation before the last rotation (older history is in <path>.1) · 1 corrupt line(s) skipped`, plus the `rotated` marker row itself — `delivery.log.1` is read, so a shortened trail never reads as a quiet one |
| Live relay answers while a stale ledger sits on disk | the socket answer wins: the disk wording never appears and the file is not consulted (`TestExplainSocketAnswerBeatsAStaleLedgerOnDisk`) |
| Spool exists, no writer | `queue  2 line(s) queued in <spool>, unconfirmed-consumed (…); resume cursor m-2; no relay is answering, so these lines have no writer` |
| Missing resume cursor | `queue  2 line(s) queued in <spool>, unconfirmed-consumed (…); resume cursor NONE — a monitor resuming here replays this channel's backlog; no relay is answering, so these lines have no writer` (the cursor comes from the spool's own tail by the relay's rules — an id is required and the role must be `user`/`agent` — so `NONE` is a real absence, never a zero). Captured from the built CLI against a private runtime dir; also in [`docs/explain.md`](docs/explain.md) |
| Relay up, bound to another server | `WARNING: this relay polls http://somewhere-else:4242, NOT the server this CLI targets (http://127.0.0.1:60533) — anything sent to http://127.0.0.1:60533 does not reach this relay` |
| Ledger never written | `delivery  no ledger at <path> — this relay has never recorded a delivery event; that is NOT the same as 'nothing was delivered'` |
| Ledger switched off | `delivery  recording is OFF in the running relay (PARLAY_RELAY_DELIVERY_LOG=0) — nothing is being written to <path>` |
| Ledger rotated (history lossy) | `ledger rotated (size-cap) — history before this line lives in delivery.log.1` |
| Old relay, no `/delivery` route (404) | the socket read fails (a 404 is "could not ask", never an empty trail) and the **file fallback takes over** — a live old relay prints `because the relay did not serve GET /delivery …`, a dead one `because the relay did not answer`, and either way a ledger that exists still shows its rows |
| Heartbeat stale vs missing | `channel  last observed 2.0h ago (<stamp>)` **vs** `channel  row present, lastSeen absent — the server has never observed activity on this channel` **vs** `channel  no presence row in the server's snapshot — never observed on this channel (or not registered)` |
| Server unreachable | `registration  unknown — the server did not answer <url>`; `channel  unknown — …`; `commands  unknown — the server did not answer /api/chat/commands`; `crew state  working · source: status-degraded · … (relay unreachable; status may be stale)`. Exit stays **0** because the relay and the local records still answered. |
| Server unreachable, roster on disk | `registration  listed in the roster the server last persisted to disk (<path>) — name X, color Y — the server did not answer, so whether it is registered RIGHT NOW is unknown; that file holds no heartbeat either`; the `channel` line stays `unknown` (presence is never on disk) and `crew state` stays `status-degraded` (the live registry is its oracle). Exit 0. |
| Server unreachable, id NOT in that roster | `registration  not in the roster the server last persisted to disk (<path>) — the server did not answer, so this is the last roster it wrote, not a live answer` — deliberately not `NOT in the registry` |
| Server unreachable, no roster on disk | `registration  unknown — the server did not answer <url> and there is no roster file at <path> to fall back on (that absence is not a 'not registered') — either it has never enrolled an agent or it runs with a -state-dir other than <statehome>` |
| Server unreachable, roster unreadable | `registration  unknown — … its roster file at <path> exists but could not be read (<err>) — the file is there and what it holds is unknown, not empty` |
| Server unreachable, target is another machine | `registration  unknown — the server did not answer <url>, and the roster file on this host (<path>) was NOT consulted: the target is another machine, whose registry lives with it` |
| Server UP while a roster sits on disk | the live answer wins: `registration  registered — name …` / `NOT in the registry — the server answered and does not list it`, and no disk wording appears |
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
| Nothing observable at all (`timeline`) | stderr `parlay timeline: nothing was observable — no delivery ledger, no audit log, no chat history, the relay at <sock> did not answer, and no command registry at <url>`, exit **1**; the header says `no event matched. No record answered at all, so an empty timeline means nothing was observable — see sources.` instead of a bare empty list |
| Chat history absent (`timeline`) | `chat history (absent)  no history file here — either the server has never persisted a message, or it runs with a -state-dir other than <statehome>. Not the same as 'no message was ever sent'` |
| Chat history unreadable (`timeline`) | `chat history (unreadable)  could not read it (<err>) — the file exists and what it holds is unknown` |
| Chat history truncated (`timeline`) | `chat history (read)  … · TRUNCATED: only the newest records were read (the file is 9.4 MiB); older messages are not in this timeline` |
| History read of another host's state dir (`timeline`) | `… · WARNING this is the state dir of THIS host (<statehome>), and the CLI targets http://macbook:31337 — a server on another host keeps its own history there, so these records may be a different server's` |
| A recorded message with no hand-over, on a complete trail (`timeline`) | outcome `unhanded`: `the chat server persisted this message on this channel and the relay's delivery trail — read in full, with no rotation — holds no hand-over for it: NOTHING picked this message up. … The message is still in the agent's history, so it can be resent` |
| …on a trail that cannot prove it | outcome `recorded` with the guard: `NO delivery trail could be read, so whether the relay ever took it is unknown — not absent` (old relay) / `The delivery trail was read but it has rotated, … — a missing hand-over line is not evidence` / `The delivery trail was read but the file exceeded this reader's cap, …` / `younger than the hand-over window (1m30s) the relay is allowed before silence means something, so this is not counted as unhanded` / `The delivery trail begins at <ts>, AFTER this message` / `no dated line to date itself from` / `Its stamp does not parse` |
| …on a channel the relay never claimed (`timeline`) | outcome `recorded`: `The relay's claim trail (audit.log, read in full) holds NO claim for this channel at all — the relay was never the delivery path for it, so no hand-over line was ever going to exist. An agent can receive messages without the relay (`parlay listen --legacy-poll` polls the chat server directly), and what such a poll consumed is recorded nowhere in this fleet` — and `--outcome unhanded` matches nothing |
| …on a channel whose claim had ended (`timeline`) | outcome `recorded`: `The relay's claim trail (audit.log, read in full) shows the relay's last claim on this channel ended at <ts>, before this message was recorded — the relay was not the delivery path for it, so a missing hand-over line is not evidence: nothing in this fleet records what has no relay behind it, including a direct poll (`parlay listen --legacy-poll`)` |
| No claim trail at all (`timeline`) | outcome `recorded`: `The relay's claim trail (audit.log) could not be read (no audit trail at <path> — this relay has never enrolled a channel here (an older relay build, or another runtime dir)), so whether the relay was ever the delivery path for this channel is unknown — a missing hand-over line is not evidence`; the `audit log (absent)` source row names the same file |
| Claim trail truncated, or a claim with no readable stamp (`timeline`) | outcome `recorded`: `… exceeded this reader's cap, so its OLDEST claims are not in it — a missing claim for this channel is not evidence` / `… holds a claim for this channel whose time cannot be read, so whether it covers this message is unknown` |

`parlay liveness` keeps the same rule with a different set of sources — two of
its four degraded modes are about a record that is absent rather than stale:

| Degraded mode | What `parlay liveness` prints |
|---|---|
| Server unreachable | `registry + presence    unreachable — no answer from <url> — registration and channel activity are UNKNOWN, not absent (an unreachable server is not an empty fleet)`; per row `STATE unknown` with `the server did not answer, so registration is unknown — this is not the same as offline` and `HEARTBEAT unknown` with `the server did not answer, so channel activity is unknown (not absent, and not fresh)`. Exit stays **0** when a local record answered. |
| Server unreachable, roster on disk | `registry (disk)  read — read <path> because the server did not answer — N agent(s) in the roster the server last persisted, so registration below comes from DISK; presence is never written to disk, so every heartbeat stays unknown`; `STATE` is live/ghost again (the process table is a LOCAL measurement) with the substitution named in the row's note, `HEARTBEAT` stays `unknown` plus `presence is kept in memory by the server and is never written to disk, so there is no heartbeat record to fall back on` |
| Server unreachable, id not in that roster | `STATE unknown` with `not in the roster the server last persisted to disk, and the server did not answer — whether it is enrolled right now is unknown, not settled` (never `offline`) |
| Server unreachable, no roster on disk | `registry (disk)  absent — no roster file at <path> and the server did not answer — either it has never enrolled an agent or it runs with a -state-dir other than <statehome>. Not the same as 'no agent is registered'`; every `STATE` is `unknown`, exactly as before the fallback existed |
| Server unreachable, roster unreadable | `registry (disk)  unreadable — could not read it (parse <path>: …) — the file is there and what it holds is unknown, not empty` |
| Target is another machine | `registry (disk)  not-this-host — not consulted — the CLI targets another machine, whose registry lives with it; <path> belongs to this host and is not that server's roster`, and no agent from that file appears in the table |
| Process table unreadable | `process table          unreadable — the process table could not be read, so a dead listener cannot be ruled out — registered agents are NOT reported as ghosts on a failed probe`; every registered agent stays `STATE live` with `registered; the process table could not be read, so a listener cannot be confirmed OR ruled out` |
| Relay not running | `relay                  unreachable — no answer at <sock> — the relay is not running (or uses another runtime dir). Its delivery trail is a FILE and is still read below; only the relay's live state is unknown`; `LAST OBSERVED ACTIVITY` still comes from the ledger (`relay spool-failed 1h30m ago`) |
| Relay bound to another server | the relay line gains `· WARNING this relay polls <other>, NOT the server this CLI targets (<url>)` |
| The relay restarted (its own rows on this channel) | `resumed` and `delivery-ended reason=shutdown` are **not** counted toward `SILENT` or shown as activity, and the row carries `relay      the relay resumed polling this channel when it started at <ts> — that is the RELAY's own event, not this agent's activity, so it was NOT counted toward the silence above: a restart writes one for every channel it was polling, and counting it would report a deaf fleet as freshly active`. The same sentence travels in `--json` as `relay_note`. |
| Heartbeat expired vs absent | `expired (3h00m ago)` **vs** `never observed` (a presence row with no `lastSeen`) **vs** `no row` (no presence row at all) **vs** `unknown` (the server did not answer, or the stamp does not parse). Only the two ages carry a parsed stamp at all. |
| No dated record anywhere | `SILENT unknown` plus `no dated activity record exists (looked at: <what was consulted>) — silence is unmeasurable here, not zero` |
| No local home for the id | `no agent home for this id on this host, so its status file could not be consulted either — run this where the agent runs to see local activity` |
| Ledger never written | `delivery ledger        absent — no ledger — this relay has never recorded a delivery event. That is NOT the same as 'nothing was delivered': an older relay build has no ledger at all` |
| Nothing observable at all | `no agents to report — the server lists none and this host has no agent homes`, stderr `parlay liveness: nothing was observable — the server did not answer at <url> and no relay trail or agent home exists under <agents-root>`, exit **1** |
| `--silent` with nothing to show | `no agent is silent beyond 10m0s, and none is missing a heartbeat record` — an explicit empty, not a bare table |

Exit codes: `0` at least one source answered (including bad news), `1` nothing
observable, `2` usage. An unknown flag is a hard exit, never silently ignored.

## Tests

- `tools/cli/internal/timeline/enrollment_test.go` (iteration 8) — the claim
  intervals themselves, with no files or clock: a message stamped exactly at the
  `register` or the `unregister` is INSIDE the relay's window (inclusive at both
  ends, because getting that backwards accuses the relay of losing a message it
  was claiming at the time); a channel claimed, released and re-claimed uses the
  LATEST release, so the first interval's end cannot be read as the second's;
  another channel's claim never answers for this one; and the four
  cannot-tell shapes (never read, an unreadable trail with its reason, a
  truncated one, an undated claim) each come back not-claimed WITH a reason
  rather than silently claimed.
- `tools/cli/internal/timeline/history_test.go` (iteration 8 additions) — five
  new rows in the guard table: `claim trail never read (enrollment unknown)`,
  `claim trail truncated`, `channel never claimed (a direct poll)`, `claim ended
  before the message`, `claim exists but cannot be dated` — each must land on
  `recorded` and name itself. The `trail()` helper now supplies a claim covering
  the fixture stamp, so the two tests that assert `unhanded` DO fire also pin
  that the guard did not simply turn the verdict off.
- `tools/cli/internal/commands/timeline_history_test.go` (iteration 8) —
  end-to-end against private fixtures: the direct-poll case (the relay is
  enrolled for a DIFFERENT channel, so absence of a claim for this one is
  positive evidence) staying `recorded` and `--outcome unhanded` matching
  nothing; a claim released before the message; no `audit.log` at all
  (enrollment unknown, with the `audit log (absent)` source row naming the file);
  and the two tests that assert `unhanded` still does fire now write the claim
  that licenses it.
- **Tests that bite (iteration 9).** `tools/cli/internal/commands/relay_health_note_test.go`
(four tests, eleven assertions) is the pin for the absence-as-value fix, and two
mutations each turn it red — both mutations were applied to a copy of the tree,
run, and reverted:

- making `relayHealthNote` return the pre-change `"polling " + h.Server +
  ", runtime " + h.Runtime` turns all four tests red (`output missing "polling
  unknown, runtime unknown"`, `output must NOT contain "polling , runtime"`,
  `output missing "up — polling unknown, runtime unknown"` — in the unit test,
  `liveness` and `timeline`);
- pinning `explain`'s disk-fallback reason back to `"because the relay did not
  answer"` turns `TestExplainSaysWhenTheRelayAnsweredButServedNoDelivery` red
  (`output missing "read from disk (<path>) because the relay did not serve GET
  /delivery"`, `output missing "it answered /health, so it was up"`, `output must
  NOT contain "because the relay did not answer"`).

Both mutations also fail against the untouched pre-change tree: the quoted
strings above are exactly what the three surfaces printed on the live fleet
before this iteration.

**Tests that bite (iteration 8).** Three mutations each turn tests red:
  dropping the `Enrollment.ClaimAt` guard from `classifyHistory` turns the five
  guard-table cases AND all three new end-to-end tests red (the pre-change
  behaviour is exactly the false accusation); making `ClaimAt` always answer
  "claimed" turns the four pure `ClaimAt` tests, the five guard cases and the
  direct-poll end-to-end test red; and making `unregister` never close a claim
  turns the claim-ended test red. The pre-change tree, run against the same
  fixtures, reports `outcome unhanded — NOTHING picked this message up` for a
  message the relay never had a path to.
- `tools/cli/internal/commands/registry_file_test.go` (iteration 7) — the roster
  fallback end-to-end for BOTH verbs over a private state home: the fleet table
  with `STATE` answered from disk (`live` from the process table, `ghost` for a
  rostered agent with nothing listening) while `HEARTBEAT` stays `unknown`;
  absent / unreadable / `not-this-host` roster files, each with its own note and
  with no agent called `offline` off a failed read; an id missing from the
  roster staying `unknown`; the same four shapes on `explain`'s `registration`
  line with `channel` still `unknown` and `crew state` still `status-degraded`;
  the live answer beating a stale roster on disk; the roster counting as an
  observable source for the exit code; and a byte-identical roster after both
  verbs run.
- `tools/cli/internal/agentregistry/agentregistry_test.go` (iteration 7) — the
  reader and the locality gate: file order kept and id-less entries counted
  rather than dropped; empty roster is a read; a non-array or unparseable file is
  `unreadable`, never an empty fleet; the decode target's field list is asserted
  (no field for the roster's arbitrary `caps` blob); and `ServesThisHost` for
  loopback (any 127/8, `::1`, `localhost`), this host's name with the mDNS
  `.local` suffix folded and case-insensitive, and a remote FQDN or bind address
  declined.
- `tools/cli/internal/liveness/liveness_test.go` (iteration 7 additions) — the
  classifier half: a rostered agent read off disk stays `live`/`ghost` with the
  substitution in the note; the heartbeat stays `unknown` and the note says
  presence is never written to disk; a missing id is `unknown` with
  `unknown, not settled`; and the ordinary path is unchanged (no note fires on a
  healthy registered agent with a fresh stamp).
- **Tests that bite (iteration 7).** Three mutations each turn tests red:
  dropping the roster read from `explain`'s gather (6 tests), disabling the
  disk-roster branch in `liveness`'s gather (3 tests), removing the
  `unknown-not-offline` classification branch (1 test), and returning `read`
  instead of `unreadable` for an unparseable roster (3 tests across two
  packages).
- `tools/cli/internal/commands/explain_delivery_test.go` (iteration 6) — the
  trail read off disk when the relay does not answer, end-to-end against a
  private runtime dir: the full three-row story in the ledger's own vocabulary
  with the substitution named and the `spool-failed` row promoted into
  `last error`; another agent's events excluded; rotation (both generations,
  oldest-first, with the coverage note and the `rotated` marker) and a corrupt
  line counted rather than fatal; an unreadable ledger as "unknown, not empty";
  a ledger read but quiet for this agent as an ANSWER (exit 0, not "nothing was
  observable"); a live relay beating a stale ledger in the same runtime dir
  (with the GET-only assertion); `spooled` never relabelled; half-unreadable
  trails in both directions (active unreadable / rotated unreadable) keeping the
  rows that WERE read and naming the generation that failed; and the coverage
  caveats a synthetic trail cannot produce without a 16 MiB file.
- **Tests that bite (iteration 6).** Removing the disk fallback (leaving the
  socket-only read) turns SIX tests red — four in
  `explain_delivery_test.go`, the never-relabel test, and the updated
  `TestExplainRelayDownStillCoversTheServerHalf` — with the failure text being
  exactly the old lie: `output must NOT contain "delivery        unknown"`,
  `output must NOT contain "is not observable from here"`, and
  `exited 1; the ledger on disk was read, so something was observable`.

- `tools/cli/internal/chathistory/chathistory_test.go` — the server-history
  reader: the record type has no body field (an assertion on the STRUCT, so a
  later field addition reddens it) and no body reaches a decoded record; order
  is oldest-first; absent is not empty and unreadable is named; a channel-less
  message is counted but never listed (it is not addressed to an agent); junk
  lines are skipped and counted; the record cap keeps the NEWEST and says it
  truncated; and a >8 MiB file is tail-read with the partial line at the seek
  boundary discarded rather than counted as corrupt.
- `tools/cli/internal/timeline/history_test.go` — the classification of the
  server's records against the relay's trail: recorded-plus-hand-over points at
  the hand-over; recorded-with-no-hand-over on a complete trail is `unhanded`;
  a table of eight guards (no evidence object, no trail, rotated, truncated, no
  clock, too recent, trail starts later, no dated line) each stays `recorded`
  and names itself; an unparseable stamp is never judged; a FAILED hand-over
  counts as handed (that is the `dropped` line's story, and two contradictory
  verdicts on one message id would be worse than either); a hand-over for
  another agent does not clear this one; and the two new outcomes are members
  of the closed vocabulary (while `delivered` stays out of it).
- `tools/cli/internal/commands/timeline_history_test.go` — the same rules
  end-to-end against private fixtures: the full three-line story with message
  bodies present in the file and absent from the output; `--outcome unhanded`
  selecting exactly one row; a five-second-old message staying `recorded`; an
  old relay with no ledger producing no accusation at all (and matching
  nothing); a rotated ledger; history absent vs unreadable; the
  another-host warning; and both new members travelling verbatim through
  `--json`.
- **Tests that bite (iteration 5).** Five separate mutations each turn tests
  red: disabling the `unhanded` verdict (2 package-`timeline` tests + 2
  end-to-end tests, including the `--json` one), deleting the "trail is not
  complete" guard (2 tests), keying the hand-over index by message only
  instead of agent+message (1 test), no longer counting channel-less messages
  (1 test), and dropping the partial-line discard at the seek boundary (1 test).
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
- **Iteration 10's regression tests.** `relay_health_note_test.go` holds a
  nine-shape unit table for the two routes that carry the relay's bindings, a
  unit test for the precedence rule, and four end-to-end tests (liveness merge,
  liveness recovered warning, timeline merge + warning, explain merge +
  warning). Disabling the merge (`if a == nil` → `if true`) in `relaySelfOf`
  turns **NINE checks red**, and the list is the evidence that each surface is
  covered rather than one shared code path being asserted once:

  ```
  --- FAIL: TestRelayHealthNoteNeverPrintsAnEmptyValue (0.00s)
      --- FAIL: TestRelayHealthNoteNeverPrintsAnEmptyValue/both_reported_by_/agents (0.00s)
      --- FAIL: TestRelayHealthNoteNeverPrintsAnEmptyValue/server_only_from_/agents (0.00s)
      --- FAIL: TestRelayHealthNoteNeverPrintsAnEmptyValue/runtime_only_from_/agents (0.00s)
  --- FAIL: TestRelaySelfOfPrefersHealthAndFillsOnlyWhatItLeftEmpty (0.00s)
  --- FAIL: TestLivenessTakesBindingsFromAgentsWhenHealthOmitsThem (0.00s)
  --- FAIL: TestLivenessWarnsWhenOnlyAgentsNamesAnotherServer (0.00s)
  --- FAIL: TestTimelineTakesBindingsFromAgentsWhenHealthOmitsThem (0.00s)
  --- FAIL: TestExplainWarnsWhenOnlyAgentsNamesAnotherServer (0.00s)
  ```

  The one shape that stays green under that mutation is the both-routes-silent
  test, which is correct: it pins the wording of an unknown value and does not
  depend on a value being merged.
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

- **Tests that bite (iteration 11).** The four relay tests, the timeline unit
  test, the end-to-end restart test and the liveness exclusion test are listed
  with their mutation evidence in section 11: removing both relay hooks turns 4
  red, disabling the two classification cases turns 2 red (one per package), and
  making `relayProcessEvent` false turns the liveness test red with 5 missing
  assertions recorded verbatim. The liveness test is the one that fails if a
  relay restart is ever counted as an agent's activity again.

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
form the stop condition names.

### Iteration 11, verbatim

The full chain re-run on the FINAL tree of this iteration (after the last edit
of the iteration), each step's exit code captured from a subshell with **no
pipe** in front of it (a `cmd | tail` reports tail's status — the bug recorded
in iterations 6 and 9), and `-count=1` so nothing is `(cached)` in the module
runs:

```
$ ( cd tools/cli && go build ./... ); echo build=$?; ( cd tools/cli && go vet ./... ); echo vet=$?; ( cd tools/cli && go test -count=1 ./... ) > log; echo test=$?
tools/cli  build=0 vet=0 test=0  (32 package(s) ok, 0 FAIL line(s))
tools/relay  build=0 vet=0 test=0  (1 package(s) ok, 0 FAIL line(s))
packages/go-server  build=0 vet=0 test=0  (11 package(s) ok, 0 FAIL line(s))
packages/spawn-profiles  build=0 vet=0 test=0  (1 package(s) ok, 0 FAIL line(s))
gofmt  exit=0  unformatted file(s)=0
make test-bdd  exit=0
17 scenarios (17 passed)
55 steps (55 passed)
PASS
ok  	github.com/trillium/parlay/tools/cli/internal/evalengine	(cached)
7 scenarios (7 passed)
21 steps (21 passed)
PASS
ok  	github.com/trillium/parlay/tools/cli/internal/spawn	(cached)
```

Under the race detector (needs the ICU include/lib flags on this macOS box, as
recorded in iteration 4's learnings):

```
=== -race on the touched packages ===
ok  	github.com/trillium/parlay/tools/cli/internal/commands	47.158s
ok  	github.com/trillium/parlay/tools/cli/internal/timeline	1.362s
ok  	github.com/trillium/parlay/tools/cli/internal/relayctl	1.927s
ok  	github.com/trillium/parlay/tools/cli/internal/help	1.599s
ok  	github.com/trillium/parlay/tools/relay	4.446s
```

And the literal stop condition at the repo root is unchanged, by shape:

```
$ go build ./...
pattern ./...: directory prefix . does not contain main module or its selected dependencies
root build exit=1
```

**Known-red baseline: none.** `make test-bdd` (17 scenarios / 55 steps and 7
scenarios / 21 steps) is green on this box, as it was before iteration 1; the
root-command failure above is the four-modules-no-`go.work` shape, not a red
test.

The chain below was re-run end-to-end on iteration 10's tree (each step's
**true** exit code — no pipe swallowing it — and `make test-bdd` at the end),
pasted verbatim:

```
===== MODULE tools/cli =====
build tools/cli exit=0
vet tools/cli exit=0
test tools/cli exit=0
ok  	github.com/trillium/parlay/tools/cli/internal/timeline	(cached)
ok  	github.com/trillium/parlay/tools/cli/internal/wire	(cached)
ok  	github.com/trillium/parlay/tools/cli/internal/worktreeliveness	(cached)
===== MODULE tools/relay =====
build tools/relay exit=0
vet tools/relay exit=0
test tools/relay exit=0
ok  	github.com/trillium/parlay/tools/relay	(cached)
===== MODULE packages/go-server =====
build packages/go-server exit=0
vet packages/go-server exit=0
test packages/go-server exit=0
ok  	parlay/go-server/internal/sourcecontracts	(cached)
ok  	parlay/go-server/internal/static	(cached)
ok  	parlay/go-server/internal/store	(cached)
===== MODULE packages/spawn-profiles =====
build packages/spawn-profiles exit=0
vet packages/spawn-profiles exit=0
test packages/spawn-profiles exit=0
ok  	parlay/spawn-profiles/cmd/validate	(cached)
gofmt clean (whole tree)
===== make test-bdd =====
make test-bdd exit=0
17 scenarios (17 passed)
55 steps (55 passed)
--- PASS: TestFeatures (0.03s)      # evalengine
7 scenarios (7 passed)
21 steps (21 passed)
--- PASS: TestFeatures (0.24s)      # spawn
===== CHAIN fail=0 =====
```

(The three tailed `ok` lines are only the last three of the `tools/cli` run;
`go test ./...` there reports every package, and its exit code — 0 — is the
whole-package verdict. `(cached)` means Go validated that exact package content,
not that the tests were skipped. The `#` comments in the bdd block are mine: the
runner prints both suites under the same test name.)

And under the race detector (needs the ICU include/lib flags on this macOS box,
as recorded in iteration 4's learnings):

```
=== -race on the touched packages (./internal/commands/ ./internal/help/) ===
ok  	github.com/trillium/parlay/tools/cli/internal/commands	36.249s
ok  	github.com/trillium/parlay/tools/cli/internal/help	1.306s
```

At the repo root the literal stop condition is still:

```
=== root: go build ./... (expected to fail: 4 modules, no root go.work) ===
pattern ./...: directory prefix . does not contain main module or its selected dependencies
--- root build exit=1
=== root: gofmt -l . (empty list = pass) ===
--- gofmt exit=0
```

Older per-package listing (iteration 8, kept for the per-package detail):

```
=== root: go build ./... (expected to fail: 4 modules, no root go.work) ===
pattern ./...: directory prefix . does not contain main module or its selected dependencies
--- root build exit=1
=== root: gofmt -l . (empty list = pass) ===
--- gofmt exit=0
=== tools/cli go build === OK
--- tools/cli build exit=0
=== tools/cli go vet ===
--- tools/cli vet exit=0
=== tools/cli go test ===
ok  	github.com/trillium/parlay/tools/cli	0.734s
ok  	github.com/trillium/parlay/tools/cli/internal/agentregistry	0.809s
ok  	github.com/trillium/parlay/tools/cli/internal/args	1.007s
ok  	github.com/trillium/parlay/tools/cli/internal/capability	1.185s
ok  	github.com/trillium/parlay/tools/cli/internal/chathistory	1.718s
ok  	github.com/trillium/parlay/tools/cli/internal/cityscaffold	1.566s
ok  	github.com/trillium/parlay/tools/cli/internal/commandreport	1.754s
ok  	github.com/trillium/parlay/tools/cli/internal/commands	49.026s
ok  	github.com/trillium/parlay/tools/cli/internal/config	2.413s
ok  	github.com/trillium/parlay/tools/cli/internal/crewevents	2.539s
ok  	github.com/trillium/parlay/tools/cli/internal/evalengine	14.123s
ok  	github.com/trillium/parlay/tools/cli/internal/format	2.302s
ok  	github.com/trillium/parlay/tools/cli/internal/gctemplate	2.292s
ok  	github.com/trillium/parlay/tools/cli/internal/help	2.286s
ok  	github.com/trillium/parlay/tools/cli/internal/httpc	2.241s
ok  	github.com/trillium/parlay/tools/cli/internal/identity	21.370s
?   	github.com/trillium/parlay/tools/cli/internal/juggle	[no test files]
ok  	github.com/trillium/parlay/tools/cli/internal/liveness	2.281s
ok  	github.com/trillium/parlay/tools/cli/internal/monitor	3.194s
ok  	github.com/trillium/parlay/tools/cli/internal/parlaybeads	2.122s
ok  	github.com/trillium/parlay/tools/cli/internal/procscan	2.037s
ok  	github.com/trillium/parlay/tools/cli/internal/relayctl	1.890s
ok  	github.com/trillium/parlay/tools/cli/internal/resolvehandoff	19.453s
ok  	github.com/trillium/parlay/tools/cli/internal/robotswatch	14.198s
ok  	github.com/trillium/parlay/tools/cli/internal/routing	2.064s
ok  	github.com/trillium/parlay/tools/cli/internal/sayguard	1.574s
ok  	github.com/trillium/parlay/tools/cli/internal/sourcecontract	1.444s
ok  	github.com/trillium/parlay/tools/cli/internal/spawn	22.543s
ok  	github.com/trillium/parlay/tools/cli/internal/staleness	1.459s
ok  	github.com/trillium/parlay/tools/cli/internal/supersession	1.397s
?   	github.com/trillium/parlay/tools/cli/internal/testsupport	[no test files]
ok  	github.com/trillium/parlay/tools/cli/internal/timeline	1.418s
ok  	github.com/trillium/parlay/tools/cli/internal/wire	1.600s
ok  	github.com/trillium/parlay/tools/cli/internal/worktreeliveness	1.470s
--- tools/cli test exit=0
=== tools/relay go build === OK
--- tools/relay build exit=0
=== tools/relay go vet ===
--- tools/relay vet exit=0
=== tools/relay go test ===
ok  	github.com/trillium/parlay/tools/relay	2.942s
--- tools/relay test exit=0
=== packages/spawn-profiles go build === OK
--- packages/spawn-profiles build exit=0
=== packages/spawn-profiles go vet ===
--- packages/spawn-profiles vet exit=0
=== packages/spawn-profiles go test ===
ok  	parlay/spawn-profiles/cmd/validate	0.274s
--- packages/spawn-profiles test exit=0
=== packages/go-server go build === OK
--- packages/go-server build exit=0
=== packages/go-server go vet ===
--- packages/go-server vet exit=0
=== packages/go-server go test ===
ok  	parlay/go-server/cmd/parlay-server	0.732s
ok  	parlay/go-server/internal/atomicfile	0.974s
ok  	parlay/go-server/internal/bus	7.106s
ok  	parlay/go-server/internal/capability	0.949s
ok  	parlay/go-server/internal/guard	1.230s
ok  	parlay/go-server/internal/handlers	16.643s
ok  	parlay/go-server/internal/linkrewrite	1.849s
ok  	parlay/go-server/internal/remoteinput	6.188s
ok  	parlay/go-server/internal/sourcecontracts	2.289s
ok  	parlay/go-server/internal/static	2.084s
ok  	parlay/go-server/internal/store	2.206s
--- packages/go-server test exit=0
=== make test-bdd ===
21 steps ([32m21 passed[0m)
243.194458ms
--- PASS: TestFeatures (0.24s)
    --- PASS: TestFeatures/Spawning_the_same_agent_id_twice_is_rejected (0.10s)
    --- PASS: TestFeatures/Stopping_a_spawned_agent_leaves_no_live_process (0.11s)
    --- PASS: TestFeatures/No_accounts_configured (0.00s)
    --- PASS: TestFeatures/A_token_stored_under_one_account_name_is_not_found_under_a_different_name (0.00s)
    --- PASS: TestFeatures/An_account_missing_from_the_accounts_file_is_not_resolved (0.00s)
    --- PASS: TestFeatures/Resolution_of_a_stored_account_without_a_keychain_entry_fails (0.03s)
    --- PASS: TestFeatures/The_default_working_directory_is_the_current_user's_home (0.00s)
PASS
ok  	github.com/trillium/parlay/tools/cli/internal/spawn	(cached)
--- make test-bdd exit=0
=== VERIFY DONE ===
```

`-race` on the packages this iteration touched is green as well (CI's Go job runs
`-race` by default). On this box that needs the ICU cgo flags the beads
dependency's embedded-Dolt tree wants:

```
$ cd tools/cli && CGO_ENABLED=1 \
    CGO_CFLAGS=-I/opt/homebrew/opt/icu4c/include \
    CGO_CXXFLAGS=-I/opt/homebrew/opt/icu4c/include \
    CGO_LDFLAGS=-L/opt/homebrew/opt/icu4c/lib \
    go test -race -count=1 ./internal/timeline/ ./internal/commands/
ok  github.com/trillium/parlay/tools/cli/internal/timeline      1.273s
ok  github.com/trillium/parlay/tools/cli/internal/commands     35.642s
$ echo $?
0
```

Without those flags `-race` on `tools/cli` dies at compile time in
`github.com/dolthub/go-icu-regex/internal/icu` (`unicode/regex.h` not found),
which is a **pre-existing environment gap on macOS**, not a red test: CI's
ubuntu runner has the headers. Plain `CGO_ENABLED=0 go test` needs no flags and
is what the chain above uses.

**Re-run on iteration 9's tree (same per-module chain, `-count=1`, each step's
true exit code captured in a subshell with no pipe swallowing it):**

```
=== root: go build ./... (expected to fail: 4 modules, no root go.work) ===
pattern ./...: directory prefix . does not contain main module or its selected dependencies
--- root build exit=1
=== root: gofmt -l . (empty list = pass) ===
(nothing listed)
--- gofmt exit=0
=== tools/cli go build ===
--- tools/cli build exit=0
=== tools/cli go vet ===
--- tools/cli vet exit=0
=== tools/cli go test ===
ok  github.com/trillium/parlay/tools/cli  0.546s
ok  github.com/trillium/parlay/tools/cli/internal/agentregistry  0.825s
ok  github.com/trillium/parlay/tools/cli/internal/args  0.630s
ok  github.com/trillium/parlay/tools/cli/internal/capability  1.010s
ok  github.com/trillium/parlay/tools/cli/internal/chathistory  1.560s
ok  github.com/trillium/parlay/tools/cli/internal/cityscaffold  1.393s
ok  github.com/trillium/parlay/tools/cli/internal/commandreport  1.580s
ok  github.com/trillium/parlay/tools/cli/internal/commands  53.852s
ok  github.com/trillium/parlay/tools/cli/internal/config  1.763s
ok  github.com/trillium/parlay/tools/cli/internal/crewevents  2.306s
ok  github.com/trillium/parlay/tools/cli/internal/evalengine  14.034s
ok  github.com/trillium/parlay/tools/cli/internal/format  2.233s
ok  github.com/trillium/parlay/tools/cli/internal/gctemplate  2.237s
ok  github.com/trillium/parlay/tools/cli/internal/help  2.227s
ok  github.com/trillium/parlay/tools/cli/internal/httpc  2.188s
ok  github.com/trillium/parlay/tools/cli/internal/identity  21.590s
ok  github.com/trillium/parlay/tools/cli/internal/liveness  2.219s
ok  github.com/trillium/parlay/tools/cli/internal/monitor  3.479s
ok  github.com/trillium/parlay/tools/cli/internal/parlaybeads  2.048s
ok  github.com/trillium/parlay/tools/cli/internal/procscan  1.964s
ok  github.com/trillium/parlay/tools/cli/internal/relayctl  1.842s
ok  github.com/trillium/parlay/tools/cli/internal/resolvehandoff  19.556s
ok  github.com/trillium/parlay/tools/cli/internal/robotswatch  13.944s
ok  github.com/trillium/parlay/tools/cli/internal/routing  1.833s
ok  github.com/trillium/parlay/tools/cli/internal/sayguard  1.532s
ok  github.com/trillium/parlay/tools/cli/internal/sourcecontract  1.398s
ok  github.com/trillium/parlay/tools/cli/internal/spawn  23.862s
ok  github.com/trillium/parlay/tools/cli/internal/staleness  1.451s
ok  github.com/trillium/parlay/tools/cli/internal/supersession  1.538s
ok  github.com/trillium/parlay/tools/cli/internal/timeline  1.398s
ok  github.com/trillium/parlay/tools/cli/internal/wire  1.401s
ok  github.com/trillium/parlay/tools/cli/internal/worktreeliveness  1.506s
--- tools/cli test exit=0
=== tools/relay go build ===
--- tools/relay build exit=0
=== tools/relay go vet ===
--- tools/relay vet exit=0
=== tools/relay go test ===
ok  github.com/trillium/parlay/tools/relay  2.817s
--- tools/relay test exit=0
=== packages/spawn-profiles go build ===
--- packages/spawn-profiles build exit=0
=== packages/spawn-profiles go vet ===
--- packages/spawn-profiles vet exit=0
=== packages/spawn-profiles go test ===
ok  parlay/spawn-profiles/cmd/validate  0.220s
--- packages/spawn-profiles test exit=0
=== packages/go-server go build ===
--- packages/go-server build exit=0
=== packages/go-server go vet ===
--- packages/go-server vet exit=0
=== packages/go-server go test ===
ok  parlay/go-server/cmd/parlay-server  0.230s
ok  parlay/go-server/internal/atomicfile  0.885s
ok  parlay/go-server/internal/bus  7.150s
ok  parlay/go-server/internal/capability  0.798s
ok  parlay/go-server/internal/guard  1.043s
ok  parlay/go-server/internal/handlers  16.181s
ok  parlay/go-server/internal/linkrewrite  1.405s
ok  parlay/go-server/internal/remoteinput  6.218s
ok  parlay/go-server/internal/sourcecontracts  1.784s
ok  parlay/go-server/internal/static  1.964s
ok  parlay/go-server/internal/store  2.152s
--- packages/go-server test exit=0
=== make test-bdd ===
--- PASS: TestFeatures (0.03s)      # evalengine: 17/17 scenarios
--- PASS: TestFeatures (0.24s)      # spawn: 7/7 scenarios
--- make test-bdd exit=0
=== -race on the touched packages ===
--- race exit=0                     # ./internal/commands/ ./internal/help/
=== VERIFY DONE overall=0 ===
```

Every line above is the real output of one run of the chain inside each module
(each step's own exit code captured in a subshell, no pipe swallowing it), with
the `//` comments in the bdd block added by hand to say which feature set each
`--- PASS: TestFeatures` belongs to — the runner prints both suites under the
same test name. The exit=1 on the root build is the same repository-shape fact as
before (four modules, no root `go.work`): the command named in the stop
condition cannot exit zero here, and forcing it with a root `go.work` would
break CI's own module-shape gate and drop >2 MiB binaries into the repo root.
**Known-red baseline: none.** `make test-bdd` was green on this box before this
iteration's work and after it; the literal root-command failure above is a
repository-shape fact (four modules, no root `go.work`), not a red test, and it
was the same before iteration 1.

## Line budget

This repository enforces no per-file line budget (only a 2 MiB tracked-blob
ceiling and a docs-index gate) — the 250-line cap on every new **production**
file is my choice. Iteration 11 added **no new file at all** (23 files touched,
22 changed): the production change is 35 lines in `relay_delivery.go`
(242 → 277, an existing file that was already near the cap, not split because
the six events and their limits read as one unit — stated rather than hidden),
20 in `main.go` (182 → 202, the hook plus the reason it sits after the bind),
3 switch cases in `timeline/build.go` (243) and 8 lines in `timeline.go` (231),
the exclusion maps and its two helpers in `liveness_sources.go` (248 → 291,
existing, already over the choice), one note in `liveness_render.go` (178),
the field in `liveness.go` (184) and `liveness_json.go` (107). Everything else
is tests (in the range this package's test files already occupy: `startup_test.go`
206 → 431, `liveness_test.go` 531 → 569, `timeline_test.go` 489 → 555) and prose.
Iteration 10 added no new file at all: `commands/relay_health_note.go`
(39 → 128, still well inside the cap) gained the merge and its provenance
wording, and the change touched three existing call sites — `explain.go`
(252 → 261) and `explain_render.go` (220, the renderer now reads the merged
view), `liveness_sources.go` (236 → 248, the two control-socket reads are
reordered so the merge happens before the source note is written), and
`timeline_sources.go` (249 → 258) — the last two above the 250 choice on
existing files, deliberately not split mid-edit for the same reason as before.
`relay_health_note_test.go` (135 → 273) is a test file in the range the
package's other test files already occupy. Iteration 9: `commands/relay_health_note.go` (39, the one
rendering of a live relay's self-reported bindings, shared by `explain`,
`liveness` and `timeline`) is a new production file far inside the cap, and the
iteration's production change is three one-line call sites plus ~20 changed
lines in `explain_render_detail.go` (282, an existing file already over the cap
and left unsplit for the same reason as before).
`commands/relay_health_note_test.go` (135) is a new test file in the range the
package's other test files occupy. Iteration 7: `internal/agentregistry/agentregistry.go` (166,
the reader and the locality gate) and `commands/registry_file.go` (85, the one
read both verbs share) are new production files inside the cap; the change also
added ~20 lines to `explain.go` (228 → 252, an existing file that was already
near the cap — the same treatment `explain_render_detail.go` at 271 got in
iteration 2: not split, because splitting a renderer mid-edit costs more than
the prose it saves), the disk-roster branch (~20) in `liveness_sources.go` (236),
and the vocabulary member in `timeline_sources.go` (245, both still inside).
`commands/registry_file_test.go` (317) and `agentregistry_test.go` (131) follow
the package convention rather than the production cap, as every earlier test
file here does. Iteration 8: `internal/timeline/enrollment.go` (93, the claim
intervals and `ClaimAt`) and `commands/timeline_enrollment.go` (84, the audit
trail → intervals mapping) are new production files well inside the cap; the
change grew `internal/timeline/build.go` (229 → 237), `commands/timeline_sources.go`
(245 → 249), `internal/timeline/timeline.go` (212 → 220) and
`commands/timeline_render.go` (235 → 237, the footer now names the claim guard) —
all existing files, all still inside 250. `internal/timeline/enrollment_test.go`
(91) is a new test file in the range its neighbours occupy. Iteration 6: `commands/explain_delivery.go` (151, the two
sources and the coverage note) is a new production file inside the cap, and the
change it belongs to touched ~60 lines (additions and deletions) across the
existing `explain.go`, `explain_render.go` and `explain_render_detail.go`
(271 lines, the largest of them) rather than splitting them further; the new
`commands/explain_delivery_test.go` (323) sits in the range the package's other
test files already occupy. Iteration 5: `internal/chathistory/chathistory.go` (184, the
reader and the record shape) is a new package deliberately kept small enough to
read in one screen, because what it does NOT decode is as load-bearing as what
it does; the timeline's classifier grew to `internal/timeline/build.go` (229)
and its reader/notes to `commands/timeline_sources.go` (244) +
`commands/timeline_notes.go` (141), all still inside the cap. `parlay liveness`
is split to stay under it:
`internal/liveness/liveness.go` (238, the vocabulary and `Classify`) +
`classify.go` (156, the three pure steps); the verb is `commands/liveness.go`
(180, flags, entry and ranking) + `liveness_sources.go` (207, the reads) +
`liveness_render.go` (177, the table and notes) + `liveness_json.go` (102, the
envelope). Earlier iterations: `internal/timeline` is
`timeline.go` (212, types and vocabulary) + `build.go` (229, classification) +
`select.go` (158, narrowing and ordering); the verb is `timeline.go` (188,
flags) + `timeline_sources.go` (244, the reads) + `timeline_notes.go` (141, the
sentences) + `timeline_when.go` (45) + `timeline_render.go` (235); the relay
readers are `relayctl_trail.go` (181) + `relayctl_spool.go` (89). New **test**
files follow the package's own existing convention instead:
`internal/commands` test files run 216–2055 lines and
`internal/relayctl/relayctl_test.go` ends at 220, so
`commands/timeline_test.go` (489), `commands/timeline_history_test.go` (195),
`chathistory/chathistory_test.go` (154) and `timeline/history_test.go` (196)
are in line with their neighbours.

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
  socket's `GET /health|/agents|/delivery`), or is a file the fleet already
  writes (`{runtime}/delivery.log`, `{runtime}/audit.log`,
  `{STATE_HOME}/messages.jsonl`, `{STATE_HOME}/agents.json`), so
  `internal/guard.GuardedPaths` is untouched
  and no guard classification/test was needed. A `GET /api/chat/timeline` would
  have to re-implement enrollment, presence, delivery and command filtering
  server-side for no added truth — and, worse, could only answer while the
  server was up, which is the opposite of what a 2am read needs. The same
  argument is why the chat history is read off disk rather than through the
  existing `GET /api/chat/history?limit=N`, which is a route that cannot be
  asked of a dead server.
- **No read receipts, so still no `delivered` outcome.** Building one means the
  monitor acknowledging what it consumed — a new wire field, a new round trip,
  and a delivery path that now depends on an observability hop. That is the
  regression the objective forbids, so the vocabulary stops at `queued` and the
  server-side half stops at `recorded`/`unhanded`.
- **No new store for message bodies.** `messages.jsonl` is read as five
  identifiers; nothing is copied anywhere, and the reader has no field that
  could hold a body. Retention and privacy posture are exactly the server's.
- **No presence file, and therefore no on-disk heartbeat.** The one thing the
  roster fallback cannot answer — who is talking — is the one thing the server
  deliberately never persists (`PresenceTracker` is memory-only, because a
  connection count that survived a restart would be lying). Writing a heartbeat
  file to make `HEARTBEAT` answer while the server is dead would create a new
  durable record with a lifetime nobody has decided, and would be a different
  fact from the one the server reports. `HEARTBEAT unknown`, with the reason on
  the note, is the honest ceiling.
- **The final PR.** The run's orchestrator owns commits, so the branch is pushed
  and the PR opened from the commits already made —
  <https://github.com/trillium/parlay/pull/313> (head
  `gnhf/objective-make-parla-ea8605`, base `main`, **not merged**). Because each
  iteration's work is committed after that iteration, the branch has to be
  pushed again at the end of any later iteration for the PR head to include it;
  the PR body says which surfaces its current head contains. At the end of
  iteration 5 the head carried iterations 1–4 (the ledger, `explain`, `timeline`,
  `liveness`), with iteration 5's server-history work in the next commit on the
  branch.
- **No `--json` on `explain`.** The machine-readable halves already exist
  (`parlay commands --json`, the subscribers snapshot, the relay's `/delivery`);
  a third schema would be another thing to keep in sync for a surface an
  operator reads by eye at 2am.
- **No message bodies anywhere new.** The ledger stores identifiers, a role, a
  clock and a count — never text, a path, or an error string — `explain` prints
  what the ledger holds plus the agent's own status line (already durable state),
  and the history reader decodes five identifiers with no field for a body.
- **No dashboard/panel surface, no query language, no new store.** The
  timeline that answers the 2am question is text, and the durable records it
  reads are the ones the relay and the server already keep.
- **Not widened:** `JSON_EXEMPT_PATHS`, `GuardedPaths`, `internal/httpc`'s
  timeout-less client, the spool's `CHAT_MSG` line format, and any deployment
  script or public endpoint shape.
- **No new outcome for "the relay was not the delivery path".** A channel the
  relay never claimed is not a delivery outcome at all — it is the absence of
  one — and `recorded` already means the weak true statement ("the server
  persisted this message") with the guard sentence explaining what could not be
  concluded. Adding a twelfth vocabulary member would mean a new `--outcome`
  name, help text and `--json` value for a case the guard already names in
  prose.

## Left undone (with the reason)

- **The last commit's push.** The run's orchestrator owns commits, and a commit
  only reaches the PR once the branch is pushed again afterwards. Iteration 11
  pushed at its end, so the remote head is `4918031` (iterations 1–10: the
  ledger, `explain` with its disk and roster fallbacks, `timeline` with server
  history and the claim-trail guard, `liveness`, and the two live-fleet honesty
  fixes of iterations 9–10). Iteration 11's own change (the relay's `started`
  and `resumed` rows, the two timeline outcomes, and the liveness rule that a
  relay-process event is not agent activity) is uncommitted in this worktree and
  reaches <https://github.com/trillium/parlay/pull/313> on the orchestrator's
  next commit and push (head `gnhf/objective-make-parla-ea8605`, base `main`,
  **not merged**). A push is checked every iteration because the remote head was
  wrong once before: read it, never trust a note.
- **`explain` still does not read the chat server's own history** (the
  `recorded` / `unhanded` half iteration 5 added to `timeline`). It is
  deliberate: the per-agent screen already carries the relay's whole trail and
  the spool, and "did the server ever have a message the relay never took" is a
  windowed question with eight coverage guards — duplicating that verdict on a
  second surface is a second thing to keep in sync. `parlay timeline --agent
  <id> --outcome unhanded` is the query.
- **No `delivered` outcome, and therefore no read receipt.** Not built because it
  cannot be built honestly without a change to the delivery path itself: the
  monitor would have to acknowledge what it consumed (a new wire field, a new
  round trip, and a delivery path that now depends on an observability hop).
  That is exactly the regression the objective forbids, so the vocabulary stops
  at `queued` and says why in the footer of every run.
- **No supersession or staleness semantics beyond what the trails prove.**
  `superseded` here means "an earlier hand-over of a message id that was handed
  over again later"; `unhanded` means "the server persisted it and the relay's
  complete trail holds no hand-over". `internal/supersession` and
  `internal/staleness` are representation-plane (records, not chat) and are
  deliberately not entangled with either.
- **`parlay commands` does not read the relay.** It reports only the server's
  live-command registry, so a delivery that never reached an agent is invisible
  there. `explain` bridges that for one agent and `timeline` for a window;
  changing `commands`' own contract deserves its own decision.
- **No spool reconciliation past 64 agents in one pass** (named in the output,
  with `--agent` as the remedy) and **no `--json` on `explain`** (the
  machine-readable halves already exist; a third schema is another thing to
  keep in sync).
- **`unhanded` is a claim about the RELAY's path, not about delivery.** Even
  with a claim covering the message, the verdict says the relay never handed it
  over; it cannot say nothing else did. That is the ceiling of what any record
  here supports (no read receipt exists — see above), and the row now names the
  claim trail as the basis for the accusation rather than implying it covers
  every possible consumer.
- **The claim intervals come from `audit.log` alone; the live socket is not
  consulted for the guard.** A `GET /agents` answer says who is enrolled *now*,
  which cannot speak about a message from an hour ago, and `audit.log` is the
  durable record that survives the relay — the same reason the trails are read
  as files. If that file is gone (deleted, or a runtime dir that moved),
  enrollment is unknown and the verdict stays silent rather than guessing.
- **`register-denied` opens a claim, `unregister-denied` closes nothing.** A
  denied register proves the channel was held (by another caller), so it is
  evidence the relay was polling it; a denied unregister proves the caller held
  nothing, so it cannot end an interval. Both are pinned by tests, not by
  convention.

---

## Input-seam observability work (branch notes for PR #312 follow, unmodified)
The sections above are main's onboarding account (PR #314) and runtime-observability notes (PR #313), which landed while this branch was in flight.

# Input-seam observability — working notes

Status: both doors are instrumented end to end, the confidence hold is real,
all six named failure classes have a real producer, the live view can no longer
show a gap without naming it, and the eval door's outcomes have been audited
against the real compiled engine rather than a stub. This file records what the
tooling can and cannot tell the operator **right now**.

## What the operator can now tell apart

The question this exists to answer, from the panel: did the speech recogniser
mishear it, did the relay drop it, did the agent ignore it, or did the phone
never send it?

There is a durable **input-seam ledger** (`input.jsonl` beside
`messages.jsonl`): one record per **hop** an input made, keyed by the input's own
id, plus a live view (`parlay input`), a replay (`parlay input --input <id>`), a
live tail (`parlay input --watch`) and a JSON form for scripts. Four doors are
instrumented: the chat door (`POST /api/chat/send`, `/alert`), the dictation door
(`POST /api/chat/remote-input/submit`), the delivery hop at both poll points,
and the composer door (`POST /api/chat/eval`).

These states are now distinguishable where four failures previously looked
identical:

| State | What it means | Why it used to be invisible |
|---|---|---|
| `delivered` | A listener was handed a chat message; or the dictation reached its target. `LATENCY` is first hop → delivery. | A delivered and a queued message are byte-identical in `messages.jsonl`. |
| `queued` | Durably held, waiting for a listener. | Same bytes as delivered. |
| `queued (unpicked)` | Queued and nothing picked it up within the stale window. | Indistinguishable from an intentional queue. |
| `refused` | An intake declined it, the target refused delivery, or the interpreter never answered. `WHY` names the reason (`empty-input`, `missing-device`, `interpreter-unreachable`, …). | `/send` refusals never reached disk at all; dictation refusals reached nothing; an eval against a dead engine was a 502 and no record anywhere. |
| `recogniser error` | The dictation sent an empty transcript. Nothing was said, or nothing was transcribed. | A bare `400` the phone logged and dropped. |
| `no match` | The input parsed as a command but named no destination that matched: a dictation whose focus target did not become the active app/window, or a spoken channel/contact name in the composer's picker that matched none of the offered entries (`WHY` says `channel-not-matched` or `sender-not-matched`). | The submission settled `focus_failed` in an in-memory map nothing read; the picker's miss flashed as a hint for a second and left no durable record at all. |
| `command` | The engine read the buffer as that phrase command, and `WHY` names which (`submit`, `clear`, `switch-tab`, …). It was acted on rather than left as text. | A phrase that became a command and a phrase that matched nothing leave the same visible trace — the box keeps its text — so "did my input become a command?" had no answer anywhere. |
| `held` | A reported confidence fell below the configured threshold. `WHY` names the number. Nothing was typed; the text is preserved. | Did not exist: no surface reported a confidence at all. |
| `confidence_unknown` | No recognition confidence was reported for this input. | Did not exist, and would otherwise read as "confident". |
| `superseded` | A later input replaced this one before it was acted on — today, a composer snapshot the eval engine dropped because a newer snapshot of the same buffer had already been evaluated. | The engine fast-returned a noop and the relay forwarded it: no hop, no record, no trace anywhere. |
| `received`/`interpreted`/`routed` with no later hop | Where the input **stopped** (`WHY` says so, with the age). | Read as ordinary silence. |

Two rules are enforced rather than intended:

- **A non-`ok` hop must carry a `reason`.** The ledger refuses to store an
  unexplained failure and counts it (`stats.rejected`), so a gap in the view is
  never invisible: an input event that failed with no recorded reason is a
  defect in the tooling, not a quiet night.
- **`confidence_unknown` is its own state**, never folded into `ok`. `/send` and
  `/alert` have nothing to report, so that is what they honestly are.

## The hold

`PARLAY_INPUT_MIN_CONFIDENCE` (a number in [0,1]) is the threshold below
which a dictation is **held rather than routed**. `inputlog.Judge` owns the
decision, and the asymmetry is load-bearing:

| Reported confidence | Threshold | Result |
|---|---|---|
| below it | enabled | **held** — nothing typed or captured, text preserved, `low_confidence` + `held` hops, `status: "held"` on the wire and the SSE event |
| at or above it | enabled | routed normally |
| anything | disabled (unset) | routed normally |
| **not reported** | any | routed normally, recorded as `confidence_unknown` |

An absent confidence is **never** a hold: holding on an unreported value would
refuse every surface that cannot report one — today, all of them — a policy
invented on no evidence. A malformed threshold is a hard startup error, never a
silent fallback to disabled. `stats.minConfidence` carries it to the reader and
`parlay input` prints it above the table, so a hold always has its number beside
it.

## The sixth class: a superseding message

The composer posts every text change — typed or dictated — as a **versioned
buffer snapshot** to `POST /api/chat/eval`, and the compiled engine answers with
actions against it. When a newer snapshot for the same stream has already been
evaluated, the engine fast-returns a `noop` whose reason is
`stale-request-version`: its own name for "a later input replaced this one
before it was acted on" (`tools/cli/internal/evalengine/engine.go`).

That is the failure class the objective names and the product's own semantics
supply. It is now recorded as its own hop, with the stream and version it
belonged to and the engine token that produced the verdict:

```
superseded  superseded  source=eval  why=superseded-by-newer-version
            detail=stream=eval-phone-1-main v=3 engine=stale-request-version
```

Two deliberate choices:

- **The verdict is read, never recomputed.** The relay inspects the engine's own
  action batch; it does not compare versions a second time, because the engine
  owns last-write-wins and a second comparison could disagree with the decision
  that actually produced what the panel saw. The batch is walked as the generic
  JSON shape rather than re-typed into structs, because re-typing it would
  re-encode it and drop any field the relay did not know about.
- **Ordinary evals record nothing.** A row per keystroke would swamp the
  retained window and drown the seam it exists to make legible. Only the
  superseded verdict is recorded (`TestOrdinaryEvalRecordsNothing`).

This class needs no separate guard: the engine has already refused to act on the
snapshot. It is the one failure the product drops *before* the ledger existed;
what was missing was the operator being able to see that it happened.

## What the input became, and a destination that matched nothing

The engine's answer to every eval carries one more fact that is an input
outcome rather than a transport detail: `fired`, the id of the command it
decided the buffer was, or `""` when nothing matched. The relay reads it —
never recomputes it, the engine owns matching — and records one hop when it is
non-empty:

```
interpreted  ok  source=eval  detail=command=submit stream=eval-phone-1-cmd v=2
```

The view calls that state **`command`** and prints the command id in `WHY`. Two
deliberate choices: only a *fired* command is recorded (an eval that matched
nothing records nothing, or a row per keystroke would drown the seam), and the
derivation names this state **before** the generic "stopped after
interpreted" fallback, because an input that became a command stopped there by
design — the composer sends the text onward as a message under its own id — and
reading it as an input that went nowhere would be the same lie in the other
direction.

The same answer names the objective's "command that parsed but matched no
agent". `channel-select` and `sender-select` modes resolve a *spoken*
destination against the channels or contacts the panel offered
(`evalengine/commands.go`, rules 1–5); rule 5 is a miss and the engine answers
with `pickerHint` (channels) or `senderPickerHint` (contacts). That hint
flashes for a second in the panel and nothing durable recorded it, so from
every record the operator's spoken channel name was indistinguishable from an
input that was never sent:

```
routed  no_match  source=eval  why=channel-not-matched
        detail=stream=eval-phone-1-picker v=1 mode=channel-select candidates=3
routed  no_match  source=eval  why=sender-not-matched
        detail=stream=eval-phone-1-sender v=1 mode=sender-select
```

The two pickers get different reason tokens because they are different
failures, and `candidates=` is carried only when the request itself held the
list (the sender list is the engine's own, so a count for it would be invented
evidence). The hint's `args.text` is deliberately **not** read: it contains
what the operator said, and this ledger never stores that. One verdict per eval
is recorded, with the precedence written down: a superseded snapshot (the
engine never interpreted it) beats a fired command, which beats a picker miss,
which beats nothing at all.

### The eval door's own failures

Two points in the relay used to answer with a bare status and record nothing at
all, which is the one shape of gap this ledger calls a defect rather than a
quiet night:

| Failure | Recorded as | What it means |
|---|---|---|
| the body named no `device` | `interpreted` / `refused`, `missing-device` | the door declined the input before evaluating anything |
| the engine never answered (refused, timed out, non-200) | `interpreted` / `refused`, `interpreter-unreachable` | the input was never interpreted by anything — the "did the relay drop it" half of the original question, now named |
| the engine answered something unreadable | `interpreted` / `refused`, `interpreter-response-invalid` | a different failure from silence, so it gets its own token |

The wire shapes are untouched, including the door's pre-existing quirk that a
missing `device` answers **HTTP 200** with an `{"error":"device required"}` body
(`handlers.go`'s `writeAppError`). That quirk is a second reason the ledger has
to hold this: a client that reads only the status code cannot tell that refusal
from success, and before this iteration nothing else recorded it either.

## The live tail, and the four ways it names its own incompleteness

`parlay input --watch` reads **forward from a cursor** (`?afterSeq=<seq>`) rather
than re-reading a fixed newest-N window on every poll. That window form had a
hole of exactly the kind this seam exists to remove: a burst larger than the
window was printed minus whatever fell off its front, and nothing said so. A
cursor cannot be outrun, because when one is present `limit` means the **oldest**
N of the set it matches (the snapshot view, with no cursor, still gets the newest
N — unchanged).

The tail can still be incomplete in ways no cursor fixes, so all of them are
printed rather than inferred. A tail that shows a gap silently is the same defect
as an input event that failed with no recorded reason:

| Line | What it means | How it is known |
|---|---|---|
| `JOINED at the live edge (seq N) — M retained hop(s) before this tail are NOT shown` | A tail follows the live edge. The history before it is a deliberate starting point, not an omission; `parlay input` reads it. When the server was unreachable at startup the tail adopts the edge on the first page it does get, rather than dumping the retained window into a live view. | The join page's own `retained` and `newestSeq`. |
| `GAP — N hop(s) (seq X–Y) were evicted from the retained ledger before this tail read them` | The ring holds 5,000 events and dropped older ones between two polls. | The first `seq` a page returns, against the cursor. `seq` is dense, so `cursor+1 … first-1` is provably missing. |
| `OBSERVER LOSS — the ledger itself did not write N record(s) (… dropped on a full queue, … rejected as malformed)` | The ledger's own writer shed records. | `stats` travels with every page; only a change is announced. |
| `CURSOR AHEAD — this tail is at seq N but the ledger's newest is M (it restarted against a fresh ledger)` | The ledger is **behind** the cursor: seqs began again. Without this the tail prints nothing for ever and looks calm. It re-joins at the live edge. | `stats.newestSeq`, which is why that field exists. |

Adding `stats.newestSeq` also exposed a real defect: the ledger's `nextSeq` was
written by the writer goroutine without the mutex, so reading it from `Stats`
raced. It is now an atomic assigned *before* the append, which is also what lets
a reader learn where the ledger is without ever waiting on the disk
(`TestStatsDoesNotWaitOnAWedgedSink`).

## Demonstration (isolated server, `TALON_REPL_PATH` pinned away from the live machine)

Thirteen injections: the six named failure classes, what an input became, two
picker misses, the eval door's own two failures, and two healthy deliveries.
The eval injections run through a stub engine that answers the way the real
engine answers — a stale noop for a snapshot a newer one replaced, the engine's
own `pickerHint`/`senderPickerHint` for a picker that matched nothing, and
`fired` naming the command a phrase matched — and the last one is taken with
that stub deliberately stopped, so the interpreter really is unreachable. The
relay, the verdict reads and the ledger hops are all the real ones.

```
-- recogniser error: the dictation produced nothing --
-> POST /api/chat/remote-input/submit {"device":"phone-1","text":"","app":"Terminal"}
   HTTP 400  {"error":"text is required"}

-- low confidence below the 0.80 threshold: held, nothing typed --
-> POST /api/chat/remote-input/submit {"device":"phone-1","text":"ship it maybe","dryRun":true,"allowUnfocused":true,"confidence":0.25}
   HTTP 202  {"id":"ri-1","status":"held"}

-- named a target that does not match: no_match --
-> POST /api/chat/remote-input/submit {"device":"phone-1","text":"ship it","app":"NoSuchApp","dryRun":true}
   HTTP 202  {"id":"ri-2","status":"queued"}

-- intake refused it before storing anything (no device) --
-> POST /api/chat/remote-input/submit {"text":"ship it"}
   HTTP 400  {"error":"device is required"}

-- dictation that reached the target (dry run: nothing typed) --
-> POST /api/chat/remote-input/submit {"device":"phone-1","text":"ship it","dryRun":true,"allowUnfocused":true}
   HTTP 202  {"id":"ri-3","status":"queued"}

-- a chat message nobody polls: queued, then unpicked --
-> POST /api/chat/send {"text":"this one is never picked up","toAgent":"nobody"}
   HTTP 200  {"ok":true,"id":"m0"}

-- a chat message that IS delivered --
-> POST /api/chat/send {"text":"this one is delivered","toAgent":"c0"}
   HTTP 200  {"ok":true,"id":"m1"}

-> GET /api/chat/poll?channel=c0&after=nonexistent  (the delivery hop)
   HTTP 200
{"id":"m1","role":"user","text":"this one is delivered","cursorReset":true}

-- a composer snapshot a newer one already replaced: superseded --
-> POST /api/chat/eval {"device":"phone-1","streamId":"eval-phone-1-main","version":3,"text":"ship it"}
   HTTP 200  {"actions":[{"args":{"reason":"stale-request-version"},"verb":"noop"}],"baseVersion":1,"engineEvalNs":1,"ok":true,"seq":3,"sseClients":0,"streamId":"eval-phone-1-main","timing":{"engineEvalNs":1},"v":1}

-- a phrase the engine read as a command: what the input BECAME --
-> POST /api/chat/eval {"device":"phone-1","streamId":"eval-phone-1-cmd","version":2,"text":"submit","voiceEnabled":true}
   HTTP 200  {"actions":[{"args":{"delayMs":1000},"verb":"armSubmit"}],"baseVersion":1,"engineEvalNs":1,"ok":true,"seq":3,"sseClients":0,"streamId":"eval-phone-1-cmd","timing":{"engineEvalNs":1},"v":1}

-- a spoken channel name the engine matched against 3 channels and found nothing --
-> POST /api/chat/eval {"device":"phone-1","mode":"channel-select","streamId":"eval-phone-1-picker","version":1,"text":"nonexistent channel","tabs":[{"id":"a"},{"id":"b"},{"id":"c"}]}
   HTTP 200  {"actions":[{"args":{"text":"No channel matched — try again"},"verb":"pickerHint"}],"baseVersion":1,"engineEvalNs":1,"ok":true,"seq":3,"sseClients":0,"streamId":"eval-phone-1-picker","timing":{"engineEvalNs":1},"v":1}

-- a spoken contact name that matched nothing (the reply-to picker) --
-> POST /api/chat/eval {"device":"phone-1","mode":"sender-select","streamId":"eval-phone-1-sender","version":1,"text":"nobody i know"}
   HTTP 200  {"actions":[{"args":{"text":"No contact matched — try again"},"verb":"senderPickerHint"}],"baseVersion":1,"engineEvalNs":1,"ok":true,"seq":3,"sseClients":0,"streamId":"eval-phone-1-sender","timing":{"engineEvalNs":1},"v":1}

-- an eval the door declined before evaluating anything (no device) — HTTP 200 with an {error} body --
-> POST /api/chat/eval {"streamId":"eval-phone-1-main","version":1,"text":"ship it"}
   HTTP 200  {"error":"device required"}

-- the interpreter itself is down: the input was never interpreted at all --
-> POST /api/chat/eval {"device":"phone-1","streamId":"eval-phone-1-main","version":9,"text":"ship it"}
   HTTP 502  {"error":"engine unreachable: Post \"http://127.0.0.1:4658/eval\": dial tcp 127.0.0.1:4658: connect: connection refused"}
```

The view over that ledger:

```
==================== parlay input ====================
INPUT SEAM — 13 input(s) from the last 60 retained hop(s)
ledger: 18 retained, 18 written, 0 dropped, 0 rejected, 0 queued, newest seq 18
threshold: hold below confidence 0.80 (server PARLAY_INPUT_MIN_CONFIDENCE); a hold needs a reported confidence — unreported input is never held
STATE            INPUT                       SOURCE       CHANNEL    WHEN      LATENCY  WHY
refused          in-1791369066902203000-13   eval         -          2.6s      —        interpreter-unreachable
refused          in-1791369066575643000-12   eval         -          2.9s      —        missing-device
no match         in-1791369066569278000-11   eval         -          2.9s      —        sender-not-matched
no match         in-1791369066562633000-10   eval         -          2.9s      —        channel-not-matched
command          in-1791369066556274000-09   eval         -          2.9s      —        submit
superseded       in-1791369066549289000-08   eval         -          2.9s      —        superseded-by-newer-version
delivered        m1                          poll-backlog c0         3.0s      +6ms     —
queued (unpicked) m0                          send         nobody     3.0s      —        no listener picked it up in 3.0s
delivered        ri-3                        remote-input -          3.0s      +0ms     —
refused          in-1791369066515640000-04   remote-input -          3.0s      —        missing-device
no match         ri-2                        remote-input -          3.0s      —        target-not-matched
held             ri-1                        remote-input -          3.0s      —        confidence 0.25 below threshold 0.80
recogniser error in-1791369066485106000-01   remote-input -          3.0s      —        empty-transcript

Confidence: reported for 1 of 13 input(s) in this window.
Legend: delivered = handed a listener or typed at the target; queued = waiting;
refused = an intake or the target declined it; no match = it named a destination that
did not match; command = the engine read it as that phrase command (WHY names which),
so it was acted on rather than left as text; low confidence = measured below the
threshold; held = actually stopped by it; superseded = a later input replaced it before
it was acted on. WHY names the reason in every case. Every INPUT id is printed whole,
and pasting one into `parlay input --input <id>` replays its hops. See docs/input-seam.md.
```

Not one of the six failures reads as healthy, the two new eval failures are
named rather than silent, and `command` is distinguishable from both. Two things
worth reading twice in that paste:

- The device-less eval answers **HTTP 200** with an `{"error":"device required"}`
  body — the door's pre-existing wire shape. A status-code-only reader calls
  that success; the ledger is what makes the refusal visible.
- The unreachable interpreter answers 502 and the operator's input is named
  `refused / interpreter-unreachable`, which is the "did the relay drop it"
  half of the original question answered with evidence instead of a guess.

Replays, straight off the durable ledger (`parlay input --input <id>`), one per
class:

```
==================== parlay input --input in-1791369066485106000-01 ====================
REPLAY in-1791369066485106000-01 — 1 hop(s)

  #1 —       2026-10-07T03:31:06.485  interpreted recogniser_error   source=remote-input  channel=- why=empty-transcript

Outcome: RECOGNISER ERROR — empty-transcript

==================== parlay input --input ri-1 ====================
REPLAY ri-1 — 3 hop(s)

  #1 —       2026-10-07T03:31:06.497  received    ok                 source=remote-input  channel=- detail=mode=inject
  #2 +0ms    2026-10-07T03:31:06.497  interpreted low_confidence     source=remote-input  channel=- why=below-confidence-threshold
  #3 +0ms    2026-10-07T03:31:06.497  held        held               source=remote-input  channel=- why=below-confidence-threshold

Outcome: HELD in 0ms — confidence 0.25 below threshold 0.80

==================== parlay input --input ri-2 ====================
REPLAY ri-2 — 2 hop(s)

  #1 —       2026-10-07T03:31:06.507  received    ok                 source=remote-input  channel=- detail=mode=inject
  #2 +1ms    2026-10-07T03:31:06.508  routed      no_match           source=remote-input  channel=- why=target-not-matched detail=outcome=focus_failed

Outcome: NO MATCH in 1ms — target-not-matched

==================== parlay input --input in-1791369066515640000-04 ====================
REPLAY in-1791369066515640000-04 — 1 hop(s)

  #1 —       2026-10-07T03:31:06.515  interpreted refused            source=remote-input  channel=- why=missing-device

Outcome: REFUSED — missing-device

==================== parlay input --input ri-3 ====================
REPLAY ri-3 — 2 hop(s)

  #1 —       2026-10-07T03:31:06.522  received    ok                 source=remote-input  channel=- detail=mode=inject
  #2 +0ms    2026-10-07T03:31:06.522  delivered   ok                 source=remote-input  channel=- detail=outcome=dry_run_passed

Outcome: DELIVERED in 0ms

==================== parlay input --input m0 ====================
REPLAY m0 — 1 hop(s)

  #1 —       2026-10-07T03:31:06.529  queued      ok                 source=send          channel=nobody

Outcome: QUEUED (UNPICKED) — no listener picked it up in 6.1s

==================== parlay input --input m1 ====================
REPLAY m1 — 2 hop(s)

  #1 —       2026-10-07T03:31:06.535  queued      ok                 source=send          channel=c0
  #2 +6ms    2026-10-07T03:31:06.542  delivered   ok                 source=poll-backlog  channel=c0

Outcome: DELIVERED in 6ms

==================== parlay input --input in-1791369066549289000-08 ====================
REPLAY in-1791369066549289000-08 — 1 hop(s)

  #1 —       2026-10-07T03:31:06.549  superseded  superseded         source=eval          channel=- why=superseded-by-newer-version detail=stream=eval-phone-1-main v=3 engine=stale-request-version

Outcome: SUPERSEDED — superseded-by-newer-version

==================== parlay input --input in-1791369066556274000-09 ====================
REPLAY in-1791369066556274000-09 — 1 hop(s)

  #1 —       2026-10-07T03:31:06.556  interpreted ok                 source=eval          channel=- detail=command=submit stream=eval-phone-1-cmd v=2

Outcome: COMMAND — submit

==================== parlay input --input in-1791369066562633000-10 ====================
REPLAY in-1791369066562633000-10 — 1 hop(s)

  #1 —       2026-10-07T03:31:06.562  routed      no_match           source=eval          channel=- why=channel-not-matched detail=stream=eval-phone-1-picker v=1 mode=channel-select candidates=3

Outcome: NO MATCH — channel-not-matched

==================== parlay input --input in-1791369066569278000-11 ====================
REPLAY in-1791369066569278000-11 — 1 hop(s)

  #1 —       2026-10-07T03:31:06.569  routed      no_match           source=eval          channel=- why=sender-not-matched detail=stream=eval-phone-1-sender v=1 mode=sender-select

Outcome: NO MATCH — sender-not-matched

==================== parlay input --input in-1791369066575643000-12 ====================
REPLAY in-1791369066575643000-12 — 1 hop(s)

  #1 —       2026-10-07T03:31:06.575  interpreted refused            source=eval          channel=- why=missing-device

Outcome: REFUSED — missing-device

==================== parlay input --input in-1791369066902203000-13 ====================
REPLAY in-1791369066902203000-13 — 1 hop(s)

  #1 —       2026-10-07T03:31:06.902  interpreted refused            source=eval          channel=- why=interpreter-unreachable

Outcome: REFUSED — interpreter-unreachable
```

`m0` is the dropped one: queued, never polled, and the replay says so rather
than stopping silently. The `in-…` ids exist on no wire response, so the view is
their only copy — and five of the classes are keyed by one: refusals (from all
three producers), recogniser errors, fired commands, the eval door's picker
misses, and superseded snapshots.

## Demonstration: what the live tail does with a hole (freshly captured)

Everything below is a **real** isolated server and the **real** CLI. Two steps
replace the ledger *file* under a running observer — which is what restoring a
backup or swapping a state dir does — and are labelled FIXTURE.

```
############ A. the tail joins at the live edge (real) ############
WATCHING the input seam — polling every 1s (the ledger has no push stream yet)
JOINED at the live edge (seq 2) — 1 retained hop(s) before this tail are NOT shown; `parlay input` reads them.
TIME         INPUT                       STAGE       CLASS              SOURCE         LATENCY
03:12:23.577 m2                          queued      ok                 send           —

-- the snapshot view over the same ledger (note 'newest seq') --
INPUT SEAM — 3 input(s) from the last 20 retained hop(s)
ledger: 3 retained, 3 written, 0 dropped, 0 rejected, 0 queued, newest seq 3

############ B. the cursor pages forward, from the OLDEST (real) ############
-- ?afterSeq=0&limit=2 (a cursor keeps the OLDEST page) --
seqs: [1, 2] newestSeq: 3
-- ?limit=2 with no cursor (the snapshot keeps the NEWEST page) --
seqs: [2, 3] newestSeq: 3
-- ?afterSeq=<newest> (the live edge: an empty list, HTTP 200) --
   HTTP 200  events: []

############ C. a ledger that restarts under the tail (real) ############
-- the observer stops answering: the tail says so, and keeps watching --
  (server unreachable — still watching; this is not evidence that nothing came in)
WATCHING the input seam — polling every 1s (the ledger has no push stream yet)
JOINED at the live edge (seq 3) — 2 retained hop(s) before this tail are NOT shown; `parlay input` reads them.
TIME         INPUT                       STAGE       CLASS              SOURCE         LATENCY
  (server unreachable — still watching; this is not evidence that nothing came in)
CURSOR AHEAD — this tail is at seq 3 but the ledger's newest is 1 (it restarted against a fresh ledger). Re-joining at the live edge.

############ D. hops evicted before the tail read them (real tail, FIXTURE ledger) ############
WATCHING the input seam — polling every 1s (the ledger has no push stream yet)
JOINED at the live edge (seq 1) — 0 retained hop(s) before this tail are NOT shown; `parlay input` reads them.
TIME         INPUT                       STAGE       CLASS              SOURCE         LATENCY
GAP — 98 hop(s) (seq 2–99) were evicted from the retained ledger before this tail read them, so they are NOT shown; a shorter --interval narrows the gap.
03:12:34.407 m3                          queued      ok                 send           —
03:12:34.407 m4                          queued      ok                 send           —

== replay still works on a hop the tail showed (durable, not memory) ==
REPLAY m5 — 1 hop(s)

  #1 —       2026-10-07T03:12:34.407  queued      ok                 source=send          channel=c0

Outcome: QUEUED
```

`OBSERVER LOSS` is not in that paste and cannot honestly be: it fires when the
ledger's own writer drops records (a full 256-deep queue, or a malformed event),
which a healthy server does not do on demand. It is demonstrated at the unit
boundary instead, through the real renderer and the real `stats` shape
(`TestWatchTailReportsTheLedgersOwnLosses`), which is also where the *first*
observation reports the running totals so a tail that joined after the loss
still sees it.

The scripts that produced these pastes are `.pi/demo/input-seam-demo.sh` and
`.pi/demo/input-tail-demo.sh` — run-local scratch, deliberately **not** part of
the diff. They pin `TALON_REPL_PATH` away from the live REPL, pin
`PARLAY_SERVER`/`PARLAY_STATE_HOME` at the isolated instance, and use a fresh
temp state dir; nothing is typed or focused on this machine and the live fleet
state is never touched.

## Demonstration against the REAL engine (this iteration)

Sixteen injections against an isolated server whose eval door points at the
**real compiled engine** (`parlay eval serve`, `PARLAY_EVAL_ENGINE_URL`), no
stub anywhere: the recogniser error, the low-confidence hold, the dictation
no-match, two refusals, a delivered message, an unpicked one, two real fired
commands (`submit` from `send it`, `next-tab` from `next tab`), two picker
misses, a picker that resolved, a real `stale-request-version` supersession, the
door's own `missing-device` refusal, and the interpreter stopped dead. The
isolated server ran with `HOME`, `PARLAY_STATE_HOME`, `PARLAY_AGENT_HOME`,
`PAI_DIR` and `TALON_REPL_PATH` all redirected into a temp sandbox.

The view over that ledger (`parlay input --limit 60 --stale-after 4`):

```
INPUT SEAM — 15 input(s) from the last 60 retained hop(s)
ledger: 20 retained, 20 written, 0 dropped, 0 rejected, 0 queued, newest seq 20
threshold: hold below confidence 0.80 (server PARLAY_INPUT_MIN_CONFIDENCE); a hold needs a reported confidence — unreported input is never held
STATE            INPUT                       SOURCE       CHANNEL    WHEN      LATENCY  WHY
refused          in-1791369791104608000-15   eval         -          1.0s      —        interpreter-unreachable
refused          in-1791369790070075000-14   eval         -          2.1s      —        missing-device
superseded       in-1791369790063625000-13   eval         -          2.1s      —        superseded-by-newer-version
command          in-1791369790054371000-12   eval         -          2.1s      —        next-tab
no match         in-1791369790039934000-11   eval         -          2.1s      —        sender-not-matched
no match         in-1791369790032291000-10   eval         -          2.1s      —        channel-not-matched
command          in-1791369790024766000-09   eval         -          2.1s      —        next-tab
command          in-1791369790016774000-08   eval         -          2.1s      —        submit
delivered        m1                          poll-backlog c0         2.1s      +7ms     —
queued           m0                          send         nobody     2.2s      —        —
delivered        ri-3                        remote-input -          2.2s      +0ms     —
refused          in-1791369789980246000-04   remote-input -          2.2s      —        missing-device
no match         ri-2                        remote-input -          2.2s      —        target-not-matched
held             ri-1                        remote-input -          2.2s      —        confidence 0.25 below threshold 0.80
recogniser error in-1791369789958418000-01   remote-input -          2.2s      —        empty-transcript

Confidence: reported for 1 of 15 input(s) in this window.
```

(The legend and confidence note follow the table exactly as always; sixteen
injections produced fifteen inputs because the picker that resolved is
deliberately not a failure and records nothing.)

Both picker misses, replayed from the durable ledger — the two rows the real
engine's overloaded `fired` field had been hiding:

```
==================== parlay input --input in-1791369790032291000-10 ====================
REPLAY in-1791369790032291000-10 — 1 hop(s)

  #1 —       2026-10-07T03:43:10.032  routed      no_match           source=eval          channel=- why=channel-not-matched detail=stream=eval-phone-1-picker v=1 mode=channel-select candidates=3

Outcome: NO MATCH — channel-not-matched

==================== parlay input --input in-1791369790039934000-11 ====================
REPLAY in-1791369790039934000-11 — 1 hop(s)

  #1 —       2026-10-07T03:43:10.039  routed      no_match           source=eval          channel=- why=sender-not-matched detail=stream=eval-phone-1-sender v=1 mode=sender-select

Outcome: NO MATCH — sender-not-matched
```

And the message nobody picked up: in the view above it is still `queued` (it
was two seconds old, inside the stale window), and once it ages past that
window the same row states the failure and its age —

```
queued (unpicked) m0                          send         nobody     15.4s     —        no listener picked it up in 15.4s
```

— and the replay off the durable ledger answers "was it ever delivered?" with
one hop and no listener:

```
==================== parlay input --input m0 ====================
REPLAY m0 — 1 hop(s)

  #1 —       2026-10-07T03:43:09.995  queued      ok                 source=send          channel=nobody

Outcome: QUEUED (UNPICKED) — no listener picked it up in 20.3s
```

The live tail over the same ledger joined at the live edge and printed the next
hop as it arrived (`JOINED at the live edge (seq 20)`, then a `m2  queued  ok
send` row), and a replay of an id the ledger does not hold says so rather than
printing an empty success.

The stub-engine demonstration from the previous iteration is the section above;
it is kept because it is where the six-class paste lives.

## What running it caught

### The real-engine audit (this iteration)

Every previous demonstration drove the eval door through a **hand-written stub
engine**. This iteration ran the same injections against the **real compiled
engine** (`parlay eval serve`, the same binary the deployment runs), and the
stub's convenience turned out to be hiding a production bug:

- **A picker miss rendered as a healthy command.** The engine overloads `fired`
  in its two picker modes: a `channel-select` / `sender-select` request bypasses
  command matching and answers with `fired` set to the **mode name** — the
  resolution path that ran, not a command that matched (`evalengine/engine.go`).
  The relay's precedence read any non-empty `fired` as "what the input became",
  so **every spoken destination that matched nothing** was recorded as
  `interpreted` / `ok` / `command=channel-select`, and because that branch ran
  first the real `no_match` hop — with `channel-not-matched` /
  `sender-not-matched` and the candidate count — was **never written at all**.
  The view told the operator their input had come back as a command named after
  the picker: a healthy state for the one failure the picker exists to name, and
  a direct violation of "does not report a healthy state for any of them".
  Fixed by `evalFiredNamesACommand(mode, fired)`, which refuses to read `fired`
  as a command when it is the mode the request asked for; the picker's own
  verdict then decides the miss. A picker that DID resolve still records
  nothing, because the panel switches tab visibly.
- **Why the tests could not see it.** The stub that stood in for the engine
  answered picker misses with `fired` absent, because that is what a
  hand-written answer naturally looks like — and the one test that did put a
  `fired` value on a picker response asserted the *wrong* semantics
  (`"clear"` on a `channel-select` request, a combination the engine cannot
  produce). The regression is now pinned with the engine's real shape:
  `TestEvalOutcomePrecedence`, `TestResolvedPickerRecordsNothing`,
  `TestEvalFiredNamesACommand`, and the picker stubs in
  `TestPickerNoMatchIsRecordedForEachPicker` all carry `fired` = the mode name.
- **What the same audit confirmed good.** Against the real engine, all fifteen
  other injections render as distinct, honest states: `recogniser error`,
  `held` (with the threshold), `no match` (dictation target), `refused`
  (`missing-device` and `interpreter-unreachable`), `delivered` (with its
  measured hop latency), `queued (unpicked)` for the message nobody polled,
  `command` for two different real commands (`submit`, `next-tab`), `superseded`
  from a real `stale-request-version` verdict, and nothing at all for the
  picker that resolved.

### Earlier iterations

- **A truncated id made two classes unreplayable** (iteration 3). Refusals and
  recogniser errors are keyed by a server-minted `in-…` id that appears on no
  wire response, so the view is its only copy — and the INPUT column cut it at
  22 characters, leaving a string `--input` cannot resolve.
- **The live tail's header disagreed with its own rows** (iteration 3): a
  14-character INPUT column with 22-character ids in it, drifting 8 columns.
- **The minted id's width was not actually a bound** (iteration 4). The id
  was `in-<UnixNano>-<seq>` with an *unpadded* sequence, so it grew a digit at
  every power of ten and would have exceeded any fixed column after ten
  thousand minted ids in one process. The suffix is now two digits (`in-…-08`,
  25 bytes maximum) and `TestMintedIDsFitTheViewsColumn` pins both ends of the
  contract (producer ≤ 25, consumer column 27).
- **The newest-N tail could be silently outrun** (iteration 5). Reading the
  newest N every poll meant a burst larger than N was printed minus whatever
  fell off its front. The tail now reads forward from a cursor and names every
  way it can still be incomplete.
- **Where the ledger was, was a data race** (iteration 5). `Stats` needed the
  newest `seq` for the cursor contract, and reading `nextSeq` from it raced the
  writer goroutine — `-race` caught it immediately. It is an atomic now,
  assigned before the file append, so a reader learns where the ledger is
  without waiting on the disk.
- **A new state could have been rendered as a failure by the generic fallback**
  (this iteration, caught by reasoning about the derivation before writing it).
  A fired command is recorded at `interpreted`/`ok`, and the view derives
  "stopped after interpreted" for exactly that shape — so `command` had to be
  named before the fallback, with `TestDeriveInputRowNamesWhatAnInputBecame`
  pinning it and `TestDeriveInputRowOnAnOlderLedgerDoesNotInventACommand`
  pinning that an older ledger still reads as it did before.
- **A map literal with a duplicate key is a compile error, not a silent
  overwrite** (this iteration): two identical `"command="` cases in one test
  table stopped `tools/cli` from building, which is the good outcome — it is
  worth remembering that Go's compiler is the gate for that mistake, not a
  linter.
- **`gofmt -l` exits 0 while listing unformatted files** (this iteration): a
  script that prints `gofmt-exit=$?` after `gofmt -l .` reports success while
  the configured gate (`gofmt -l . | (! grep .)`) would fail. Only the pipeline
  form is a gate; the bare exit status is not.
- **The eval door answers a validation failure with HTTP 200** (this iteration,
  found while writing the test for its refusal record): `writeAppError` returns
  `{"error": ...}` with a default 200, so a client that reads only the status
  code sees success for `device required`. That is the door's frozen wire shape
  (the endpoint contract documents the `{error}` group, not a status), and it is
  a second reason the refusal belongs in the ledger — but it is worth knowing
  before anyone reads those 200s as proof an eval happened.
- **An outage produces one row per attempted input, deliberately** (this
  iteration): while the interpreter is unreachable the panel keeps posting on
  every change, so an outage is a burst of `interpreter-unreachable` rows rather
  than one. That is the truthful shape of an outage — each input really was
  dropped — and the tail's GAP/OBSERVER LOSS lines still name the case where the
  burst evicts retained history.

## Verification (per module)

This repository has four independent Go modules and no root `go.work`. The
repo-root form of the stop condition cannot exit zero here; this iteration's raw
output, exactly as the gate was run:

```
$ go build ./... && go vet ./... && gofmt -l . | (! grep .) && go test ./... && make test-bdd
pattern ./...: directory prefix . does not contain main module or its selected dependencies
```

Verified in iteration 5 that no workspace arrangement fixes it: with a root
`go.work` listing all four modules, `go build ./...` **still** fails —
`pattern ./...: directory prefix . does not contain modules listed in go.work or
their selected dependencies` — because `./...` is resolved relative to a package
tree, never across workspace members. Only a fifth root module could satisfy the
literal gate, and nested modules make that impossible. (The probe's `go.work`
was removed; nothing about it is in the diff.)

The equivalent per-module sweep, matching CI's `GO_MODULES`
(`tools/cli tools/relay packages/go-server packages/spawn-profiles`), run fresh
this iteration after every edit, is green:

```
########## 0. the literal stop condition, at the repo root ##########
pattern ./...: directory prefix . does not contain main module or its selected dependencies
go-build-exit=1     <- pre-existing and structural: no root go.mod, so the chain stops here

########## 1. gofmt -l . (the GATE form: `gofmt -l . | (! grep .)`) ##########
gofmt: clean (exit 0)

########## 2. per-module build / vet / test (CI's GO_MODULES) ##########
----- tools/cli -----
?   	github.com/trillium/parlay/tools/cli/internal/testsupport	[no test files]
ok  	github.com/trillium/parlay/tools/cli/internal/wire	(cached)
ok  	github.com/trillium/parlay/tools/cli/internal/worktreeliveness	(cached)
mod-exit=0
----- tools/relay -----
ok  	github.com/trillium/parlay/tools/relay	(cached)
mod-exit=0
----- packages/go-server -----
ok  	parlay/go-server/internal/sourcecontracts	(cached)
ok  	parlay/go-server/internal/static	(cached)
ok  	parlay/go-server/internal/store	1.198s
mod-exit=0
----- packages/spawn-profiles -----
ok  	parlay/spawn-profiles/cmd/validate	(cached)
mod-exit=0

########## 3. -race on the packages this change touches (go-server) ##########
ok  	parlay/go-server/internal/handlers	16.885s
ok  	parlay/go-server/internal/inputlog	4.374s

########## 4. make test-bdd ##########
21 steps (21 passed)
PASS
ok  	github.com/trillium/parlay/tools/cli/internal/spawn	(cached)
make-test-bdd-exit=0
```

There is no known-red BDD baseline on this box: `make test-bdd` is green at
baseline and remains green (7 scenarios / 21 steps).

`go test` needs `CGO_ENABLED=0` here, and that is a **baseline of this box, not
a regression**: with cgo on, `tools/cli` — including `internal/parlaybeads`, a
package this branch never touches — fails to build on
`go-icu-regex/internal/icu` (`unicode/regex.h` not found). CI installs
`libicu-dev`; this Mac has no ICU headers. `-race` requires cgo, so that leg is
go-server only.

## Hard constraints, and how each is met

- **Observability never sits in the delivery path.**
  `TestSupersededEvalStillDeliversWithAWedgedLedger` drives the eval relay
  through a ledger whose sink never returns: the relay still answers 200 with
  the engine's verdict, promptly, and the panel still receives its frame.
  **New this iteration** `TestCommandVerdictStillDeliversWithAWedgedLedger`
  pins the same for the fired-command and picker-miss producers, so the
  constraint is proved for each verdict the relay now reads rather than once
  for all of them.
  `TestFailingLedgerSinkDoesNotFailTheRelay` does the same with a sink that
  errors. `TestDeliveryIsNotSlowedOrFailedByAWedgedLedger` covers `/send` + poll,
  `TestRemoteInputDeliveryIsNotSlowedOrFailedByAWedgedLedger` the dictation
  door, `TestRecordNeverBlocksOnAWedgedSink` the ledger boundary (50,000
  records against a wedged sink), and `TestStatsDoesNotWaitOnAWedgedSink`
  the read side: the newest `seq` a cursor reader asks for is available while the
  writer is parked inside a disk write, so watching cannot stall on the incident
  it is watching.
- **No raw audio, no change to message-history retention or privacy.** The
  ledger stores ids, stages, classes, short reason tokens and numbers —
  **never message text**. Backend error strings are not copied in either: a
  Talon or bead error can echo the text being typed, and the eval rows carry a
  command id, a stream id and a version — never the buffer, and never the
  picker hint's own text (which contains what the operator said).
- **`internal/guard.GuardedPaths` untouched; no guard check reimplemented.**
  No route was added: `POST /api/chat/eval` keeps the guarded classification it
  already had, and the read surface `/api/chat/input-events` is unchanged and
  still deliberately unguarded for the reason written beside it.
- **No deployment scripts, endpoint shapes or downstream consumers touched.**
  No route was added and no wire field changed this iteration: the new records
  use the `reason`/`detail` tokens the ledger already carries, and the engine's
  action batch is still passed through byte-for-byte (the new verdicts are read
  out of it, never re-typed or re-encoded).
  `?afterSeq=` is a new *optional* parameter on this branch's own read route;
  `stats.newestSeq` is an additive field on the same route; the old shape is
  the absent-parameter path, and it is pinned byte-identical by
  `TestInputEventsCursorReadsForwardAndPagesFromTheOldest` (`limit` with no
  cursor still returns the newest N). The relay's action batch is passed through
  byte-for-byte: it is inspected, never re-typed and never re-encoded.
  `confidence` on the submit body is optional and additive, and `held` is a new
  terminal status downstream readers already handle (Parlay clears input state
  only on `injected` and preserves the text on anything else, which is what a
  hold wants).
- **No harness scratch committed.** `.pi/` is in `.gitignore`.

## Deliberately not built (and why)

- **A recorded `unpicked` producer.** "No listener picked it up in 60s" is a
  read-time judgement over a queued hop; nothing has to fire for it to become
  true, and a producer would have to invent a timeout owner. The view derives
  it from the queued hop's age and says the age.
- **`received`/`interpreted` hops for accepted `/send` and `/alert`.** The
  `queued` row already carries the id, stage and source; those doors can gain
  hops the day they have something extra to say.
- **Rows for evals that decided nothing.** The eval door records the three
  verdicts that ARE input outcomes — superseded, a fired command, a picker miss
  — and nothing else: a row per keystroke would swamp the window the seam needs
  to be legible in.
- **The positive half of the picker path.** A spoken channel name that DID
  resolve produces `switchTab`, not `fired`, so the eval door records nothing
  for it. The panel switches tabs visibly, so it is not a silent failure; the
  miss is the one that needed a durable name. Recording it would mean parsing
  another action verb for an event the operator can already see.
- **The phone side's confidence reporting.** The server accepts and enforces
  `confidence`; nothing in this repo sends it (`packages/client`'s input wrapper
  relays composer text and settings — `voiceEnabled`, `voiceSettleMs`,
  `localOnlyVoice` — but the recognition implementation itself is not here, and
  no in-repo surface reports a per-transcript confidence). Until something
  reports one, the honest state of every dictation is `confidence_unknown`, and
  the view says so rather than implying trust.
- **A provenance threshold.** No surface reports provenance strength, so a
  provenance hold would compare against a value nothing produces.
- **A push stream for the ledger.** `--watch` polls and its header states the
  cadence; an SSE event would imply a delivery latency the ledger does not have.
  The cursor makes the poll *lossless within the retained window*, not instant.
- **Per-input aggregation in the tail.** `--watch` prints hops as they arrive;
  the per-input state is the snapshot view's job, and a tail that collapsed hops
  would hide the ordering that makes a stopped input obvious.

## Left undone

- **The branch lags the working tree by one commit.** PR
  [#312](https://github.com/trillium/parlay/pull/312) is open against `main`.
  This iteration pushed `gnhf/objective-give-the-o-ad0a88` as far as iteration 6's
  head, so this iteration's changes (the picker-mode `fired` fix, its
  regression tests, the docs) reach the PR on **the next push of that branch**;
  nothing blocks that. The PR has still never had a real CodeRabbit review — the
  repository is under 10 stars, so the bot posts a "skip review" summary unless
  a comment asks it with `@coderabbitai review`, which is worth spending.
- **No upstream surface reports a recogniser confidence**, so the hold is
  enforced and visible but nothing in this repo can trigger it end to end except
  a test or a caller that sends `confidence`. The view says `not reported`
  rather than implying trust; that is as far as this repository reaches.
- `OBSERVER LOSS` has no end-to-end injection (see above); it is
  unit-demonstrated through the real renderer.
- The ledger's read route still has no cursor *backwards* (no `beforeSeq`), so
  paging older history out of a 5,000-event window is one `limit` at a time.
- The eval rows' stream and version appear in the replay's `detail`, not in the
  snapshot table's columns (CHANNEL is a destination, and a stream id is 20+
  characters). One command away, but worth saying.
- **A fired command and the message it becomes are still two records.** The
  composer sends the text onward as a separate `POST /api/chat/send` under its
  own message id, and no correlation id crosses that boundary, so the ledger
  cannot join "what I said" to "the message it became" without a heuristic on
  text and time — which would be a guess dressed as a record. Naming that gap is
  the honest thing to do with it.
- **The eval door's stubs still stand in for the engine in most tests.** This
  iteration's defect existed precisely because a hand-written engine answer is
  not the engine's answer; the new-format picker stubs carry the real `fired`
  value now, but any future producer read out of the engine's envelope is worth
  one injection against `parlay eval serve` before it is trusted.
- The stop condition literally configured for this run cannot exit zero here:
  there is no root `go.mod`, and (now verified) no workspace arrangement fixes
  that. The per-module equivalent above is green.

## File size

This repository enforces no per-file line budget (CI gates are conflict markers,
a 2 MiB tracked-blob ceiling, gofmt/vet/build/test, and docs-index completeness —
`.github/workflows/ci.yml`). Following the objective, every **new** file this
branch adds is under 250 lines — this iteration's
`eval_interpret_precedence_test.go` (126), and the previous ones
`eval_interpret.go` (157 → 188 with the picker-mode rule and its comment),
`eval_interpret_test.go` (219 → 184 after the precedence tests moved out),
`eval_interpret_unit_test.go` (56 → 85), `eval_door_failure_test.go` (76) and
`input_command_test.go` (121), plus earlier
`input_watch.go` (234), `read_after_test.go` (144), `input_watch_test.go` (135),
`input_events_cursor_test.go` (80), `eval_supersede.go` (82),
`eval_supersede_test.go` (222), `inputlog/vocabulary.go` (105) and
`input_threshold.go` (45) — and the ceiling was chosen by this branch, not by the
repository: 250 lines is small enough that a file has one subject and large
enough that a real subject fits. The largest file this iteration was split when
it crossed the line (`eval_interpret_test.go` reached 252 with the new
regression tests, so the precedence and resolved-picker tests moved into
`eval_interpret_precedence_test.go`), which is the ceiling doing its job.

The edited files above 250 lines are pre-existing ones this branch only adds to:
`handlers/eval.go` (380, the eval door itself) and `inputlog/log.go`
(**already 242**, 254 now, because it owns the `Stats` struct that gained
`newestSeq`); `tools/cli/internal/commands/input_model.go` is 235. Splitting any
of them for a handful of lines would hurt more than help.
