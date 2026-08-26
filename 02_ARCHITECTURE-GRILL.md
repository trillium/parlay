# 02 — Architecture grill: Round 1 answers (captain → agent)

## Q1. Endgame for the Bun server (`packages/server`): replace / sidecar / peer

Replace with a go server, bun not needed

## Q2. Which of the unported routes are *product* vs Pulse/PAI residue

**Clarification (from code review):** Bun uniquely owns:
- `eval`/`eval-push` (voice processing — PRODUCT)
- `device-cmd`/`navigate`/`reload` (panel control — PRODUCT)
- `declare-channel`/`alert` (panel messaging — PRODUCT)
- `tts*` (speech synthesis with PAI cache — SIDECAR/RESIDUE)
- `debug-log` (product but trivial)
- `plugins`/`pages`/`parlay-ui` (Pulse integration — RESIDUE)

**Decision:** Port eval, device-cmd, navigate, reload, alert, debug-log to go-server (all small, all in api-contract.md). TTS stays optional/pluggable. Plugins/pages die with Pulse.

## Q3. Does `tools/relay` survive, or does its job fold into go-server + Go CLI

**Clarification:** The relay daemon multiplexes long-poll channels via Unix socket to `$TMPDIR/parlay/<agent>.chan` spool files consumed by `tail -F`. It's had the largest bug class in the repo (orphaned readers, registered-but-deaf agents, 104-byte socket-path cap, spool cursor semantics).

**Folding into go-server means:** per-agent durable cursors on server, `parlay listen` becomes direct long-poll/SSE client with resume-from-cursor, eliminating the spool/tail layer entirely.

**Decision:** Fold relay into go-server + CLI cursor resume (Option b).

## Q4. Storage for search/audit given pure-stdlib constraint

**Clarification:** Pure-stdlib means no `go.sum` (current policy), so no SQLite cgo. The proposal: split live ring buffer from archive (rotate at byte-cap, but append evicted lines to `archive/<yyyy-mm>.jsonl` instead of dropping), brute-scan both on `GET /search?q=` — milliseconds for years of history on a one-captain fleet. Upgrade to SQLite later if it measures slow.

**Decision:** JSONL archive + brute-scan (Option a).

## Q5. Beads-backed crew status: what does "backed" mean mechanically

It means that we use beads as the layer to tell the system of activity and health. All actions by agents are associated with beads:
- agent spawns - bead claimed
- agent struggles - robots bead created
- agent finished - bead closed

System can check all these states to get an idea of what is going on, and can also have various aliveness checks

## Q6. Auth: opt-in static bearer token vs strictly network-delegated

Tailscale is the security layer at the moment, whatever that means here

## Q7. Audit log fidelity vs redaction policy — which wins

Fidelity presently wins

## Q8. Webhook delivery contract: config, guarantees, event set

**Clarification:** Unbuilt; VISION promises "publishes full message bodies when messages arrive." Proposal: static config (URL + event filter in `settings.json`, not a new CRUD API), fire-and-forget with hub-ingress queue pattern (5s abort, bounded queue, stall-based shed), include status transitions (`done`/`failed`/`needs-decision`) alongside message bodies — those are the notifications you actually want on your phone.

**Decision:** Static config, fire-and-forget, include status transitions.

## Q9. Two front ends permanent; go-server as blessed panel host; deploy pipeline

**Clarification:** `packages/client` = chat/write surface (vanilla TS), `packages/webview` = read-only fleet dashboard (React/Vite). Go-server serves both at `/` and `/fleet/` and **has never been deployed with either** — the deploy gap is that nothing builds/copies their `dist/` into `go-server/internal/static` (`-assets-dir` mount exists; build pipeline doesn't). This belongs in `go-server/deploy/install.sh`.

**Decision:** Keep stacks separate, add build+copy to go-server deploy script.

## Q10. Public/private line in spawn layer (parlay-spawn, launchers, beads)

**Clarification:** `bin/parlay-spawn` today: treehouse pool leasing, beads-required mode, herdr/gascity launchers, mechanic-dispatch wiring, PII-aware model routing. Proposal: public `parlay spawn` (register → launch → enroll, minimal launcher interface only) vs private `bin/parlay-spawn` (full fleet power tool with treehouse/beads/mechanic coupling).

**Decision:** Split (Option c) — public API keeps README promise ("spawn + supervise") honest while scaling to what's reproducible off-box.
