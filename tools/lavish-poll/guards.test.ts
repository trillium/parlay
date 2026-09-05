// The pure half of the hot-spin/orphan guards (robots-zahn). The budget
// arithmetic is unit-tested here so spin.test.ts only has to prove the loop
// actually consults it, rather than waiting out a 60s production window.

import { test, expect, describe } from "bun:test"
import { DEFAULT_BUDGET, backoffFor, budgetSpent, isOrphaned, readBudget } from "./guards"

describe("readBudget", () => {
  test("an empty environment yields the production defaults", () => {
    expect(readBudget({})).toEqual(DEFAULT_BUDGET)
  })

  test("a positive override is applied", () => {
    const b = readBudget({ LAVISH_POLL_UNREACHABLE_WINDOW_MS: "400", LAVISH_POLL_MAX_RETRIES: "3" })
    expect(b.windowMs).toBe(400)
    expect(b.maxRetries).toBe(3)
    expect(b.backoffMs).toBe(DEFAULT_BUDGET.backoffMs)
  })

  test("a malformed override keeps the default and is reported, never silently dropped", () => {
    // The whole point of the guard is to bound a runaway loop, so a typo that
    // disarmed it would be worse than no override at all — Number("abc") is NaN
    // and Number("") is 0, and both are falsy in exactly the way that turned a
    // bad --timeout-ms into "never time out".
    const warnings: string[] = []
    const b = readBudget({ LAVISH_POLL_MAX_RETRIES: "abc", LAVISH_POLL_BACKOFF_MS: "0" }, m =>
      warnings.push(m),
    )
    expect(b.maxRetries).toBe(DEFAULT_BUDGET.maxRetries)
    expect(b.backoffMs).toBe(DEFAULT_BUDGET.backoffMs)
    expect(warnings.length).toBe(2)
    expect(warnings.join("\n")).toContain("LAVISH_POLL_MAX_RETRIES")
    expect(warnings.join("\n")).toContain("LAVISH_POLL_BACKOFF_MS")
  })

  test("a cap below the base backoff is raised to it, and reported", () => {
    // Every value here is individually valid, so the loop accepts both — the
    // relation between them is what is wrong. Left alone, backoffFor() would
    // return the 40ms cap for the very first failure and the documented 250ms
    // floor would never be honoured.
    const warnings: string[] = []
    const b = readBudget({ LAVISH_POLL_MAX_BACKOFF_MS: "40" }, m => warnings.push(m))
    expect(b.backoffMs).toBe(DEFAULT_BUDGET.backoffMs)
    expect(b.maxBackoffMs).toBeGreaterThanOrEqual(b.backoffMs)
    expect(b.maxBackoffMs).toBe(DEFAULT_BUDGET.backoffMs)
    expect(warnings.join("\n")).toContain("LAVISH_POLL_MAX_BACKOFF_MS")
    // The first failure still waits the full base backoff.
    expect(backoffFor(1, b)).toBe(DEFAULT_BUDGET.backoffMs)
  })

  test("a cap at or above the base backoff is left exactly as configured", () => {
    const b = readBudget({ LAVISH_POLL_BACKOFF_MS: "100", LAVISH_POLL_MAX_BACKOFF_MS: "800" })
    expect(b.backoffMs).toBe(100)
    expect(b.maxBackoffMs).toBe(800)
  })

  test("an unset variable is not a malformed one", () => {
    const warnings: string[] = []
    readBudget({ LAVISH_POLL_MAX_RETRIES: "" }, m => warnings.push(m))
    expect(warnings).toEqual([])
  })
})

describe("backoffFor", () => {
  const b = { ...DEFAULT_BUDGET, backoffMs: 100, maxBackoffMs: 800 }

  test("no failures means no wait", () => {
    expect(backoffFor(0, b)).toBe(0)
    expect(backoffFor(-1, b)).toBe(0)
  })

  test("it doubles from the first step and then holds at the cap", () => {
    expect([1, 2, 3, 4, 5].map(n => backoffFor(n, b))).toEqual([100, 200, 400, 800, 800])
  })

  test("a very long streak stays at the cap rather than overflowing to Infinity", () => {
    // 2 ** 5000 is Infinity, not an error, so an uncapped expression would have
    // slept forever — a hang dressed as a backoff.
    expect(backoffFor(5_000, b)).toBe(800)
  })
})

describe("budgetSpent", () => {
  const b = { ...DEFAULT_BUDGET, windowMs: 60_000, maxRetries: 30 }

  test("a clean streak is never spent", () => {
    expect(budgetSpent(0, 0, 1_000_000, b)).toBe(false)
  })

  test("it is not spent while both bounds hold", () => {
    expect(budgetSpent(29, 1_000, 1_000 + 59_999, b)).toBe(false)
  })

  test("the retry count alone spends it", () => {
    expect(budgetSpent(30, 1_000, 1_100, b)).toBe(true)
  })

  test("the wall-clock window alone spends it", () => {
    expect(budgetSpent(2, 1_000, 1_000 + 60_000, b)).toBe(true)
  })
})

describe("isOrphaned", () => {
  test("ppid 1 is orphaned, anything else is not", () => {
    expect(isOrphaned(1)).toBe(true)
    expect(isOrphaned(2)).toBe(false)
    // A literal, not process.ppid: the assertion must not depend on what the
    // test runner happens to be parented to.
    expect(isOrphaned(4242)).toBe(false)
  })
})
