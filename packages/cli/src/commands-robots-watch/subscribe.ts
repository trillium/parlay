// subscribe.ts — the SUBSCRIBE half of robots-3q7n: recording which agent to
// notify when a bead completes. Pure helpers, no I/O (trivially unit-testable).
//
// MODEL (decision-4zr): a bead's Parlay "subscription" IS a `notify:<channel>`
// LABEL on the bead (see ./detect.ts notifyChannels — "this label IS the
// lightweight SUBSCRIBE"). The ORIGINATING agent stamps that label at CREATE
// time (it is the only process that knows its own PARLAY_AGENT_ID); the watch
// daemon reads it on CLOSE and delivers `parlay send --<channel>` back. This
// module centralizes the create-side rules: WHO to record and WHICH beads are
// eligible (guard-store beads are excluded).

// Guard-store beads (id prefix `guard-`, store ~/data/guard/.beads) are internal
// enforcement records, never work the originating agent waits on — excluded from
// subscribe/publish per the ticket's SCOPE (OUT: guard beads).
export function isGuardBead(id: string): boolean {
  return /^guard-/.test(id.trim());
}

// The channel to route completion back to == the creating agent's Parlay id.
// Absent PARLAY_AGENT_ID there is no originator to notify → null (no subscribe).
export function originatingAgent(env: NodeJS.ProcessEnv = process.env): string | null {
  const a = env.PARLAY_AGENT_ID?.trim();
  return a ? a : null;
}

// The subscribe label a bead carries so its close routes back — the exact shape
// ./detect.ts notifyChannels parses (`notify:<channel>`).
export function subscribeLabel(channel: string): string {
  return `notify:${channel.trim()}`;
}

// A bead should be auto-subscribed on create iff it is NOT a guard bead AND an
// originating agent is known. Returns the label to stamp, or null to skip.
export function subscribeOnCreate(
  id: string,
  env: NodeJS.ProcessEnv = process.env,
): string | null {
  if (isGuardBead(id)) return null;
  const agent = originatingAgent(env);
  if (!agent) return null;
  return subscribeLabel(agent);
}
