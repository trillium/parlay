# Deterministic-first routing with confidence and progressive hardening

Status: v1, implements issue #128 §34–§37, §81, §89–§92. Code:
`tools/cli/internal/routing/` (engine) + `parlay route` (CLI surface,
`tools/cli/internal/commands/route.go`).

This is parlay representation-plane work. The plane split (Q16,
`00_ARCHITECTURE-GRILL-META.md`, register entry Q16) assigns "routing +
confidence hardening" to parlay; Gas City's `AddressDirectory` *refuses* an
ambiguous address rather than picking a winner
(`~/code/gascity/internal/session/address_directory.go:73-89`) — that refusal
is the execution-plane backstop, while the policy about when parlay may act on
a route, when it must ask, and when it must refuse is this layer
(`docs/gascity-plane-boundary.md` §2.7).

## The model

Every routing decision answers: *which target should hear this input?* A
target is an agent id / channel name. The answer always carries three things:

- a **basis** — where the answer came from: `rule` (authored), `hardened`
  (learned), `inference` (recorded external inference), or `none`;
- a **confidence** in [0,1];
- an **outcome** — what the confidence policy says to do: `act` (route
  silently), `confirm` (propose; require confirmation before acting),
  `refuse` (do not route), or `needs-inference` (no rule decided; a workflow
  may run inference and report back).

### 1. Deterministic layer (evaluated first, in this order)

