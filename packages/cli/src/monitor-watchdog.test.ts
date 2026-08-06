import { describe, test, expect } from "bun:test"
import { registryStrike, MISSING_STRIKES_TO_RETIRE } from "./monitor"

// robots-ycfa. A monitor outlives whatever launched it, so when the owning
// session dies without tearing down, the monitor is reparented to init and
// streams a channel nobody owns — forever. 82 of those accumulated. The
// watchdog retires a monitor once the server says its channel is gone.
//
// The whole risk of this feature is the false positive: a monitor that quits on
// a real agent leaves it registered-but-deaf (robots-dcag), which is worse than
// the leak. So these tests are mostly about what must NOT retire it.

const AGENT = "mc-robots-ycfa"
const registry = (...ids: string[]) => ids.map(id => ({ id, name: id, color: "#000" }))

describe("registryStrike — evidence that a channel is gone", () => {
  test("a clean answer that omits the agent is one strike", () => {
    expect(registryStrike(AGENT, registry("main-agent", "deckhand"), true, 0)).toBe(1)
  })

  test("strikes accumulate across consecutive clean answers", () => {
    let s = 0
    s = registryStrike(AGENT, registry("main-agent"), true, s)
    s = registryStrike(AGENT, registry("main-agent"), true, s)
    expect(s).toBe(MISSING_STRIKES_TO_RETIRE)
  })

  test("seeing the agent present resets the count to zero", () => {
    expect(registryStrike(AGENT, registry("main-agent", AGENT), true, 1)).toBe(0)
  })
})

describe("registryStrike — every ambiguity keeps the monitor alive", () => {
  test("a failed request is not evidence", () => {
    expect(registryStrike(AGENT, null, false, 1)).toBe(0)
  })

  test("a non-2xx is not evidence, even with a body", () => {
    expect(registryStrike(AGENT, registry("main-agent"), false, 1)).toBe(0)
  })

  test("an unparseable body is not evidence", () => {
    expect(registryStrike(AGENT, undefined, true, 1)).toBe(0)
  })

  test("a body that is not an array is not evidence", () => {
    expect(registryStrike(AGENT, { error: "nope" }, true, 1)).toBe(0)
  })

  test("an EMPTY registry is not evidence — that is a freshly restarted server", () => {
    expect(registryStrike(AGENT, [], true, 1)).toBe(0)
  })

  test("malformed entries in an otherwise real registry do not crash or falsely clear", () => {
    const body = [null, "nonsense", 42, { name: "no id" }, { id: "main-agent" }]
    expect(registryStrike(AGENT, body, true, 0)).toBe(1)
  })

  test("a single miss between two hits never reaches the retire threshold", () => {
    let s = 0
    s = registryStrike(AGENT, registry(AGENT), true, s)
    s = registryStrike(AGENT, registry("main-agent"), true, s) // one sweep-window blip
    s = registryStrike(AGENT, registry(AGENT), true, s)
    expect(s).toBeLessThan(MISSING_STRIKES_TO_RETIRE)
  })

  test("an id that merely CONTAINS the agent name is not the agent", () => {
    expect(registryStrike(AGENT, registry(`${AGENT}-2`, "x"), true, 0)).toBe(1)
  })
})
