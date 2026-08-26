# 00 — Architecture grill: meta / tracking

The grill is an iterative, file-based interview converging on consensus about
parlay's architecture. This file is the index and the single source of truth for
question status and settled decisions. It is updated by the agent after reading
each captain response; the numbered iteration files themselves are append-only
history and are never edited after the other side has replied to them.

## Protocol

1. Files are numbered `{NN}_ARCHITECTURE-GRILL.md`, strictly increasing.
   **Odd NN = agent (questions/follow-ups). Even NN = captain (answers).**
2. Question IDs (`Q1`, `Q2`, …) are global across all rounds — never renumbered,
   never reused. A follow-up to Q3 in a later round is `Q3a`, `Q3b`, ….
3. The captain answers by creating the next even-numbered file. Inline answers
   under quoted question headings, free-form prose, or bare `Q4: (b), because…`
   are all fine. `PUNT` defers a question; it stays OPEN and gets carried forward.
4. After each captain file, the agent updates this meta file (status table +
   consensus register) and writes the next odd-numbered file with follow-ups and
   newly unlocked questions.
5. Consensus = every question RESOLVED or explicitly PARKED. Terminal step: fold
   the consensus register into `VISION.md` / `VISION-answers.md` / AGENTS.md as
   appropriate, in one PR the captain reviews.

## File ledger

| NN | Author | Date | Contents |
|---|---|---|---|
| 00 | agent | 2026-08-26 | this file |
| 01 | agent | 2026-08-26 | Round 1 — Q1–Q10 (endgame, ports, relay, storage, beads, auth, audit, webhooks, front ends, spawn) |
| 02 | captain | 2026-08-26 | Round 1 answers: Q1/Q5/Q6/Q7 decided; Q2/Q3/Q4/Q8/Q9/Q10 marked for agent clarification |
| 03 | agent | 2026-08-26 | Round 2 — Q3a/Q4a/Q6a/Q9a follow-ups; Q11–Q14 held-back questions (TUI, TS-CLI EOL, identifier leak, live-commands docs) |
| 04 | captain | — | *(awaited — Round 2 answers)* |

## Question status

Status: **OPEN** (asked, unanswered) · **ANSWERED** (captain replied, agent may
follow up) · **RESOLVED** (verdict in consensus register) · **PARKED** (deliberately
deferred, with a named revisit condition).

| ID | Topic | Asked in | Status |
|---|---|---|---|
| Q1 | Endgame for the Bun server (`packages/server`): replace / sidecar / peer | 01 | RESOLVED |
| Q2 | Which unported routes are product vs Pulse/PAI residue (incl. eval relay, TTS) | 01 | RESOLVED |
| Q3 | Future of `tools/relay`: keep daemon vs fold into go-server + CLI cursor resume | 01 | RESOLVED |
| Q4 | Storage for search/audit: pure-stdlib constraint; JSONL scan vs SQLite vs beads | 01 | RESOLVED |
| Q5 | Mechanics of beads-backed crew status: shell-out vs mirror vs source-of-truth | 01 | RESOLVED |
| Q6 | Auth: opt-in static bearer token vs strictly network-delegated | 01 | RESOLVED |
| Q7 | Audit log locus: client-side full-fidelity vs server ingest vs hybrid | 01 | RESOLVED |
| Q8 | Webhook delivery contract: config surface, guarantees, event set | 01 | RESOLVED |
| Q9 | Two front ends permanent? go-server as blessed panel host; deploy pipeline gap | 01 | RESOLVED |
| Q10 | Public/private boundary of the spawn layer (parlay-spawn, launchers, beads-required) | 01 | RESOLVED |

Held back for later rounds (dependent on the above): Q3 cursor-ownership detail,
Q4 archive retention policy, TUI stack choice, TS-CLI (`packages/cli`) +
parity-harness EOL date, `docs/live-commands.md` public/internal classification,
SSE identifier-disclosure residue (`identifier-disclosure-remains-on-sse`) —
dies with the Bun server or gets fixed in place.

## Consensus register

Q1 — REPLACE — go-server is canonical, Bun deleted when port completes (02)
Q2 — PORT + OPTIONAL — eval/device-cmd/navigate/reload/alert/debug-log to go-server; TTS pluggable; plugins/pages die (02)
Q3 — FOLD — relay daemon ceases; per-agent cursors on server; parlay listen becomes direct long-poll client (02)
Q4 — JSONL ARCHIVE — split live ring from archive at byte-cap; brute-scan both on /search; upgrade to SQLite if slow (02)
Q5 — BEADS AS HEALTH LAYER — spawns→claimed, struggles→robots, finished→closed; system reads beads for state/aliveness (02)
Q6 — TAILSCALE + OPTIONAL TOKEN — network security via Tailscale; opt-in bearer token on `PARLAY_TOKEN` env (02)
Q7 — FIDELITY FIRST — client-side full audit log (02, audit.jsonl at $PARLAY_STATE_HOME); server redaction policy unchanged (02)
Q8 — STATIC CONFIG + FIRE-AND-FORGET — URL+filter in settings.json; hub-ingress queue pattern; include status transitions (02)
Q9 — TWO STACKS + DEPLOY SCRIPT — client (vanilla TS) + webview (React) stay separate; add build+copy to go-server/deploy (02)
Q10 — SPLIT PUBLIC/PRIVATE — minimal public `parlay spawn` (register→launch→enroll); `bin/parlay-spawn` stays private power tool (02)
