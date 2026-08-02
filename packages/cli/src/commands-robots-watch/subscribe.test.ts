import { test, expect } from "bun:test"
import { isGuardBead, originatingAgent, subscribeLabel, subscribeOnCreate } from "./subscribe"
import { detectEvents, notifyChannels, type StoreState } from "./detect"

// ── subscribe-on-create rules (robots-3q7n) ─────────────────────────────────

test("isGuardBead: only guard-* ids are guard beads (excluded)", () => {
  expect(isGuardBead("guard-bs6vi")).toBe(true)
  expect(isGuardBead("robots-3q7n")).toBe(false)
  expect(isGuardBead("task-780k")).toBe(false)
})

test("originatingAgent: PARLAY_AGENT_ID or null (empty treated as absent)", () => {
  expect(originatingAgent({ PARLAY_AGENT_ID: "mechanic" } as any)).toBe("mechanic")
  expect(originatingAgent({ PARLAY_AGENT_ID: "  " } as any)).toBeNull()
  expect(originatingAgent({} as any)).toBeNull()
})

test("subscribeLabel produces the exact shape notifyChannels parses", () => {
  const label = subscribeLabel("mayor")
  expect(label).toBe("notify:mayor")
  // round-trip: the label we stamp is the one the close handler reads back.
  expect(notifyChannels([label])).toEqual(["mayor"])
})

test("subscribeOnCreate: non-guard + known agent → notify label", () => {
  expect(subscribeOnCreate("robots-3q7n", { PARLAY_AGENT_ID: "shepherd" } as any)).toBe("notify:shepherd")
})

test("subscribeOnCreate: guard bead is EXCLUDED even with an agent", () => {
  expect(subscribeOnCreate("guard-bs6vi", { PARLAY_AGENT_ID: "shepherd" } as any)).toBeNull()
})

test("subscribeOnCreate: no originating agent → no subscribe (clean skip)", () => {
  expect(subscribeOnCreate("robots-3q7n", {} as any)).toBeNull()
})

// ── robots close is now watched (WATCHES extended) ──────────────────────────

test("robots open→closed fires closed (publish-on-close path)", () => {
  const prev: StoreState = { "robots-3q7n": "open" }
  const curr: StoreState = { "robots-3q7n": "closed" }
  const r = detectEvents(prev, curr, "robots", ["created", "closed"])
  expect(r.events).toEqual([{ store: "robots", kind: "closed", id: "robots-3q7n", status: "closed" }])
})
