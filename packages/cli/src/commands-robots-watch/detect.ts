// Pure event model + diff core for the robots-watch poll-daemon. No I/O here, so
// it is trivially unit-testable (see ../commands-robots-watch.test.ts).

export type BeadStatus = string
export type StoreState = Record<string, BeadStatus> // bead id → status
export type EventKind = "created" | "closed"
export interface RouteEvent { store: string; kind: EventKind; id: string; status: BeadStatus }

// One bead as the store's `list --json` returns it (only the fields we read).
export interface Bead { id: string; status?: string; title?: string; labels?: string[] }

// bd's terminal status. Everything else (open/in_progress/blocked) is "live".
export function isClosed(status: BeadStatus | undefined): boolean {
  return status === "closed"
}

// Given the PREVIOUS status map for a store (undefined = never seen → SEED) and
// the CURRENT one, return the events to fire for the requested kinds.
//   - SEED (prev undefined): fire nothing; caller adopts curr. No history replay.
//   - created: a bead present now, absent before, and NOT already closed.
//   - closed: a bead we previously saw LIVE that is now closed (open→closed).
// A bead that first appears already-closed fires neither (history, not a
// transition we witnessed).
export function detectEvents(
  prev: StoreState | undefined,
  curr: StoreState,
  store: string,
  kinds: EventKind[],
): { events: RouteEvent[]; seeded: boolean } {
  if (prev === undefined) return { events: [], seeded: true }
  const events: RouteEvent[] = []
  const want = new Set(kinds)
  for (const [id, status] of Object.entries(curr)) {
    const before = prev[id]
    if (want.has("created") && before === undefined && !isClosed(status)) {
      events.push({ store, kind: "created", id, status })
    }
    if (want.has("closed") && before !== undefined && !isClosed(before) && isClosed(status)) {
      events.push({ store, kind: "closed", id, status })
    }
  }
  return { events, seeded: false }
}

// Parse the requester channel(s) a bead subscribes for close-notification: a
// `notify:<channel>` label. This label IS the lightweight SUBSCRIBE of
// decision-4zr — the bead names who to wake; agent/channel knowledge stays in
// parlay. A bead with no notify: label has no subscriber and is skipped.
export function notifyChannels(labels: string[] | undefined): string[] {
  return (labels ?? [])
    .map(l => /^notify:(.+)$/.exec(l.trim())?.[1]?.trim())
    .filter((c): c is string => !!c)
}
