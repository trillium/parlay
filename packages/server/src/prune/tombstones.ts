// ── Tombstones: make a removal stick against the removed channel's own poller ─
//
// Removing a channel from the registry used to mean nothing, because a leaked
// listener re-created its own row on its next poll (handlePollRequest
// auto-registers whatever polls). A tombstone is the record that the removal was
// DELIBERATE, so the poll route can refuse the channel instead of resurrecting
// it — and answer 410 Gone, which is the signal the listener needs to stop.
//
// See ./index.ts for the full robots-ycfa design note.

/**
 * How long a removed channel stays refused. Long enough that a leaked listener
 * gives up for good (it is told 410 on its very next poll, seconds later), short
 * enough that the id is reusable the same day without operator surgery.
 */
export const TOMBSTONE_TTL_MS = 6 * 60 * 60 * 1000 // 6 hours

/** id → epoch ms the tombstone expires. In-memory, like lastPollByChannel. */
export const tombstones = new Map<string, number>()

/** Mark `id` as deliberately removed, so a poll cannot silently resurrect it. */
export function tombstone(id: string, nowMs: number = Date.now()): void {
  tombstones.set(id, nowMs + TOMBSTONE_TTL_MS)
}

/**
 * True while `id` is still refused. Expired entries are dropped as they are
 * observed, so the map cannot grow without bound in a long-lived process.
 */
export function isTombstoned(id: string, nowMs: number = Date.now()): boolean {
  const until = tombstones.get(id)
  if (until === undefined) return false
  if (until > nowMs) return true
  tombstones.delete(id)
  return false
}

/**
 * Lift the refusal for `id`. Called on explicit re-enrollment: a deliberate
 * register-agent is an operator/agent act and must work on the first try, even
 * if the id was pruned a minute ago.
 */
export function clearTombstone(id: string): void {
  tombstones.delete(id)
}
