# Opt-in surfaces soak — pass 1 (2026-08-31)

Evidence pass over the four opt-in Gas City surfaces (epic task-4cfpv, bead
task-s26i7, crewmate soak-1). Every exercise ran in a **hermetic sandbox** —
`mktemp` HOME, `PARLAY_DATA_DIR`, `PARLAY_STATE_HOME`, `PARLAY_AGENT_HOME`,
`PAI_DIR`, own gc city — never the live install (robots-lor). Binaries were
built from this repo at origin/main: `parlay` (`CGO_ENABLED=0`),
`parlay-go-server`, the pinned gc (`third_party/gascity/PIN`), upstream `bd`.

## Verdicts

| Surface | Verdict | Defects |
|---|---|---|
| 1. SPAWN (`PARLAY_SPAWN_LAUNCHER=gc`) | **PASS** (after 2 fixes) | fixed [#201](https://github.com/trillium/parlay/pull/201), [#202](https://github.com/trillium/parlay/pull/202); filed [#203](https://github.com/trillium/parlay/issues/203) |
| 2. HEALTH PATROL (liveness verdicts) | **PASS** | filed [#204](https://github.com/trillium/parlay/issues/204) |
| 3. EVENT BUS (`PARLAY_BUS_EMIT`/`PARLAY_BUS_CONSUME`) | **PASS** | none |
| 4. CREW STATUS (`PARLAY_CREW_STORE` + `PARLAY_CREW_READ_BEADS`) | **PASS** (after 1 fix) | fixed [#205](https://github.com/trillium/parlay/pull/205) |

Ledger: **3 defects fixed** (PRs #201, #202, #205), **2 filed** (issues #203, #204).

## Surface 1 — SPAWN

Exercised end-to-end: `parlay spawn-agent` with `PARLAY_SPAWN_LAUNCHER=gc` →
gc session creation → charter delivery → `gc-nudge` → `gc-liveness` verdicts →
`gc-resolve` (live, stamped, and closed sessions) → teardown. Agents were real
subprocesses (long-poll loop; poll-once-then-sleep).

**Defect 1 (fixed, PR #201).** `gcLivenessObserved` decoded
`/api/chat/subscribers` as a bare JSON array; both servers emit an object
(`{poll:{channels},presence:[…],registered:{…}}`). The decode always failed, so
the confirm path could **never** fire — every gc launch took the report/steer
path. A unit test asserting on the wrong shape hid it. Fixed to parse the real
shape; confirm observed live afterwards (agent's open poll waiter within the
35s listening window → confirmed).

**Defect 2 (fixed, PR #202).** `gc-resolve` rule 2 matched live sessions by
`session_name == "parlay.<id>"`, but at the pin `gc session new parlay.<id>`
records the argument as the row's **template** and generates session_name
`<id>-adhoc-<hash>` — so a gc-spawn-created session could never resolve by
name. Fixed to match template OR exact session_name. Verified live:
`gc-resolve soak-a` resolves via session-name leg; after `gc session close`,
`gc-resolve soak-c` still resolves via the stamped bead-id leg with
`closed:true` (the closed-inclusive `gc beads show` fetch — a list scan
intentionally drops closed history).

**Defect 3 (filed, #203).** No teardown seam: `gc session close` orphans the
subprocess agent (observed: agent pid re-parented to 1, still running), and the
inverse hole — a naturally-exited process leaves its session open/asleep.
Nothing in parlay reaps either side.

**Non-signal to remember:** with no controller running, `gc session list`
reports `asleep` for live AND dead processes alike. Session state is not a
liveness signal; only the registry ∩ process-table oracle is (robots-jkwc).

## Surface 2 — HEALTH PATROL

Verdicts against real sandboxed sessions: alive (long-poller), dead (killed
pid, status file left), wedged-ish (sleeping session past its listen window).

- **Frozen crew-state exit table live-confirmed** across 7 cells: 0 valid
  status (any enrollment), 3 no-status, 4 not-enrolled, 5 status-unreadable,
  6 relay-unreachable; source suffixes `status` / `status-unenrolled` /
  `status-degraded` / none; valid-status-always-wins.
- **No false 'dead' in any scenario.** The wedged sleeper first CONFIRMED —
  investigated and found correct (its enrollment poll sat inside the 35s
  `LISTEN_WINDOW_MS`); after expiry it reported unconfirmed with a **typed R7
  refusal** (subprocess provider → no steer capability), never a "dead" claim,
  with the process verifiably alive.
- Dead-no-status → unknown/HOLD; `parlay sweep --dry-run` never proposed
  closing a live agent (keep-list/self/terminal-state holds all fired where
  expected).

**Defect 4 (filed, #204).** Sweep split-root: `sweepCandidates` enumerates
`$HOME/.parlay/agents` (`guard.go` `parlayAgentsDir`) but classifies via
`PARLAY_AGENT_HOME` (`identity.AgentsRoot`). One command, two roots — worst
case classifies (and could tear down) the HOME-root agent using another
agent's status. Sandbox workaround during this pass: symlink the two roots.

## Surface 3 — EVENT BUS

go-server with `-bus-emit -bus-consume`, own state dir, `PARLAY_GC_CITY`
pointing at the sandbox city; gc supervisor brought up with a **short**
`GC_HOME` (unix socket paths cap at 104 bytes — the first supervisor start
under the long sandbox path failed `bind: invalid argument`) and the **real**
HOME (the supervisor refuses an overridden HOME by design); city started under
it with `gc --city <dir> start`.

- **Emit:** hub actions (send, register-agent) land in the city's
  `events.jsonl` with dense seqs, verbatim payloads, actor `parlay-server`.
- **Degrade-don't-die:** while the city API was down, the consumer logged
  `gc events --follow exited … — respawning` on backoff and the server kept
  serving; once the city came up, the next respawn connected with no restart.
- **SSE round-trip from an external actor:** `gc event emit parlay.tool_event
  --actor soak-external` → hub broadcast `event: tool_event` with the verbatim
  payload; consumer cursor (`<state-dir>/bus/consumer-cursor.json`) persisted
  after broadcast.
- **Restart no-gap + cursor resume:** killed the server, emitted 3 events,
  restarted → cursor advanced 26→29 (exactly the 3 downtime events). A second
  run with the SSE subscriber attached pre-restart **witnessed the catch-up
  broadcast** of a downtime event (cursor 29→30).
- **cursorReset loud-skip:** simulated log rotation (removed two
  server-downtime lines from `events.jsonl` so the next delivered seq jumped
  30→33). On restart the consumer logged exactly one loud line —
  `bus: cursorReset — after_seq 30 predates the retained floor (next delivered
  seq 33): 2 events skipped, not replayable` — then delivered the surviving
  event and advanced the cursor to 33. Loud, skip-forward, no crash, no
  silent gap.
- **Spool vs bus:** one message sent through the hub; the `messages.jsonl`
  row and the bus `parlay.message` payload are byte-identical (unicode
  intact).

## Surface 4 — CREW STATUS

Store: upstream `bd init --server` against a sandboxed `dolt sql-server` on a
private port (the `CGO_ENABLED=0` parlay build cannot open an embedded store —
its error message names the server-mode remedy, which worked as written).

- **Baseline (both gates off):** byte-identical legacy behavior — status file
  only, no event log, no store touched.
- **Q5b loud-failure contract, twice:** with the gate on and the store
  unusable, `parlay status` died `ExitRuntime` naming exactly what landed
  (file + event log) and what failed (bead write) — once for the no-CGO
  embedded refusal, once for defect 5 below. Nothing was swallowed.
- **Defect 5 (fixed, PR #205).** The crew bead is created with issue type
  `agent`, which beads validates against built-ins + the store's
  `types.custom` config — and nothing ever seeded that config (not
  `parlaybeads.Init`, not `bd init`). The first real dual-write on any fresh
  store died `invalid issue type: agent`. CI never caught it: the only
  real-store test is opt-in (`PARLAY_BEADS_INTEGRATION=1`) and creates a bead
  with no explicit type. Fixed: `Init` seeds `types.custom` append-only.
  Post-fix the same write lands and the store shows `types.custom = agent`.
- **Dual gate proven by divergence:** appended a line to the status FILE only,
  then read both ways — flipped reader (`PARLAY_CREW_READ_BEADS=1`) reported
  the bead's status, legacy reader reported the file's. Same projection
  format both ways (the byte-level projection pin is CI-enforced by
  `status_projection_test.go` + golden).
- **Contention:** 20 concurrent `parlay status` writers on one agent →
  `events.jsonl` gained exactly 20 events, seqs strictly dense 1..23, zero
  drops; all 20 lines present in the status file. The §7.1 blocking-flock
  append held.
- **status-migrate:** dry-run by default; `--apply` replayed a 2-line legacy
  file into log+store with a full backup (`status.pre-migrate.bak`), seeded
  `.supervise-seq` at head (2) so history does not re-fire; refusing the live
  agents root without `--live` fired with the exact robots-lor message; the
  migrated agent then read from its bead under the flipped gate.
- **Misconfig note:** `PARLAY_CREW_READ_BEADS=1` without `PARLAY_CREW_STORE`
  printed the documented stderr note and quietly read the file.
- **Supervise unit-5 cursor, full doctrine live:** routine events absorbed
  with NO cursor advance (safe direction); a terminal `blocked` event with the
  relay down → "supervisor NOT woken … marker not advanced, retry when the
  relay is back", and a second pass re-surfaced the same event
  (do-not-advance-on-relay-failure = retry-not-drop); with the relay up →
  "supervisor woken", `.supervise-seq` advanced to the event's seq (24), and
  the next pass answered nothing-new with no legacy re-scan.

## Observations (not defects, worth knowing)

- **`/api/chat/message` is Go-server-only, by documented design**
  (`hub-ingress.ts` — the Bun server 404s it). Supervise's relay wake
  therefore only works against a Go hub today. If crew-status rolls out
  against a Bun hub, the wake path needs that route or a different post.
- **Vendored beads touches a global path on every store op:** logs
  `[circuit-breaker] removed legacy closed breaker file:
  /tmp/beads-circuit/…-3307-lifespan.json` — a fully sandboxed parlay run
  reaching a path keyed to the LIVE lifespan store. Benign (removes a closed
  breaker marker) but a hermeticity leak in the upstream library.
- **`bd list --all` does not show custom-typed beads** — the crew bead exists
  and the label query parlay uses returns it, but upstream bd's list omits it.
  Don't use `bd list` as absence-evidence for crew beads.
- **gc supervisor constraints:** refuses an overridden HOME (use `GC_HOME`),
  and `GC_HOME` must be short enough that `supervisor.sock` fits the 104-byte
  unix socket cap. A failed socket bind leaves the API up but `--follow`
  clients unable to attach, which presents as "requires a running city API".
- A closed gc session bead can retain stale `state: "active"` metadata; and
  `/var/folders` vs `/private/var/folders` spellings both appear in gc output
  for the same city — compare paths canonicalized.

## What this pass does NOT prove

- **Scale and duration.** Single-host, minutes-long runs, ≤3 concurrent
  agents, ≤20 concurrent writers, tens of bus events. No multi-day soak, no
  hub under real fan-out load.
- **SPAWN:** charter delivery for the `subprocess` launcher remains the
  explicitly unverified assumption it was; teardown of gc-spawned agents has
  no seam at all (#203) — "spawn works" does not mean "lifecycle closes".
- **PATROL:** wedge detection beyond the sleeping-session approximation
  (supervise's full wedge detection is still the deferred TS TODO); sweep was
  dry-run only against gc-spawned agents.
- **BUS:** cursorReset was induced by hand-editing `events.jsonl`, not by a
  real gc retention/rotation mechanism (none was observed at the pin);
  at-least-once delivery under consumer crash mid-broadcast was not
  exercised; the panel-stream was exercised via raw SSE, not a real client.
- **CREW STATUS:** the beads read path ran against a server-mode store only
  (no CGO/embedded run); exit codes 5/6 were confirmed on the file path
  (surface 2), not re-induced on the bead path; supervise ran single-shot
  passes, not the long-lived loop; `claim`'s failure-recorder dual-write leg
  was not exercised.
- **Cross-surface:** all four surfaces were exercised against the same
  sandbox generation but not simultaneously under load; no chaos (kill -9 of
  the dolt server or supervisor mid-write).

## What remains before each retirement gate opens

- **SPAWN:** land a teardown seam (#203) and verify subprocess-launcher
  charter delivery; then a multi-agent pass where every spawned session is
  provably closed AND its process provably reaped.
- **PATROL:** a soak with real wedges (hung-but-listening agents) once wedge
  detection lands; sweep `--apply` runs against gc-spawned agents after #204
  (split-root) is fixed.
- **BUS:** a duration soak (hours) with rotation/retention exercised by gc
  itself, plus consumer-crash-mid-broadcast duplicate-delivery evidence
  (at-least-once means duplicates must be shown harmless downstream).
- **CREW STATUS:** an embedded-store (CGO) pass of the same evidence; the
  claim-path dual-write; a supervised long-run under concurrent writers with
  the relay flapping.
