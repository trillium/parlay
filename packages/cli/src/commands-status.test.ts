// The fold §3.6 status verb (task-ve2v). The line builder is the load-bearing
// piece — its output MUST parse under firstmate's fm-classify-lib.sh grammar
// (verb, optional "[key=<slug>]" between verb and colon, note after colon), so
// these assertions pin the exact byte shape fm-watch reads.

import { test, expect } from "bun:test"
import { statusLine, statusSink } from "./commands-status"

// ── statusLine — firstmate grammar ─────────────────────────────────────────

test("bare verb + note → '<verb>: <note>'", () => {
  expect(statusLine("working", undefined, "building the verb")).toBe("working: building the verb\n")
})

test("keyed line puts [key=<slug>] between verb and colon", () => {
  expect(statusLine("needs-decision", "api-shape", "which shape")).toBe("needs-decision [key=api-shape]: which shape\n")
})

test("resolved keyed line closes the matching decision", () => {
  expect(statusLine("resolved", "api-shape", "went dual-mode")).toBe("resolved [key=api-shape]: went dual-mode\n")
})

test("empty note omits the trailing space after the colon", () => {
  expect(statusLine("done", undefined, "")).toBe("done:\n")
})

test("keyed line with empty note keeps the token, drops the space", () => {
  expect(statusLine("blocked", "deploy", "")).toBe("blocked [key=deploy]:\n")
})

// ── statusSink — env indirection (fold §3.6) ───────────────────────────────

test("statusSink prefers $PARLAY_STATUS_FILE when set (firstmate's injected sink)", () => {
  const prev = process.env.PARLAY_STATUS_FILE
  process.env.PARLAY_STATUS_FILE = "/tmp/fm-injected.status"
  try {
    expect(statusSink().file).toBe("/tmp/fm-injected.status")
  } finally {
    if (prev === undefined) delete process.env.PARLAY_STATUS_FILE
    else process.env.PARLAY_STATUS_FILE = prev
  }
})

test("statusSink falls back to the per-agent default keyed by PARLAY_AGENT_ID", () => {
  const prevFile = process.env.PARLAY_STATUS_FILE
  const prevId = process.env.PARLAY_AGENT_ID
  delete process.env.PARLAY_STATUS_FILE
  process.env.PARLAY_AGENT_ID = "unit-test-agent"
  try {
    expect(statusSink().file).toMatch(/\.parlay\/agents\/unit-test-agent\/status$/)
  } finally {
    if (prevFile !== undefined) process.env.PARLAY_STATUS_FILE = prevFile
    if (prevId === undefined) delete process.env.PARLAY_AGENT_ID
    else process.env.PARLAY_AGENT_ID = prevId
  }
})
