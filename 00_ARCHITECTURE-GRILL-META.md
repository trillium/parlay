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
| 02 | — | — | DELETED — the captain answered Round 1 inline inside `01` instead (under each "Your answer:" heading, 2026-08-26 ~10:11) |
| 03 | agent + captain | 2026-08-26 | Round 2, rewritten in full for self-contained context — Q3a/Q4a/Q5a/Q6a/Q7a/Q9a follow-ups; Q11–Q15. **Captain answered inline** (~10:22), superseding the delegated `04` answers |
| 04 | — | — | DELETED — its delegated answers are superseded wherever the captain's inline answers in `01`/`03` differ (Q2, Q3a, Q5/Q5a, Q6, Q7/Q7a, Q10, Q13) |
| 05 | agent | 2026-08-26 | Round 3 — implementation-ticket plan (T-01…T-14 in six phases per Q15 order). Plan now amended by the inline answers — see `07` for the deltas |
| 07 | agent | 2026-08-26 | Round 4 — synthesis of the inline answers, plan deltas, and follow-ups the captain invited (plugins, pages sidecar, beads hard-dep, tailscale layer, server audit, gascity leverage) |

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
| Q3a | Cursor ownership on reconnect: client-side vs server-side | 03 | RESOLVED |
| Q4a | Archive retention, Bun-history migration, search default scope | 03 | RESOLVED |
| Q5a | Beads mechanics: who writes/reads; public soft-dep vs private-only | 03 | RESOLVED (veto EXERCISED — hard dep) |
| Q6a | Token restated from zero: build it? shared vs per-caller | 03 | RESOLVED (veto window closed — confirmed) |
| Q7a | Audit locus confirmation: local per-machine full-fidelity | 03 | RESOLVED (overridden — server-side) |
| Q9a | Installer builds frontends vs copies pre-built dist | 03 | RESOLVED |
| Q11 | TUI: priority, dependency ruling (Bubble Tea), minimum scope | 03 | RESOLVED (veto window closed — confirmed) |
| Q12 | TS-CLI + parity harness EOL: gate, scope, timing | 03 | RESOLVED |
| Q13 | Bun-only SSE identifier leak: patch now vs die with Bun | 03 | RESOLVED |
| Q14 | `docs/live-commands.md` classification in docs/README | 03 | RESOLVED |
| Q15 | Sequencing of the full consensus backlog | 03 | RESOLVED |
| Q2b | Plugin system: what is a plugin concretely (surface, discovery, contract) | 07 | OPEN |
| Q2c | Pages as generated-on-install sidecar: generator, source, destination | 07 | OPEN |
| Q2d | Generic UI-command (command/response) protocol replacing bespoke panel-aiming routes | 07 | OPEN |
| Q5b | Beads hard-dep ergonomics: install prerequisite, absence behavior, version pinning | 07 | RESOLVED (self-answered, delegation rule) |
| Q6b | Tailscale connection-layer shape: tsnet embed vs `tailscale serve` deploy integration | 07 | OPEN |
| Q7b | Server-side audit vs wire-redaction rule: token-gated full-fidelity ingest | 07 | RESOLVED (self-answered, delegation rule) |
| Q16 | Gascity leverage map: which hard parts of parlay move onto gascity | 07 | OPEN |

All ⚑veto windows from `04` are closed: the captain's inline answers exercised
the veto on Q5a (soft-dep → hard dep) and confirmed Q6a/Q11.

## Consensus register

Entries marked (01-inline)/(03-inline) are the captain's own words and
supersede the deleted 02/04 files. Unchanged delegated verdicts keep (04).

