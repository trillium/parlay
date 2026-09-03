import { describe, test, expect, afterEach } from "bun:test"
import {
  shouldIdleReap,
  idleReapThresholdMs,
  idleReapSweep,
  DEFAULT_IDLE_REAP_MS,
} from "."
import { agents, lastPollByChannel } from "../sse"

const NOW = 1_800_000_000_000 // fixed clock so tests are deterministic

describe("shouldIdleReap — pure predicate", () => {
  test("an agent with no launchedBy is never reaped, no matter how idle", () => {
    const d = shouldIdleReap({
      id: "firstmate-spawned-1",
      launchedBy: undefined,
      lastSeenMs: NOW - 10 * DEFAULT_IDLE_REAP_MS,
      nowMs: NOW,
      thresholdMs: DEFAULT_IDLE_REAP_MS,
    })
    expect(d.reap).toBe(false)
    expect(d.reason).toContain("not a Parlay-launched")
  })

  test("launchedBy values from both spawn paths are recognized", () => {
    for (const launchedBy of ["parlay-spawn", "parlay-claim"]) {
      const d = shouldIdleReap({
        id: "x",
        launchedBy,
        lastSeenMs: NOW - DEFAULT_IDLE_REAP_MS - 1,
        nowMs: NOW,
        thresholdMs: DEFAULT_IDLE_REAP_MS,
      })
      expect(d.reap).toBe(true)
    }
  })

  test("a Parlay-launched agent with no liveness data yet is kept, not reaped", () => {
    const d = shouldIdleReap({
      id: "just-spawned",
      launchedBy: "parlay-spawn",
      lastSeenMs: undefined,
      nowMs: NOW,
      thresholdMs: DEFAULT_IDLE_REAP_MS,
    })
    expect(d.reap).toBe(false)
    expect(d.reason).toContain("no liveness data")
  })

  test("a Parlay-launched agent active within the threshold is kept", () => {
    const d = shouldIdleReap({
      id: "busy",
      launchedBy: "parlay-claim",
      lastSeenMs: NOW - Math.floor(DEFAULT_IDLE_REAP_MS / 2),
      nowMs: NOW,
      thresholdMs: DEFAULT_IDLE_REAP_MS,
    })
    expect(d.reap).toBe(false)
    expect(d.reason).toContain("active")
  })

  test("a Parlay-launched agent idle past the threshold is reaped", () => {
    const d = shouldIdleReap({
      id: "stale",
      launchedBy: "parlay-spawn",
      lastSeenMs: NOW - (DEFAULT_IDLE_REAP_MS + 1),
      nowMs: NOW,
      thresholdMs: DEFAULT_IDLE_REAP_MS,
    })
    expect(d.reap).toBe(true)
    expect(d.reason).toContain("idle")
  })

  test("exactly at the threshold is reaped (idleMs < thresholdMs is the only kept case)", () => {
    const d = shouldIdleReap({
      id: "boundary",
      launchedBy: "parlay-spawn",
      lastSeenMs: NOW - DEFAULT_IDLE_REAP_MS,
      nowMs: NOW,
      thresholdMs: DEFAULT_IDLE_REAP_MS,
    })
    expect(d.reap).toBe(true)
  })
})

describe("idleReapThresholdMs — env override", () => {
  const ENV_KEY = "PARLAY_AGENT_IDLE_TIMEOUT_MS"
  const original = process.env[ENV_KEY]
  afterEach(() => {
    if (original === undefined) delete process.env[ENV_KEY]
    else process.env[ENV_KEY] = original
  })

  test("defaults to 2h when unset", () => {
    delete process.env[ENV_KEY]
    expect(idleReapThresholdMs()).toBe(DEFAULT_IDLE_REAP_MS)
  })

  test("honors a valid positive override", () => {
    process.env[ENV_KEY] = "60000"
    expect(idleReapThresholdMs()).toBe(60_000)
  })

  test("falls back to the default on a non-numeric value", () => {
    process.env[ENV_KEY] = "not-a-number"
    expect(idleReapThresholdMs()).toBe(DEFAULT_IDLE_REAP_MS)
  })

  test("falls back to the default on zero or negative", () => {
    process.env[ENV_KEY] = "0"
    expect(idleReapThresholdMs()).toBe(DEFAULT_IDLE_REAP_MS)
    process.env[ENV_KEY] = "-5"
    expect(idleReapThresholdMs()).toBe(DEFAULT_IDLE_REAP_MS)
  })
})

