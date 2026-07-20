// Rotation-advisory decision logic (task-gbs): the pure percent parser and verdict.
// The CLI wrapper (cmdContextCheck) is a thin shell over these — exit 3 on ROTATE.

import { test, expect } from "bun:test"
import {
  parsePercent,
  rotateVerdict,
  EXIT_ROTATE,
  DEFAULT_ROTATE_THRESHOLD,
} from "./commands-context-check"

// ── parsePercent ──────────────────────────────────────────────────────────────────

test("parses a bare integer percent", () => {
  expect(parsePercent("85")).toBe(85)
})

test("parses a trailing-% percent", () => {
  expect(parsePercent("85%")).toBe(85)
})

test("parses a decimal percent", () => {
  expect(parsePercent("85.4")).toBe(85.4)
})

test("scales a fraction ≤ 1 to a percent (0.85 → 85)", () => {
  expect(parsePercent("0.85")).toBe(85)
})

test("treats 1 as a fraction → 100%", () => {
  expect(parsePercent("1")).toBe(100)
})

test("rejects non-numeric input", () => {
  expect(parsePercent("lots")).toBeUndefined()
})

test("rejects negative input", () => {
  expect(parsePercent("-5")).toBeUndefined()
})

test("rejects out-of-range (>100) input", () => {
  expect(parsePercent("101")).toBeUndefined()
})

test("rejects undefined / empty", () => {
  expect(parsePercent(undefined)).toBeUndefined()
  expect(parsePercent("")).toBeUndefined()
  expect(parsePercent("   ")).toBeUndefined()
})

// ── rotateVerdict ───────────────────────────────────────────────────────────────

test("below threshold → OK, exit 0", () => {
  const v = rotateVerdict(50)
  expect(v.rotate).toBe(false)
  expect(v.exitCode).toBe(0)
  expect(v.line).toContain("OK 50%")
})

test("at exactly the threshold → ROTATE, exit 3", () => {
  const v = rotateVerdict(DEFAULT_ROTATE_THRESHOLD)
  expect(v.rotate).toBe(true)
  expect(v.exitCode).toBe(EXIT_ROTATE)
  expect(v.line).toContain("ROTATE")
  expect(v.line).toContain("identity --submit")
})

test("above the threshold → ROTATE, exit 3", () => {
  const v = rotateVerdict(92)
  expect(v.rotate).toBe(true)
  expect(v.exitCode).toBe(3)
})

test("a custom threshold shifts the boundary", () => {
  expect(rotateVerdict(80, 75).rotate).toBe(true)
  expect(rotateVerdict(70, 75).rotate).toBe(false)
})

test("rounds the reported percent to one decimal", () => {
  expect(rotateVerdict(85.44).line).toContain("85.4")
})
