# `parlay route` — hardening is arithmetic over captain feedback, never a flag

`parlay route` (tools/cli `internal/routing` + `internal/commands/route*.go`) implements
issue #128's deterministic-first routing with confidence and progressive hardening. The
full model — evaluation order, thresholds, the five recorded gap-fills where #128
deferred — is `docs/routing.md`. This note is the operating knowledge an agent touching
the code (or scripting against the verb) needs and would plausibly get wrong.

## There is no "harden" operation, and that is the design

A route's confidence is the Beta(1,1) posterior mean over **captain** feedback:
`(confirms+1) / (confirms+corrections+2)`. Three clean captain confirms = 0.80 = the
default act threshold — the route is "hardened" purely because the arithmetic crossed
the line. Consequences:

- **Un-hardening is just a correction.** One captain `route correct` against a
  3-confirm route drops it to 0.667 — back below act, back to asking. No flag to
  clear, no special demote verb, nothing to forget to do. A hardened rule loses
  authority exactly the way it gained it.
- **Searching the code for a `Hardened bool` will mislead you.** `BasisHardened` in a
  `Result` is a *report* that learned evidence cleared the act threshold at decision
  time, not stored state.

## The captain-authority boundary is enforced in exactly one place

`Ruleset.RecordConfirmation` / `RecordCorrection` (`internal/routing/feedback.go`) are
the only writers of `Confirms`/`Corrections`, and they only write for
`AuthorityCaptain`. Agent feedback lands in a separate `AgentEvents` counter that is
rendered ("N agent events, not counted") but never enters the confidence math — this
is VISION.md's captain-authority boundary: routing must never harden on an agent's
say-so. `--authority` **defaults to agent**, so unlabeled feedback fails toward
not-hardening. Authority is declared, not authenticated (gap-fill 3 in
`docs/routing.md`) — the CLI trusts the caller's label; do not present it as a
security control.

## Semantic exit codes: only 0 means "go ahead"

`route decide`/`propose` exit 0 act · 3 confirm · 4 refuse · 5 needs-inference ·
1 runtime · 2 usage (merge-gate precedent). A caller that branches on `== 0` fails
closed through every other outcome; never collapse 3/4/5 into "error handling" that
retries.

## Ledger and store rules

- `$PARLAY_STATE_HOME/routing/decisions.jsonl` is **append-only**; nothing in the
  package rewrites it (#128 §79 — history is never destroyed). Retiring a rule or
  learned entry (`route rule retire <id>`) tombstones it — it never matches again but
  stays listed by `route rules` with its provenance.
- `policy.json` / `rules.json` fail **loud** on corrupt content but treat a missing
  file as defaults/empty. The policy decode uses pointer fields deliberately: a `{}`
  policy would otherwise decode to act=0/refuse=0, under which *everything acts
  silently*. Keep that posture when extending the store.
- Observability is `route explain <ledger-id>` (recorded trace + every follow-up
  referencing it — answers "rule or inference?") and `route rules` (the whole table).
  Both read only the store; neither calls a model or the server.

## What this verb does NOT do (yet)

`route decide` is deterministic and offline: the roster comes only from `--targets`,
and no model is ever invoked — inference happens outside and reports back via
`route propose`. Wiring the live agent list / chat path in is explicitly deferred
(gap-fill 5 in `docs/routing.md`); do not "fix" the missing server call in passing.
