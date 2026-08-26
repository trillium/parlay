# 05 — Architecture grill: Round 3 — the implementation-ticket plan

**Provenance.** Derived mechanically from the consensus register in
`00_ARCHITECTURE-GRILL-META.md` (all 22 questions RESOLVED, three under a
standing veto window). Written proactively under the captain's delegation
rather than waiting for a go-signal — the plan itself is a document and fully
reversible. **What remains gated on the captain is execution:** the grilling
protocol's own rule is "do not enact the plan until shared understanding is
confirmed," so build work starts when this file survives captain review (an
`06_` reply, an edit, or an explicit go), and any ticket can be struck or
reordered there.

Order encodes **dependencies, not dates**. Phases are sequential; tickets
inside a phase are parallel-safe. Every ticket names its consensus source, so
a veto of Q5a/Q6a/Q11 strikes exactly T-11/T-12/T-13.

---

## Phase 1 — stop the bleeding (small, independent, no prerequisites)

### T-01 · Patch the Bun SSE identifier leak (Q13)
Drop the wildcard read grant on the two disclosing Bun routes, mirroring what
the Go server already does: stop reflecting/wildcarding CORS on
`GET /api/chat/events` (whose `tts_event` frames carry the `?device=` uuid to
every reader) and `GET /api/chat/agents`. Loopback/no-Origin callers —
CLI, hooks, same-origin panel — are unaffected; only foreign browser pages
lose read access. Update the `identifier-disclosure-remains-on-sse` tracking
note. **Accept:** a cross-origin `EventSource`/`fetch` can no longer read
device uuid or agent ids from the Bun server; existing guard integration
tests still green.

