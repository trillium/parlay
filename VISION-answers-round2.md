# Vision review answers — parlay, 2026-10-07 (delta round 2)

Keep this file next to VISION.md. It is the calibration record: every verdict maps to a
principle, so future edits stay grounded in the same reasoning.

Baseline: the approved VISION.md at commit 4686256 (2026-08-28, #131).
Mode: delta. The baseline text was NOT modified during this round.
Evidence window: ~60 merged PRs between 2026-09-03 and 2026-10-07.

## Round 2 — 10 hypotheticals, verdicts verbatim

**H-1 Phone injects text into any macOS app window: Off mission**
Reasoning: "out of scope at this stage"
Significance: remote-input (#296, #298, #299, #300) is real, merged, and deliberately OUT of
vision. It is a captain's local instrument, not a product surface. This is the single most
important verdict of the round: the vision is narrower than the code.

**H-2 A teammate gets a captain-scoped relay token: Off mission**
Significance: one principal is confirmed as load-bearing, not stylistic. #286's owner tokens
stay a possession mechanism, never a role mechanism.

**H-3 Ship a hosted relay with real accounts: Off mission**
Significance: no multi-tenancy. "The one person who owns the machine and the fleet" holds.

**H-4 Merge batch without review evidence, as a standing policy: In vision**
Significance: merge-and-disclose is an accepted mechanism, not a compromise. The guard
principle covers destructive ROUTES; review evidence is a separate axis with its own rule.

**H-5 Delete another safety net for speed: In vision**
Significance: mechanical CI gates may be removed when review covers them. Cheapest-first
refusal is NOT absolute — the refusals are about irreversible and fleet-visible actions,
never about being inconvenient.

**H-6 Promote the fleet store integration out of examples/: Off mission**
Reasoning: "How does Coder do this? recipes? That might be a better scope"
OPEN: the author raised a counter-question. Answered in round 3, not folded here.

**H-7 parlay self-deploys on merge to main: In vision**
Significance: the fleet-visible restart becomes an automated consequence of landing on main.
This reverses the assumption behind refusing unattended self-mutation, and it must be
stated as a scoped grant rather than a general licence.

**H-8 Auto-repair widens from diagnose to decide: In vision**
Significance: mechanical whitelist repairs may run without the --apply gate, reporting after.

**H-9 Publish the remaining packages under new names: In vision**
Significance: publishing is IN vision, and the flat parlay-<part> naming is the permanent
workaround for the taken bare name. This upgrades D-1 from a correction to a policy.

**H-10 One principal, many agents, one hard rule on trust: Conditional**
Reasoning: "explain this in a different way"
OPEN: the author wants this reframed rather than accepted or rejected. Round 3.

## What round 2 overturned about the draft

- D-3 (name the desktop-injection surface) is DEAD. H-1 refused it.
- The refusal criterion "adds a user role beyond the captain and crew" is CONFIRMED twice
  over (H-2, H-3) and may be stated more strongly.
- A new fault line opened: H-4, H-5, H-7, and H-8 are four "yes" answers that all relax a
  refusal. The draft must distinguish irreversible/fleet-visible action from review
  inconvenience, or the safety section will read as though it was ignored.
