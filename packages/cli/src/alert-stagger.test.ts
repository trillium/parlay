import { test, expect } from "bun:test"
import { chunk, jitterMs, staggerDeliver, resolveAlertMode } from "./alert-stagger"

// resolveAlertMode — the pure delivery decision (no network, no fleet needed).
const base = { stagger: false, noStagger: false, hasAgent: false, fleetSize: 3, threshold: 8 }

test("single --agent → single, regardless of everything else", () => {
  expect(resolveAlertMode({ ...base, hasAgent: true, stagger: true, fleetSize: 100 })).toBe("single")
})

test("--no-stagger → immediate, even on a huge fleet", () => {
  expect(resolveAlertMode({ ...base, noStagger: true, fleetSize: 100 })).toBe("immediate")
})

test("explicit --stagger → stagger when anyone is enrolled, immediate when fleet empty", () => {
  expect(resolveAlertMode({ ...base, stagger: true, fleetSize: 1 })).toBe("stagger")
  expect(resolveAlertMode({ ...base, stagger: true, fleetSize: 0 })).toBe("immediate")
})

test("auto: small fleet (<= threshold) → immediate", () => {
  expect(resolveAlertMode({ ...base, fleetSize: 8, threshold: 8 })).toBe("immediate")
})

test("auto: large fleet (> threshold) → stagger without the flag", () => {
  expect(resolveAlertMode({ ...base, fleetSize: 9, threshold: 8 })).toBe("stagger")
  expect(resolveAlertMode({ ...base, fleetSize: 50, threshold: 8 })).toBe("stagger")
})

test("chunk splits into batches of size, last is remainder", () => {
  expect(chunk([1, 2, 3, 4, 5], 2)).toEqual([[1, 2], [3, 4], [5]])
  expect(chunk([1, 2, 3], 1)).toEqual([[1], [2], [3]])
  expect(chunk([1, 2, 3], 10)).toEqual([[1, 2, 3]])
})

test("chunk coerces size < 1 to 1 (never zero-length batches)", () => {
  expect(chunk([1, 2], 0)).toEqual([[1], [2]])
})

test("jitterMs stays within +/-25% of base", () => {
  expect(jitterMs(1000, () => 0)).toBe(750) // low end 0.75x
  expect(jitterMs(1000, () => 1)).toBe(1250) // high end 1.25x
  expect(jitterMs(1000, () => 0.5)).toBe(1000) // midpoint
})

test("staggerDeliver sleeps BETWEEN batches only (not after the last)", async () => {
  const sleeps: number[] = []
  const sleep = async (ms: number) => { sleeps.push(ms) }
  // stub the network by intercepting fetch via a fake postAlert is internal; instead
  // drive through the real path with 3 agents, batch 1 → 3 batches → 2 sleeps.
  const origFetch = globalThis.fetch
  // @ts-expect-error minimal stub
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ channels: 1, delivered: 1 }) })
  try {
    await staggerDeliver("hi", ["a", "b", "c"], 2, 1, "test", sleep, () => 0.5)
  } finally {
    globalThis.fetch = origFetch
  }
  // 3 batches → exactly 2 inter-batch sleeps, each the jittered 2s base (0.5 rnd → 2000ms).
  expect(sleeps).toEqual([2000, 2000])
})

test("staggerDeliver with batch size 2 over 5 agents → 3 batches → 2 sleeps", async () => {
  const sleeps: number[] = []
  const sleep = async (ms: number) => { sleeps.push(ms) }
  const origFetch = globalThis.fetch
  // @ts-expect-error minimal stub
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ channels: 1, delivered: 2 }) })
  try {
    await staggerDeliver("hi", ["a", "b", "c", "d", "e"], 1, 2, "test", sleep, () => 0.5)
  } finally {
    globalThis.fetch = origFetch
  }
  expect(sleeps.length).toBe(2)
})