### T-02 · Frontend deploy pipeline in go-server/deploy (Q9, Q9a)
`install.sh` gains the missing build+copy seam: copy pre-built
`packages/client/dist` → assets root `/` and `packages/webview/dist` →
`/fleet/`, **copy-only by default** (deploy dependency surface stays "Go,
period"), `--build` flag opt-in to run the Bun/Vite builds first. Required
rider: `packages/client/build.ts` learns an env/flag opt-out for its
reload-ping to `:31337` so an installer-driven build cannot poke the live
server (improvement-log idea 9). **Accept:** fresh `install.sh` on a box with
prebuilt dists serves the panel at `/` and fleet at `/fleet/`; an install.sh
test asserts the `-assets-dir` layout (idea 10).

## Phase 2 — finish the port (go-server reaches parity on product routes)

### T-03 · Port the small product routes to go-server (Q2)
`eval`/`eval-push`, `device-cmd`, `navigate`, `reload`, `alert` (already
present — verify against contract), `declare-channel`, `debug-log`. Each lands
in `internal/handlers` with contract fidelity per `docs/api-contract.md`, is
added to `internal/guard.GuardedPaths` in the same commit (the route-set-rot
rule), and gets handler tests. **Accept:** every route the panel/CLI calls in
production answers identically from go-server; guard tests cover each new
mutating path.

### T-04 · TTS becomes pluggable, not ported (Q2)
Define the seam (config-pointed external handler or sidecar URL; go-server
proxies or 501s cleanly when unset). The PAI-coupled cache/report logic stays
out of go-server. **Accept:** panel TTS works when the sidecar is configured;
its absence degrades silently (no console spam, no 404 storm).

### T-05 · Switch production to go-server; Bun off (Q1)
Point the launchd job/port at go-server, watch a quiet window, then delete
`packages/server` and its CI job. One-time history migration hook from T-09
may land here if sequencing favors it (see T-09's migrate step). **Accept:**
production runs go-server only; `packages/server` gone from tree and CI;
AGENTS.md sections about the Bun server rewritten to past tense/removed.

## Phase 3 — kill the relay (its own quiet window; biggest bug-class payoff)

### T-06 · Server-side long-poll resume + client cursors (Q3, Q3a)
go-server's `/api/chat/poll` (or a sibling) accepts `?after=<id>`; each
`parlay listen` persists its own last-seen id under `~/.parlay/` (per-reader
bookmark — a server-side cursor is incoherent with two listeners per channel,
a failure mode this fleet has hit). Keep the 50-line replay cap and the loud
skip notice from robots-jkwc. **Accept:** kill a listener mid-stream, send N
lines, restart it — the gap replays exactly once, capped and loud.

### T-07 · `parlay listen` goes direct; relay deleted (Q3)
`internal/monitor` drops the parlay-monitor.sh/spool/tail chain for a direct
long-poll client with T-06 resume. Then `tools/relay`, its deploy scripts, and
the `com.parlay.relay` LaunchAgent are removed; the robots-buu8/mpr3/93xu
machinery (scoped runtime dirs, sun_path budget, ensure-up policy) retires
with it. Run in a quiet window with a documented rollback (previous binary +
relay reinstall). **Accept:** fleet enrolls/receives with no relay process on
the box; orphaned-reader class structurally impossible (no spool files exist).

## Phase 4 — retire the TS CLI (Q12)

### T-08 · Port `lavish-import`, then delete `packages/cli` + parity harness
Port `lavish-import.ts` to `tools/cli` (add to main.go dispatch; drop the
`bin/parlay` special-case). Same PR: delete `packages/cli`,
`tools/cli/parity/`, the CI bun step for cli, and rewrite VISION's "parity is
maintained by a diff harness" sentence to past tense. Git history is the
reference copy. Follow-ups unlocked (improvement-log ideas 3–4): consolidate
the four frontmatter parsers and the deliberate `~/.parlay` path-resolution
split — file as separate cleanup tickets once fidelity rationale dies.
**Accept:** `parlay lavish-import` works via the Go binary; repo has one CLI.

## Phase 5 — the promised product features

### T-09 · Message archive + search (Q4, Q4a)
Compaction appends evicted lines to `archive/<yyyy-mm>.jsonl` instead of
dropping them; `GET /api/chat/search?q=` brute-scans ring + archive by
default, result-capped. No auto-deletion ever; `parlay archive prune` is an
explicit verb routed through trash. One-time migration folds the rotated Bun
history files (`~/exchange/chat-history.2026-*.jsonl`) into the archive at
cutover. **Accept:** a message evicted from the ring is findable via search;
prune moves files to trash and refuses when trash is unavailable.

### T-10 · Webhooks (Q8)
Static config in `settings.json` (URL + event filter, no CRUD API);
fire-and-forget delivery reusing the hub-ingress queue pattern (5s abort,
bounded queue, stall-based shed, rate-limited failure logs); event set =
message bodies + status transitions (`done`/`failed`/`needs-decision` — the
notifications you actually want on a phone). **Accept:** a configured URL
receives message + status events; a dead URL degrades to one warn line per
30s and never blocks a send.

### T-11 · Beads health layer + public `parlay spawn` (Q5, Q5a ⚑veto, Q10)
Public minimal `parlay spawn` verb: register → launch (minimal launcher
interface) → enroll; `bin/parlay-spawn` stays the private power tool
(treehouse/mechanic/PII routing never ship). Soft-dep beads store at
`~/.parlay/agents.beads`: spawn=claim, struggle=robots-note, finish=close,
written only when `bd` is on PATH; every read path works without it (the
server never requires a bead to answer). **Accept:** on a bd-less box, spawn/
status/teardown work unchanged; with bd, `bd list` against the store shows
the lifecycle truthfully.

### T-12 · Opt-in token + local audit log (Q6a ⚑veto, Q7a)
`PARLAY_TOKEN` checked in both guards when set (header, plus query-param
fallback for `EventSource`), off by default — zero ergonomic change unless
exported; per-caller tokens refused (user-role infra VISION excludes). Audit:
one append per verb to `~/.parlay/audit.jsonl` from the command-report
wrapper — full argv, exit, duration, agent id; nothing new crosses the wire;
server redaction untouched. **Accept:** with the env set, an unauthenticated
cross-machine request 403s while tailnet CLI calls with the token work; every
Go-CLI invocation lands one audit line locally.

## Phase 6 — the TUI (Q11 ⚑veto)

### T-13 · `tools/tui`, Bubble Tea, isolated module
First third-party Go dep, leaf-isolated in its own module (own go.mod/go.sum;
server/CLI modules stay pure-stdlib — that law is unchanged). Scope: agent
list + live status, selected-channel tail, send box; same guard/token rules
as any writer. **Accept:** `parlay tui` (or `parlay-tui`) runs against a live
server read+send; no other module gains a dependency.

## Terminal step (from the meta protocol)

### T-14 · Fold the consensus into the permanent docs
One PR the captain reviews: consensus register → `VISION-answers.md` (or a
`docs/decisions/` home — improvement-log idea 8 suggests relocating the grill
files there too), AGENTS.md entries updated as each phase's reality changes,
`docs/live-commands.md` listed under "Generally useful" in `docs/README.md`
(Q14 — one line, can ride any earlier PR). **Accept:** a new agent reading
only the permanent docs learns every settled verdict without opening the
grill files.

---

*Objections, strikes, and reorders go in `06_` (or edits/comments anywhere —
I'll reconcile). Absent objection, this is the shared understanding, and
execution starts at Phase 1.*