Q1 — REPLACE, LEAVE PAI — "we do not want to participate in PAI any longer"; the concept becomes the Go command server + chat relay + event stack; PAI was too heavy (01-inline)
Q2 — PORT + PLUGINS/PAGES VITAL — eval/device-cmd/navigate/reload/alert/debug-log to go-server; TTS "yes, may drop" (pluggable); **plugins are vital** ("expand the capabilities of the commands that can be run"); **pages vital as sidecar** (generated on install/build, not in git); voice = string-eval capability only; panel-aiming vital but generalized to a command/response protocol driving any subscriber's UI; panel product vital (01-inline, OVERRIDES 02's "plugins/pages die")
Q3 — FOLD — relay daemon ceases; parlay listen becomes a direct long-poll client (01-inline: "B is a good choice")
Q3a — HYBRID CURSOR — client MAY pass its position (?after=); otherwise the server decides what the client receives; 50-line replay cap + loud skip kept (03-inline)
Q4 — LIGHT + CONFIGURABLE — sensible smart default (JSONL brute-scan), storage backend swappable later (01-inline)
Q4a — UNBOUNDED + DOCTOR — no auto-deletion; a doctor-style command tracks archive size/age/since-when so the user can dump at will; migrate rotated Bun history at cutover; search covers ring+archive (03-inline)
Q5 — BEADS REQUIRED — "bd is still the required backend of this thing, we must use beads, it is a superior tool" (01-inline, OVERRIDES 04's soft-dep)
Q5a — BEADS IS THE LAYER — beads is the necessary layer for spawn/status/aliveness and "should be depended upon to get that info" (03-inline, veto EXERCISED on 04's soft-dep)
Q6 — TOKEN + TAILSCALE FIRST-CLASS — opt-in bearer token yes; "I explicitly want an option to use tailscale as the connection layer to make this screamingly easy" (01-inline)
Q6a — BUILD TOKEN, SHARED SECRET — opt-in PARLAY_TOKEN in both guards, header + SSE query fallback, off by default; per-caller refused (04, confirmed 03-inline)
Q7 — SERVER-SIDE AUDIT INGEST — option B: "the client apps will not have an easy UI for this likely, and should not" (01-inline, OVERRIDES 04's client-side)
Q7a — FOLLOWS Q7 — server-side ingest confirmed by reference (03-inline defers to 01); token becomes a prerequisite of the audit route
Q8 — STATIC CONFIG + FIRE-AND-FORGET — URL+filter in settings.json; hub-ingress queue pattern; include status transitions (04, confirmed 01-inline)
Q9 — TWO STACKS + DEPLOY SCRIPT — client (vanilla TS) + webview (React) stay separate; add build+copy to go-server/deploy; revisit later (01-inline)
Q9a — COPY-ONLY INSTALLER + --build FLAG — "prod go only, dev accepts bun, tinkerers will bring in dev tooling" (03-inline)
Q10 — SPAWN IS CORE, GASCITY IS THE LAUNCHER — bash `parlay-spawn` is deprecated; `parlay spawn` is an integral core product verb; gascity is the entry point for agent launching (01-inline, OVERRIDES 02's public/private split)
Q11 — TUI AFTER PORT — Bubble Tea allowed in isolated tools/tui module only; scope = agent list + channel tail + send (04, confirmed 03-inline)
Q12 — PORT LAVISH-IMPORT THEN DELETE — packages/cli + parity harness removed in one PR; VISION parity sentence rewritten same PR (04, confirmed 03-inline)
Q13 — HORIZON COMMITTED — captain took the rec: Bun server off within ~two release cycles of the Q2 port list landing; SSE identifier leak ACCEPTED until then (03-inline "take rec", supersedes 04's "patch now")
Q14 — GENERALLY USEFUL — list live-commands.md in docs/README's public table; may revisit (03-inline)
Q15 — ORDER ADOPTED, LOW STAKES — take rec; "doesnt matter, is ai built, will all be built in a short window" (03-inline). Phase 1 unblocked.
Q5b — REFUSE LOUDLY — bd absent = named error with install pointer at the verbs that need it, never a silent degrade; bd documented as an install prerequisite (07, self-answered)
Q7b — TOKEN-GATED FULL FIDELITY — the audit ingest route requires the bearer token (or loopback when no token is set); the redacted live-commands registry stays as the unauthenticated surface (07, self-answered)

STANDING DIRECTIVE (01-inline, closing note) — actively find scenarios where
gascity can take over hard parts of parlay: "What does gascity do well, where
we have to maintain less while gascity trucks along." Tracked as Q16.
