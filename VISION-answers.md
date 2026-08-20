# Vision review answers — parlay, 2026-08-18

Keep this file next to VISION.md. It is the calibration record: every verdict maps to a principle in the vision, so future edits stay grounded in the same reasoning.

## Round 1 — 10 hypotheticals

**H-1 Broadcast to all agents: In vision**
Reasoning: in mission - constraints to only broadcast to LIVE agents, constraints to adhere to rate limiting practices.
Edit: relay product section now includes broadcast with rate-limit enforcement.

**H-2 Optional JWT auth: Conditional**
Reasoning: stretch goal, not a core tenant of parlay, security to be handled by the environment it runs in.
Edit: auth stays environment-delegated; scope says "authentication for external access is the operator's responsibility."

**H-3 History search across channels: In vision**
Reasoning: would love the ability to keep track of what agents do what.
Edit: "The relay maintains a queryable record of channel history; cross-channel search is part of the relay contract."

**H-4 Agent-to-agent direct messaging: In vision**
Reasoning: Agent-to-agent coms are important, need human to observe the chats if they wish.
Edit: authority section revised — agents may message each other; all channels remain observable by the captain.

**H-5 TUI crew dashboard: In vision**
Reasoning: TUI as well as chrome web app that provides same data.
Edit: fleet visibility section added; standalone web app lives in this repo.

**H-6 Mechanic auto-merge: Off mission**
Reasoning: this isn't directly relevant to Parlay as a tool.
Edit: not added.

**H-7 Relay-managed crew status: In vision**
Reasoning: yes — with integrations built for beads specifically.
Edit: "Agent status is a first-class relay concept... backed by parlay's own beads store."

**H-8 Persistent command audit log: In vision**
Edit: audit log added to relay product section, more complete than live-registry.

**H-9 Outbound webhook: In vision**
Edit: "The relay publishes outbound webhooks carrying full message bodies."

**H-10 Publishable server package: In vision**
Edit: scope section updated; PAI/TTS extracted before publishing.

## Round 1 — inline draft annotations

**Safety section (PAI_DIR reference):** "We need to work to strike PAI from the reference, has no relevance to parlay in the longer term."
Edit: removed PAI_DIR; safety section now host-agnostic.

**Truthfulness section:** "This is something that is a consequence of how the tool has been developed and not a core tenant of parlay."
Edit: dissolved the section; honest-reporting survives only as the origin-guard invariant in scope.

**Go section:** "go is the target language of parlay besides the front end stuff."
Edit: reframed from "the rewrite follows" to "Go is the target language."

**Scope / Pulse claim:** "Pulse IS open source, is part of PAI, but is NOT a part of parlay and parlay should exist outside Pulse and PAI."
Edit: corrected — "Pulse is open source and part of PAI, but not part of this repository."

## Round 2 — 6 hypotheticals

**H-A Beads-native vs. generic relay status: Conditional → resolved**
Reasoning: "a hard beads dependency does NOT require PAI at all, please discover why this is true."
Research finding: bd (beads) is a standalone binary; a .beads store lives at any BEADS_DIR path; PAI is a YAML federation of beads stores but beads itself requires nothing from PAI. Parlay's status store = $PARLAY_STATE_HOME/agents.beads, zero PAI dependency.
Edit: "backed by parlay's own beads store at a parlay-controlled path; no PAI federation is required."

**H-B Independent parlay web app vs. Pulse: In vision**
Reasoning: parlay is divorced from PAI, we need to have the sources of monitoring outside of PAI.
Edit: "These surfaces live in this repository and work without Pulse or PAI."

**H-C Webhook payload (full body vs. token): In vision**
Edit: webhooks carry full message body.

**H-D Publishable server dependency shedding: In vision**
Reasoning: goodbye PAI, not needed.
Edit: "PAI store layouts and TTS caches are extracted before publishing and are not part of its public contract."

**H-E Broadcast rate limiting (relay-enforced): In vision**
Reasoning: important because we may send a message to 50 agents at once, and those agents are not spun up in the external service, resulting in getting a rate hit.
Edit: "the relay enforces rate limits so downstream services are not overwhelmed."

**H-F Audit log content (more complete): In vision**
Edit: audit log "includes verb, agent, flag values, positionals, exit code, and timing."
