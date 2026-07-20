// Ephemeral hash identity helpers — pure, deterministic, testable.
//
// An ephemeral agent has no human-chosen slug: it gets a random hash id
// ("eph-" + 8 hex chars), a derived display name, and a deterministic color
// computed from the id so the same id always paints the same tab accent.
//
// These functions are intentionally free of filesystem/network side effects so
// they can be unit-tested. The one collision-aware generator takes an `exists`
// predicate the caller supplies (backed by the ~/.parlay/agents/ dir listing),
// so it stays pure while still avoiding id reuse.

// 4 random bytes → 8 lowercase hex chars, prefixed "eph-". Uses the crypto RNG.
export function ephemeralHash(): string {
  const bytes = new Uint8Array(4)
  crypto.getRandomValues(bytes)
  let hex = ""
  for (const b of bytes) hex += b.toString(16).padStart(2, "0")
  return `eph-${hex}`
}

// Generate an ephemeral id that does not collide with an existing one. Retries
// once on collision (spec: "retry once if collision"); if the retry also
// collides — astronomically unlikely across a 4-byte space — the caller gets
// the second candidate and the on-disk write guard is the final backstop.
export function generateEphemeralId(exists: (id: string) => boolean): string {
  const first = ephemeralHash()
  if (!exists(first)) return first
  return ephemeralHash()
}

// Display name from an ephemeral id: "Agent " + the 8 hex chars uppercased.
// id is "eph-a3f21b4c" → "Agent A3F21B4C".
export function ephemeralName(id: string): string {
  return `Agent ${id.slice(4).toUpperCase()}`
}

// Deterministic, readable hex color from an id string.
//
// A tiny FNV-1a hash over the id produces a 32-bit value; we take three bytes
// and squeeze each into the 40–220 range so no channel is too dark (unreadable
// on a dark tab) or too washed out. Same id ⇒ same color, always.
export function colorFromId(id: string): string {
  // FNV-1a 32-bit.
  let h = 0x811c9dc5
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i)
    // Multiply by the FNV prime (16777619) with 32-bit wraparound.
    h = Math.imul(h, 0x01000193) >>> 0
  }
  const span = 220 - 40 // 180
  const chan = (byte: number): string => (40 + (byte % (span + 1))).toString(16).padStart(2, "0")
  const r = chan(h & 0xff)
  const g = chan((h >>> 8) & 0xff)
  const b = chan((h >>> 16) & 0xff)
  return `#${r}${g}${b}`
}

// Full ephemeral identity triple for a given id.
export function ephemeralIdentity(id: string): { id: string; name: string; color: string } {
  return { id, name: ephemeralName(id), color: colorFromId(id) }
}
