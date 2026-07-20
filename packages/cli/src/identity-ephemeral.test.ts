// Unit tests for the pure ephemeral-identity helpers.

import { test, expect } from "bun:test"
import {
  ephemeralHash,
  generateEphemeralId,
  ephemeralName,
  colorFromId,
  ephemeralIdentity,
} from "./identity-ephemeral"

test("ephemeralHash yields 'eph-' + 8 lowercase hex chars", () => {
  for (let i = 0; i < 50; i++) {
    const id = ephemeralHash()
    expect(id).toMatch(/^eph-[0-9a-f]{8}$/)
  }
})

test("generateEphemeralId returns the first candidate when it does not collide", () => {
  const id = generateEphemeralId(() => false)
  expect(id).toMatch(/^eph-[0-9a-f]{8}$/)
})

test("generateEphemeralId retries once on collision and returns a fresh id", () => {
  let calls = 0
  const seen: string[] = []
  // The first candidate "collides"; the generator must then produce a second,
  // distinct candidate. We record every id the predicate is asked about.
  const id = generateEphemeralId((candidate) => {
    calls++
    seen.push(candidate)
    return calls === 1 // only the first candidate collides
  })
  expect(id).toMatch(/^eph-[0-9a-f]{8}$/)
  expect(calls).toBe(1) // the collided first candidate is checked; the retry is taken
  expect(id).not.toBe(seen[0]) // the returned id is NOT the colliding one
})

test("colorFromId returns a valid #rrggbb and is deterministic", () => {
  const c1 = colorFromId("eph-a3f21b4c")
  const c2 = colorFromId("eph-a3f21b4c")
  expect(c1).toMatch(/^#[0-9a-f]{6}$/)
  expect(c1).toBe(c2)
})

test("colorFromId keeps every channel in the readable 0x28–0xdc range", () => {
  for (const id of ["eph-00000000", "eph-ffffffff", "eph-deadbeef", "mayor", "fable", "x"]) {
    const c = colorFromId(id)
    expect(c).toMatch(/^#[0-9a-f]{6}$/)
    const r = parseInt(c.slice(1, 3), 16)
    const g = parseInt(c.slice(3, 5), 16)
    const b = parseInt(c.slice(5, 7), 16)
    for (const v of [r, g, b]) {
      expect(v).toBeGreaterThanOrEqual(0x28) // 40
      expect(v).toBeLessThanOrEqual(0xdc) // 220
    }
  }
})

test("colorFromId differs for different ids (no trivial constant)", () => {
  const a = colorFromId("eph-11111111")
  const b = colorFromId("eph-22222222")
  expect(a).not.toBe(b)
})

test("ephemeralName is 'Agent ' + uppercased 8 hex chars", () => {
  expect(ephemeralName("eph-a3f21b4c")).toBe("Agent A3F21B4C")
})

test("ephemeralIdentity bundles id/name/color consistently", () => {
  const id = "eph-deadbeef"
  const ident = ephemeralIdentity(id)
  expect(ident.id).toBe(id)
  expect(ident.name).toBe("Agent DEADBEEF")
  expect(ident.color).toBe(colorFromId(id))
})