describe("idleReapSweep — end to end against the live registry", () => {
  afterEach(() => {
    for (const id of ["reap-me-z1", "keep-firstmate-z1", "keep-active-z1", "keep-nodata-z1"]) {
      agents.delete(id)
      lastPollByChannel.delete(id)
    }
    delete process.env.PARLAY_AGENT_IDLE_TIMEOUT_MS
  })

  test("reaps a Parlay-launched agent idle past threshold via the #234 unregister primitive", () => {
    process.env.PARLAY_AGENT_IDLE_TIMEOUT_MS = String(60_000)
    agents.set("reap-me-z1", { id: "reap-me-z1", name: "reap-me", color: "#000", launchedBy: "parlay-spawn", startedAt: new Date(NOW - 200_000).toISOString() })
    lastPollByChannel.set("reap-me-z1", NOW - 120_000)

    const result = idleReapSweep(NOW)

    expect(result.reaped.map(r => r.id)).toContain("reap-me-z1")
    // unregisterAgent's actual effects — same primitive `parlay shutdown` drives.
    expect(agents.has("reap-me-z1")).toBe(false)
    expect(lastPollByChannel.has("reap-me-z1")).toBe(false)
  })

  test("never reaps a firstmate-spawned (no launchedBy) agent, however idle", () => {
    process.env.PARLAY_AGENT_IDLE_TIMEOUT_MS = String(60_000)
    agents.set("keep-firstmate-z1", { id: "keep-firstmate-z1", name: "fm", color: "#000" })
    lastPollByChannel.set("keep-firstmate-z1", NOW - 10 * 60_000)

    const result = idleReapSweep(NOW)

    expect(result.reaped.map(r => r.id)).not.toContain("keep-firstmate-z1")
    expect(agents.has("keep-firstmate-z1")).toBe(true)
  })

  test("never reaps a Parlay-launched agent still active within the threshold", () => {
    process.env.PARLAY_AGENT_IDLE_TIMEOUT_MS = String(60_000)
    agents.set("keep-active-z1", { id: "keep-active-z1", name: "active", color: "#000", launchedBy: "parlay-claim" })
    lastPollByChannel.set("keep-active-z1", NOW - 1000)

    const result = idleReapSweep(NOW)

    expect(result.reaped.map(r => r.id)).not.toContain("keep-active-z1")
    expect(agents.has("keep-active-z1")).toBe(true)
  })

  test("never reaps a Parlay-launched agent with no liveness data yet", () => {
    process.env.PARLAY_AGENT_IDLE_TIMEOUT_MS = String(60_000)
    agents.set("keep-nodata-z1", { id: "keep-nodata-z1", name: "fresh", color: "#000", launchedBy: "parlay-spawn" })
    // deliberately no lastPollByChannel entry

    const result = idleReapSweep(NOW)

    expect(result.reaped.map(r => r.id)).not.toContain("keep-nodata-z1")
    expect(agents.has("keep-nodata-z1")).toBe(true)
  })

  test("double-reap is safe — a second sweep after removal is a no-op for that id", () => {
    process.env.PARLAY_AGENT_IDLE_TIMEOUT_MS = String(60_000)
    agents.set("reap-me-z1", { id: "reap-me-z1", name: "reap-me", color: "#000", launchedBy: "parlay-spawn" })
    lastPollByChannel.set("reap-me-z1", NOW - 120_000)

    const first = idleReapSweep(NOW)
    expect(first.reaped.map(r => r.id)).toContain("reap-me-z1")

    // Second pass: the id is already gone from `agents`, so the loop never
    // considers it again — proving idempotency end to end, not just relying
    // on unregisterAgent's own idempotent-404 behavior at the HTTP layer.
    const second = idleReapSweep(NOW + 1)
    expect(second.reaped.map(r => r.id)).not.toContain("reap-me-z1")
  })
})
