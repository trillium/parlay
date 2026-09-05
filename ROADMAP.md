# Parlay Roadmap

This file is the living roadmap for the Parlay project.
Task IDs reference the federated task store (`task show <id>`).

---

## Now — P1

### Gas City adoption (task-4cfpv)
Move Parlay's execution plane onto Gas City.

- Move parlay spawn onto Gas City session runtime providers (task-4cfpv.9)
- Move liveness and worktree teardown onto Gas City health patrol (task-4cfpv.10)
- Move the event spool and cursors onto the Gas City event bus (task-4cfpv.11)
- Collapse parlay crew status onto the Gas City bead store (task-4cfpv.12)
- Resolve: does the chat relay spool move to the GC bus or stay in Parlay? (task-3xk9x) ⬅ decision needed

### Firstmate integration (task-n4l9, task-vgd7)
Parlay takes over firstmate's agent lifecycle management.

- Evaluate integration readiness → in-progress (task-n4l9)
- Implement once eval lands (task-vgd7)
- Build Slice 3 (parlay supervise primitive) + Slice 4 (firstmate thin shims) (task-tozf)
- Wire `parlay supervise <id>` for supervisor waking, replaces fm-watch poll loop (task-40oo)

### mini2 migration (task-hsmi)
Parlay + Pulse + Danny agents survive MacBook-off.

- Scout + plan in progress (task-hhrw)
- Decision pending: standalone Bun server vs Pulse in-process module (task-l33i) ⬅ decision needed
- Decision pending: who serves static HTML/JS once Pulse is out (task-spoo) ⬅ decision needed

---

## Next — P2

### Go-first / CLI consolidation
Port the bash layer into Go so the CLI is the single spawn path.

- Use `juggle use <account>` instead of direct keychain read (task-4e9sd)

### Parlay/firstmate fold
Full merge of the two supervision layers.

- Complete Parlay tooling to replace herdr launching via agents with firstmate (task-5w63)
- Decision: remote-bridge crewmate spawn — build in firstmate now or wait for parlay primitive? (task-ybur) ⬅ decision needed
- Adopt Parlay-central supervision architecture? (task-60qn) ⬅ decision needed

### Server and infrastructure
- Fold `com.parlay.bundle-rebuild` launchd template into install flow (task-zubx)

### Event fabric
- Generic on-status-change EMIT hook in beads/bd for the Parlay event fabric (task-n1ao)
- External events into agent context (event stream & webhook ingestion) (task-h5q)

### Docs and polish
- Blog post: what Parlay is, how to use it, how to configure it (task-v2yp)

---

## Open decisions

These are blocking work until the captain decides:

| Decision | Task |
|---|---|
| Parlay standalone Bun server vs Pulse in-process module | task-l33i |
| Who serves Parlay static HTML/JS once Pulse is gone | task-spoo |
| Does the chat relay spool move to the Gas City bus? | task-3xk9x |
| Remote-bridge spawn: build in firstmate now or wait for parlay primitive? | task-ybur |
| Adopt Parlay-central supervision architecture? | task-60qn |

---

## Parked / deferred

- Fleet space-map: asteroids-style visualization of agent activity (task-sfer, task-sqy8)
- Offer Parlay routing hardening back to Gas City upstream (task-4cfpv.19)
- Pulse+Parlay: extract shared pure-string render lib (task-92a)
- Parlay TTS: backend display-version vs spoken-version (task-xqc)

---

## Landed

### Go-first / CLI consolidation

- Port `bin/parlay-spawn` to Go subcommand — bash script now errors out, use `parlay spawn` (task-04g1) — PR #239, #249, #250, #270
- Add `spawn` to GO_ONLY_VERBS in tools/cli/parity/run.sh (task-4avd) — closed obsolete: parity/run.sh deleted in T-08 (871b3f8f)
- `tools/parlay-bin spawn` has the same silent-sonnet-fallback gap (task-21d36) — PR #238, #270
- Wire `--profile` into parlay spawn (resolve from spawn-profiles TOML catalog) (task-3ui8) — PR #248

### Agent lifecycle

- Fix unmanaged agent lifecycle (task-4dz9) — PR #236
- Graceful agent shutdown via parlay (task-35ww) — PR #234
- Relay watch-list polls retired agents every 2s (HTTP 410 spam) — prune dead agents (task-0n80i) — PR #225

### Server and infrastructure

- Graceful read-only poll: `/api/chat/poll` should be genuinely read-only (task-1t0m) — PR #226
- Make `PARLAY_ALLOWED_ORIGINS` reachable on installed path (plist + install.sh flag) (task-2gjz) — PR #224
- Config-driven localhost link rewriter for off-home reachability (task-ead) — PR #229

### Docs and polish

- README: document each part of the system with links to deep dives (task-wnxq) — PR #233
- Consolidate env-var docs: examples/env.example vs packages/server/README.md (task-wpbc) — PR #228

---

_Last synced from task store: 2026-09-05_
