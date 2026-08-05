import { describe, test, expect } from "bun:test"
import {
  shouldPrune,
  PROTECTED_CHANNELS,
  PHANTOM_IDS,
  IDLE_PRUNE_MS,
  type PruneMode,
} from "./prune"
import { LISTEN_WINDOW_MS } from "./sse"

// The real 23-channel registry as returned by GET /api/chat/agents on 2026-07-14.
// 7 protected + 15 test orphans + 1 phantom (firstmate).
const REGISTRY_IDS = [
  "main-agent", "lavish", "parlay-panel", "resume", "deckhand", "parlay-dev",
  "nonexistent-bench-channel-xyz", "bench-probe-nonexistent-channel",
  "forge-probe-88764", "forge-iso-a-90498", "forge-iso-b-90498",
  "forge-agent-b", "forge-agent-a", "forge-b-b", "forge-b-a",
  "meas-b", "meas-c", "meas-a", "meas-old",
  "test-agent", "forge-deploy-test",
  "muse", "firstmate",
] as const

const PROTECTED = ["main-agent", "lavish", "parlay-panel", "resume", "deckhand", "parlay-dev", "muse"]
const ORPHANS = [
  "nonexistent-bench-channel-xyz", "bench-probe-nonexistent-channel",
  "forge-probe-88764", "forge-iso-a-90498", "forge-iso-b-90498",
  "forge-agent-b", "forge-agent-a", "forge-b-b", "forge-b-a",
  "meas-b", "meas-c", "meas-a", "meas-old",
  "test-agent", "forge-deploy-test",
]
const PHANTOM = ["firstmate"]

const NOW = 1_800_000_000_000 // fixed clock so tests are deterministic

// Helper: run the predicate for every id with no freshness data (the startup
// reality — lastPollByChannel empty), collect the ids that would be pruned.
function prunedIds(mode: PruneMode, lastSeen: Record<string, number> = {}): string[] {
  return REGISTRY_IDS.filter(id =>
    shouldPrune({ id, lastSeenMs: lastSeen[id], nowMs: NOW, mode }).prune,
  )
}

describe("shouldPrune — fixture-driven policy", () => {
  test("startup sweep prunes exactly the 15 orphans + firstmate, keeps all 7 protected", () => {
    const pruned = prunedIds("startup").sort()
    const expected = [...ORPHANS, ...PHANTOM].sort()
    expect(pruned).toEqual(expected)
    // Nothing protected was touched.
    for (const p of PROTECTED) expect(pruned).not.toContain(p)
    // Resulting count matches the captain's target: 23 → 7.
    expect(REGISTRY_IDS.length - pruned.length).toBe(PROTECTED.length)
  })

  test("periodic sweep with no freshness data matches startup (name/phantom only)", () => {
    expect(prunedIds("periodic").sort()).toEqual(prunedIds("startup").sort())
  })

  test("every protected channel is kept in both modes, even with ancient lastSeen", () => {
    const ancient = Object.fromEntries(PROTECTED.map(p => [p, NOW - 10 * IDLE_PRUNE_MS]))
    for (const mode of ["startup", "periodic"] as PruneMode[]) {
      for (const p of PROTECTED) {
        const d = shouldPrune({ id: p, lastSeenMs: ancient[p], nowMs: NOW, mode })
        expect(d.prune).toBe(false)
      }
    }
  })

  test("static whitelist and phantom set do not overlap", () => {
    for (const p of PHANTOM_IDS) expect(PROTECTED_CHANNELS.has(p)).toBe(false)
  })
})

describe("shouldPrune — dynamic active-window protection", () => {
  test("a test-named channel seen within the listen window is KEPT (conservative)", () => {
    const d = shouldPrune({
      id: "forge-agent-a",
      lastSeenMs: NOW - Math.floor(LISTEN_WINDOW_MS / 2),
      nowMs: NOW,
      mode: "periodic",
    })
    expect(d.prune).toBe(false)
    expect(d.reason).toContain("active")
  })

  test("the same channel just outside the window falls back to its test-pattern prune", () => {
    const d = shouldPrune({
      id: "forge-agent-a",
      lastSeenMs: NOW - (LISTEN_WINDOW_MS + 1000),
      nowMs: NOW,
      mode: "periodic",
    })
    expect(d.prune).toBe(true)
    expect(d.reason).toContain("test-pattern")
  })
})

describe("shouldPrune — idle-threshold prune (periodic only)", () => {
  const nonTestId = "some-real-looking-channel"

  test("a non-test channel idle beyond threshold is pruned in periodic mode", () => {
    const d = shouldPrune({ id: nonTestId, lastSeenMs: NOW - (IDLE_PRUNE_MS + 1), nowMs: NOW, mode: "periodic" })
    expect(d.prune).toBe(true)
    expect(d.reason).toContain("idle")
  })

  test("the same idle channel is KEPT at startup (no reliable freshness data)", () => {
    const d = shouldPrune({ id: nonTestId, lastSeenMs: NOW - (IDLE_PRUNE_MS + 1), nowMs: NOW, mode: "startup" })
    expect(d.prune).toBe(false)
  })

  test("a non-test channel idle just under threshold is KEPT in periodic mode", () => {
    const d = shouldPrune({ id: nonTestId, lastSeenMs: NOW - (IDLE_PRUNE_MS - 1000), nowMs: NOW, mode: "periodic" })
    expect(d.prune).toBe(false)
  })

  test("a never-seen non-test channel is KEPT in both modes (unsure → keep)", () => {
    for (const mode of ["startup", "periodic"] as PruneMode[]) {
      expect(shouldPrune({ id: nonTestId, lastSeenMs: undefined, nowMs: NOW, mode }).prune).toBe(false)
    }
  })
})
