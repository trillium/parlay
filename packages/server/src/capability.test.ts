import { describe, expect, test } from "bun:test"
import {
  MAX_ACCEPTS, MAX_DECLARATION_BYTES, MAX_TOKENS, PRESENTATION_COMMANDS,
  countSuppressed, parseDeclaration, recognize, shouldDeliver, suppressedCounts,
  type CapabilityDeclaration,
} from "./capability"

// Mirror of tools/cli/internal/capability's suite — the Go engine is the
// normative contract; these tests keep the TS mirror honest against it.

const valid = () => JSON.stringify({
  schema: "1.0.0",
  surface: { kind: "panel", instance: "dev-42" },
  accepts: { navigate: {}, reload: {} },
  content: ["text", "images"],
  interactions: ["select", "compose"],
})

function declare(...accepts: string[]): CapabilityDeclaration {
  const result = parseDeclaration(JSON.stringify({
    schema: "1.0.0",
    surface: { kind: "panel", instance: "dev-1" },
    accepts: Object.fromEntries(accepts.map(n => [n, {}])),
  }))
  if ("error" in result) throw new Error(`fixture declaration invalid: ${result.error}`)
  return result.decl
}

describe("parseDeclaration", () => {
  test("accepts a valid declaration", () => {
    const result = parseDeclaration(valid())
    if ("error" in result) throw new Error(result.error)
    expect(result.decl.surface).toEqual({ kind: "panel", instance: "dev-42" })
    expect(Object.keys(result.decl.accepts).length).toBe(2)
    expect(result.decl.content.length).toBe(2)
    expect(result.decl.interactions.length).toBe(2)
  })

  test("ignores unknown top-level fields (LSP posture)", () => {
    const raw = valid().replace('"schema"', '"from_the_future": {"x": 1}, "schema"')
    expect("decl" in parseDeclaration(raw)).toBe(true)
  })

  const rejections: Array<[string, string]> = [
    ["not json",                  '{"schema": '],
    ["missing schema",            '{"surface": {"kind": "panel"}}'],
    ["malformed schema",          '{"schema": "1.0", "surface": {"kind": "panel"}}'],
    ["prerelease schema",         '{"schema": "1.0.0-rc1", "surface": {"kind": "panel"}}'],
    ["unsupported major",         '{"schema": "2.0.0", "surface": {"kind": "panel"}}'],
    ["missing kind",              '{"schema": "1.0.0", "surface": {}}'],
    ["uppercase kind",            '{"schema": "1.0.0", "surface": {"kind": "Panel"}}'],
    ["bad instance",              '{"schema": "1.0.0", "surface": {"kind": "panel", "instance": "a b"}}'],
    ["accept name leading digit", '{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"9lives": {}}}'],
    ["accept name uppercase",     '{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"Navigate": {}}}'],
    ["accepts as array",          '{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": ["navigate"]}'],
    ["bad content token",         '{"schema": "1.0.0", "surface": {"kind": "panel"}, "content": ["text/plain"]}'],
    ["bad interactions token",    '{"schema": "1.0.0", "surface": {"kind": "panel"}, "interactions": ["click!"]}'],
  ]
  for (const [name, raw] of rejections) {
    test(`rejects ${name}`, () => expect("error" in parseDeclaration(raw)).toBe(true))
  }

  test("rejects oversized declarations", () => {
    const padded = valid().replace('"dev-42"', `"dev-42", "pad": "${"x".repeat(MAX_DECLARATION_BYTES)}"`)
    expect("error" in parseDeclaration(padded)).toBe(true)
  })

  test("rejects over-cap entry counts", () => {
    const entries = Array.from({ length: MAX_ACCEPTS + 1 }, (_, i) => [`cap_${i}`, {}])
    const overAccepts = JSON.stringify({ schema: "1.0.0", surface: { kind: "panel" }, accepts: Object.fromEntries(entries) })
    expect("error" in parseDeclaration(overAccepts)).toBe(true)

    const tokens = Array.from({ length: MAX_TOKENS + 1 }, (_, i) => `tok_${i}`)
    const overTokens = JSON.stringify({ schema: "1.0.0", surface: { kind: "panel" }, content: tokens })
    expect("error" in parseDeclaration(overTokens)).toBe(true)
  })

  test("preserves accepts detail objects", () => {
    const result = parseDeclaration('{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"device_cmd": {"cmds": ["flash"]}}}')
    if ("error" in result) throw new Error(result.error)
    expect(result.decl.accepts.device_cmd).toEqual({ cmds: ["flash"] })
  })
})

describe("shouldDeliver", () => {
  const declared = declare("navigate", "draft")

  // The gate table from docs/interface-capabilities.md, row by row.
  test("legacy client gets everything", () => {
    expect(shouldDeliver(undefined, "reload")).toBe(true)
    expect(shouldDeliver(undefined, "message")).toBe(true)
  })
  test("declared + accepted command delivers", () => expect(shouldDeliver(declared, "navigate")).toBe(true))
  test("declared + unaccepted command suppresses", () => expect(shouldDeliver(declared, "reload")).toBe(false))
  test("state reports and lifecycle stay ungated", () => {
    expect(shouldDeliver(declared, "message")).toBe(true)
    expect(shouldDeliver(declared, "connected")).toBe(true)
    expect(shouldDeliver(declared, "some_future_event")).toBe(true)
  })

  test("a declaration only subtracts deliveries", () => {
    const narrow = declare("navigate")
    for (const ev of [...PRESENTATION_COMMANDS, "connected", "message", "history", "unheard_of"]) {
      expect(shouldDeliver(undefined, ev)).toBe(true)
      if (shouldDeliver(narrow, ev)) expect(shouldDeliver(undefined, ev)).toBe(true)
    }
  })
})

describe("recognize", () => {
  test("splits accepts into recognized and unknown, sorted", () => {
    const { recognized, unknown } = recognize(declare("navigate", "draft", "teleport", "hologram"))
    expect(recognized).toEqual(["draft", "navigate"])
    expect(unknown).toEqual(["hologram", "teleport"])
  })
  test("empty accepts yields empty arrays, never null", () => {
    expect(recognize(declare())).toEqual({ recognized: [], unknown: [] })
  })
})

test("suppression counters accumulate and report sorted", () => {
  // Deltas, not absolutes: the counter map is module state shared with any
  // other test that exercises the gate.
  const before = suppressedCounts()
  countSuppressed("reload")
  countSuppressed("reload")
  countSuppressed("draft")
  const after = suppressedCounts()
  expect((after.reload ?? 0) - (before.reload ?? 0)).toBe(2)
  expect((after.draft ?? 0) - (before.draft ?? 0)).toBe(1)
  expect(Object.keys(after)).toEqual(Object.keys(after).toSorted())
})