Where a lookup table can answer, no model is asked (#128 §36: "Inference
should be an escalation mechanism rather than the default router").

1. **Explicit address** — the input's lead token names a known target
   ("Dave, check the thing" → `dave`). #128 §34: "The system can interpret
   the first portion as a potential routing key and the remainder as
   content." Confidence 1.0 by construction. The known-target roster is
   supplied by the caller via `--targets` — the engine itself holds no
   roster, and the CLI deliberately does not ask the server for one:
   `route decide` stays deterministic and offline. Wiring the live agent
   list in is part of the live-path integration (gap-fill 5).
2. **Authored rules** — operator-written `key → target` entries
   (`parlay route rule add`). A rule matches when its normalized key is a
   word-boundary **prefix** of the normalized input — prefix-only on
   purpose: the routing-key position is the front of the message (#128
   §34), and contains-anywhere matching would false-positive on inputs
   that merely mention a project. Confidence 1.0 by construction: an
   authored rule *is* the lookup table. When several authored rules match,
   the longest key wins; ties break by rule id (lexicographic). The order
   is total and documented so evaluation is reproducible.
3. **Hardened rules** — learned `signal → target` entries whose confidence
   is computed from recorded captain feedback (see "Hardening"). A hardened
   entry decides deterministically only while its evidence keeps its
   confidence at or above the act threshold; below that it can still
   *propose* (outcome `confirm`).
4. **No match** → outcome `needs-inference`, basis `none`.

### 2. Inference path and confidence policy

Parlay itself has no inherent inference (#128 §81; #128 §2). The engine
therefore never calls a model. When the deterministic layer does not decide,
the decision is recorded as `needs-inference`, and whatever workflow owns the
input may run inference and report the result back
(`parlay route propose --decision <id> --target <t> --confidence <c>`). The
engine then applies the thresholds to the reported confidence and records
everything — exactly #128 §36's "insufficient confidence → invoke inference →
record the inference decision".

Thresholds (policy, persisted in `$PARLAY_STATE_HOME/routing/policy.json`,
defaults are a **proposed gap-fill**, flagged below):

| outcome | condition (confidence c) | default |
|---|---|---|
| act | c ≥ act threshold | 0.80 |
| confirm | refuse ≤ c < act | 0.50–0.80 |
| refuse | c < refuse threshold | 0.50 |

### 3. Hardening — confirmed inference becomes deterministic

Evidence is keyed by the **lead signal**: the normalized first
segment of the input (text before the first comma/colon, else the first
token). This is the deterministic generalization #128 §34 names — the
"first portion as a potential routing key". An input with no usable lead
signal (empty after normalization) accrues no evidence and stays on the
inference path every time; that is correct, not a gap — an unkeyed
ambiguous message *should* stay probabilistic.

- `parlay route confirm <decision-id>` records a confirmation of the
  decision's (signal → target).
- `parlay route correct <decision-id> --target <t>` records a correction
  against the decided target **and** a confirmation of the corrected target
  (a captain correction is the strongest possible evidence for where the
  input actually belonged, #128 §90).

Confidence of a learned entry is the Beta(1,1) posterior mean over captain
feedback:

    confidence = (confirms + 1) / (confirms + corrections + 2)

This is the mathematically grounded confidence #128 §36 asks for: a Bayesian
estimate of "how often has this signal actually meant this target" under a
uniform prior. It is deliberately not a separate "hardened" flag plus a
counter — **an entry is hardened exactly while its evidence-based confidence
sits at or above the act threshold.** With the default 0.80 threshold, three
clean captain confirmations harden a route ((3+1)/(3+0+2) = 0.8); the
progressive property of #128 §35 falls out of the arithmetic.

### 4. Un-hardening — a wrong hardened rule loses its authority

A system that can only harden calcifies its first mistake. Three mechanisms:

1. **Automatic demotion.** Corrections enter the same formula. One correction
   against three confirmations drops confidence to (3+1)/(3+1+2) = 0.667 —
   below act, so the entry stops deciding silently and every future match
   goes back through `confirm`. More corrections push it under the refuse
   threshold, where it stops proposing at all.
2. **Teaching the right answer.** The correction's `--target` immediately
   starts accruing evidence for the correct route, so the signal re-hardens
   toward the right target rather than merely un-hardening from the wrong one.
3. **Explicit retirement.** `parlay route rule retire <id>` tombstones an
   authored or learned entry. Retired entries never match again but are
   preserved with their provenance — history is never destroyed, superseded
   entries remain inspectable (#128 §79).

**Ambiguity refuses to auto-act.** If two learned entries for the same signal
are both at or above the act threshold, the engine does not pick a winner —
the outcome is `confirm` listing both candidates. This mirrors Gas City's
mailbox posture ("delivering a message to the wrong session is worse than not
delivering it", `~/code/gascity/internal/session/address_directory.go:22-25`).

### 5. The captain-authority boundary

`VISION.md:21`: "An agent may speak; only the captain decides what happens
next." Two invariants keep hardening from ever acquiring captain authority:

1. **Only captain feedback is evidence.** `confirm`/`correct` carry an
   `--authority` field (`captain` | `agent`, default `agent`). Agent-authority
   events are recorded in the ledger for observability but are **excluded
   from the confidence formula**. An agent can never harden a route by
   confirming its own routing guesses. (The CLI cannot *authenticate* the
   captain — the chat API has no auth — so this is honest-by-declaration,
   recorded per event and auditable; flagged for review below.)
2. **Hardening only ever decides delivery.** A hardened rule changes *which
   target hears an input* — never what happens next, never a side effect. The
   routed target still does whatever its own workflow and the captain allow.

### 6. Observability

Every decision is recorded append-only in
`$PARLAY_STATE_HOME/routing/decisions.jsonl` with its full evaluation trace:
input, extracted signal, every layer consulted, which entry matched, the
confidence, the thresholds in force, and the outcome.

- `parlay route why "<text>"` — dry-run: full trace, nothing recorded.
- `parlay route explain <decision-id>` — the recorded trace of a past
  decision: *why did this message route the way it did, and was it a rule or
  an inference?*
- `parlay route rules` — every authored, learned, and retired entry with
  evidence counts and provenance (the decision ids that built it).

The decision ledger stores free text **locally** under
`$PARLAY_STATE_HOME` (like identity/scratchpad). Nothing here widens the
server-side live-command registry; the registry's no-free-form-text rule is
untouched (command reporting only ever sees the verb and flag names).

## Storage

`$PARLAY_STATE_HOME/routing/`:

- `policy.json` — thresholds (atomic tmp+sync+rename writes, same discipline
  as `internal/config`).
- `rules.json` — authored rules + learned evidence + tombstones.
- `decisions.jsonl` — append-only decision ledger.

Files, not beads, deliberately: Q4 (the store substrate) is reopened and
unresolved (`00_ARCHITECTURE-GRILL-META.md` Q4 entry). The store sits behind
a small interface so a beads-backed implementation can supersede it without
touching the engine.

## Exit codes (`parlay route decide` / `propose`)

0 = act (routed) · 3 = confirm required · 4 = refused · 5 = needs-inference ·
1 = runtime error · 2 = usage. Semantic exits follow the merge-gate
precedent; callers branching on non-zero fail closed.

## Proposed gap-fills flagged for review

#128 defers these; v1 chooses, records here, and none are load-bearing to
change later:

1. **Thresholds 0.80/0.50 and the Beta(1,1) confidence estimator** — chosen
   so three clean captain confirmations harden a route. Tunable in
   `policy.json`.
2. **Lead-signal-only generalization.** "Similar input" (#128 §35) is scoped
   to *same lead signal* in v1. Anything fuzzier (token overlap, embeddings)
   is inference's job, not the deterministic layer's.
3. **Authority is declared, not authenticated.** The chat API has no auth;
   `--authority captain` is an honest declaration recorded per event. If the
   token layer (Q6a) lands, authority can be derived from it instead.
4. **Correction teaches.** A correction counts as captain confirmation of the
   corrected target. #128 §90 supports this reading but does not state it.
5. **Live-path integration is out of scope here.** This ships the engine +
   CLI surface; wiring `parlay route` into the chat server's message path is
   a separate ticket once the engine has soaked.
