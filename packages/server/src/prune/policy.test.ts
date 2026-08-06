import { describe, test, expect } from "bun:test"
import {
  shouldPrune,
  unregisterAgent,
  tombstone,
  isTombstoned,
  clearTombstone,
  tombstones,
  TOMBSTONE_TTL_MS,
  PROTECTED_CHANNELS,
  PHANTOM_IDS,
  IDLE_PRUNE_MS,
  type PruneMode,
} from "."
import { agents, LISTEN_WINDOW_MS } from "../sse"

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
  // robots-ycfa: this used to assert the opposite — that a live poll spared a
  // test-named channel. It is exactly the rule that let 82 leaked fixtures
  // accumulate: a leak's own orphaned listener polls forever, so "active" was
  // permanently true for every single one of them and the hourly sweep skipped
  // them all. For a name only a harness produces, polling IS the leak.
  test("a test-named channel is pruned even while actively polling", () => {
    const d = shouldPrune({
      id: "forge-agent-a",
      lastSeenMs: NOW - Math.floor(LISTEN_WINDOW_MS / 2),
      nowMs: NOW,
      mode: "periodic",
    })
    expect(d.prune).toBe(true)
    expect(d.reason).toContain("test-pattern")
  })

  test("a phantom id is pruned even while actively polling", () => {
    const d = shouldPrune({
      id: "firstmate",
      lastSeenMs: NOW - Math.floor(LISTEN_WINDOW_MS / 2),
      nowMs: NOW,
      mode: "periodic",
    })
    expect(d.prune).toBe(true)
    expect(d.reason).toContain("phantom")
  })

  test("the same channel just outside the window still prunes on its test pattern", () => {
    const d = shouldPrune({
      id: "forge-agent-a",
      lastSeenMs: NOW - (LISTEN_WINDOW_MS + 1000),
      nowMs: NOW,
      mode: "periodic",
    })
    expect(d.prune).toBe(true)
    expect(d.reason).toContain("test-pattern")
  })

  // The activity check still does its job for every channel the NAME rules do
  // not claim — which is the only place it could ever have been sound.
  test("an ordinary channel active within the window is kept from the idle rule", () => {
    const d = shouldPrune({
      id: "some-real-looking-channel",
      lastSeenMs: NOW - Math.floor(LISTEN_WINDOW_MS / 2),
      nowMs: NOW,
      mode: "periodic",
    })
    expect(d.prune).toBe(false)
    expect(d.reason).toContain("active")
  })

  test("a protected channel outranks every name rule", () => {
    for (const p of PROTECTED) {
      expect(shouldPrune({ id: p, lastSeenMs: NOW, nowMs: NOW, mode: "periodic" }).prune).toBe(false)
    }
  })
})

// The exact ids observed leaked on 2026-08-05 (robots-ycfa): 82 orphaned
// listeners from firstmate's test suite, every one of them polling.
describe("shouldPrune — firstmate test-fixture families", () => {
  const LEAKED_FIXTURES = [
    "profile-off-z1", "profile-claude-z2", "profile-grok-xhigh-z6b",
    "profile-batch-a-z9", "profile-relative-paths-z1b", "profile-secondmate-z16",
    "busy-pi-1", "busy-cl-2", "busy-pi-order", "busy-cx-1",
    "spawn-beads-absent-z2", "spawn-beads-hooksect-z5",
    "settle-single-stale-z1", "settle-already-settled-z2",
    "account-one-z1", "account-off-z2",
    "nobackendz3", "explicitbackendz4", "nestbackendz5", "orcaspawnz1",
  ]

  test("every leaked fixture family is pruned, actively polling and at startup", () => {
    for (const id of LEAKED_FIXTURES) {
      for (const mode of ["startup", "periodic"] as PruneMode[]) {
        const d = shouldPrune({ id, lastSeenMs: NOW, nowMs: NOW, mode })
        expect(`${id}:${mode}:${d.prune}`).toBe(`${id}:${mode}:true`)
      }
    }
  })

  // Guards the patterns against over-reach: these are the shapes real agent ids
  // actually take on this fleet, and none of them may match.
  test("real agent ids are untouched by the fixture patterns", () => {
    const REAL = [
      "main-agent", "mc-robots-ycfa", "parlay-leg1", "nm-review-gate",
      "herdr-web-proto19", "task-a2s4", "shipwright", "deckhand",
      "gascity-deadlink", "upstream-sweep", "remote-dispatch-impl",
    ]
    for (const id of REAL) {
      const d = shouldPrune({ id, lastSeenMs: NOW, nowMs: NOW, mode: "periodic" })
      expect(`${id}:${d.prune}`).toBe(`${id}:false`)
    }
  })
})

describe("tombstones — a removal that sticks", () => {
  test("unregisterAgent tombstones the id so a later poll cannot resurrect it", () => {
    agents.set("leaky-fixture-z1", { id: "leaky-fixture-z1", name: "leaky", color: "#000" })
    expect(unregisterAgent("leaky-fixture-z1").ok).toBe(true)
    expect(isTombstoned("leaky-fixture-z1")).toBe(true)
  })

  test("a tombstone expires after its TTL, so the id is reusable later", () => {
    tombstone("expiring-z1", NOW)
    expect(isTombstoned("expiring-z1", NOW + TOMBSTONE_TTL_MS - 1)).toBe(true)
    expect(isTombstoned("expiring-z1", NOW + TOMBSTONE_TTL_MS + 1)).toBe(false)
  })

  test("an expired tombstone is dropped from the map, so it cannot grow unbounded", () => {
    tombstone("gc-me-z1", NOW)
    isTombstoned("gc-me-z1", NOW + TOMBSTONE_TTL_MS + 1)
    expect(tombstones.has("gc-me-z1")).toBe(false)
  })

  test("clearTombstone lifts the refusal immediately — a real re-enroll works first try", () => {
    tombstone("re-enrolling", NOW)
    expect(isTombstoned("re-enrolling", NOW)).toBe(true)
    clearTombstone("re-enrolling")
    expect(isTombstoned("re-enrolling", NOW)).toBe(false)
  })

  test("a never-removed channel is not tombstoned", () => {
    expect(isTombstoned("never-touched")).toBe(false)
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
