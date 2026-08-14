---
"parlay-cli": patch
---

Fix the handoff create→submit / say-guard false-positive that mis-nagged every fresh or resumed agent (robots-4x9f; root-cause cluster robots-6wb/0sv/bu8/51s/vi7).

`resolveRow` (in both `packages/cli/src/resolve-handoff.ts` and its Go port `tools/cli/internal/resolvehandoff`) queried the agent-scoped `list --assignee <agent> --status open` first, but on an empty result **fell through** to the store-global newest-open handoff. A fresh/resumed agent has no open handoff of its own, so the fallback grabbed some *other* agent's newest open handoff (136 open store-wide) and pinned the create→submit / say-guard nag onto whoever was posting — the agent then narrated "stale/inherited handoff unrelated to my role — I'll dismiss it", every session.

The agent-scoped query is now **authoritative**: when the agent is known, its answer — including "nothing open for this agent" — is final and never falls through to the store-global handoff. Handoffs set `assignee=<agent-id>` (owner stays the principal), so the scoped list is reliable and its emptiness genuinely means "nothing unsubmitted for me". The store-global `list` / `show --current` fallbacks now run only when the agent is unknown (a bare CLI call with no `PARLAY_AGENT_ID`), where there is no one to misattribute to. Regression tests added on both sides via an assignee-aware store stub.
