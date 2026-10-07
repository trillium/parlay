# Vision review answers — parlay, 2026-10-07 (delta round 3)

Companion to `VISION-answers-round2.md`. Verdict text is verbatim from the author.

## Round 3 — 6 hypotheticals

**R3-1 Recipes instead of core, the way Coder does it: Conditional**
Reasoning: "punting this for now, keeping current method"
Interpretation: the current method (fleet glue lives in `examples/fleet/`, out of core) stays.
NOT a rejection of recipes as a concept - a decision not to act now. No delta folded.

**R3-2 Where the trust discipline is written down: Conditional**
Reasoning: "I dont understand this - ened better explainer page"  [sic - "need"]
**This is a failure of the card, not a decision.** The card was three abstract placements
(a/b/c) with no example of what the reader would actually see. Re-asked as R4-2 in
concrete form: the actual text, the actual file, the actual reader.

**R3-3 What happens to code the vision does not cover: Conditional**
Reasoning: "I dont understand this either, need better explainer"
Same failure. The card asked about directory placement without saying which files move or
what the difference is to a person opening the repo. Re-asked concretely as R4-3.

**R3-4 The self-deploy grant, when the deploy itself is broken: In vision**
Reasoning: "redeploy should restore old if deploy fails"
Folded as D-7: the self-deploy grant (H-7) carries a mandatory rollback, not a health log.

**R3-5 Publishing freezes a format this repo deletes: In vision**
Reasoning: "publish, can change the contract later if we need to"
Folded as D-8: publishing is not a promise of stability. This RESOLVES the standing tension
between H-9 (publish) and the baseline's "parity is a historical fact" line. The versioned
`parlay-<part>` naming plus an explicit stability disclaimer is the mechanism.

**R3-6 Which CI gates are mechanical, and which are load-bearing: Conditional**
Reasoning: "we dont want to run bad tests, slow tests, or tests that are not useful"
This is the most informative refusal of the round, and it is NOT the question I asked.
I asked which gates are redundant. The answer reframed the principle entirely: the test
standard is not "cheap" but "worth the wall clock". A slow test earns its place only if it
catches something. That principle is stated by the author and is eligible to be folded -
but the specific gate-by-gate question is still unanswered, so R3-6 is re-asked as R4-6
with the reframed standard.

## Why round 3 produced three "I don't understand"

R3-2, R3-3, and R3-6 were written as abstract placements and category questions.
A hypothetical the author cannot answer is a badly built hypothetical, not a hard question.
Round 4 rewrites all three to name files, show the text, and state what changes for a reader.
