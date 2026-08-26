// Cursor persistence (robots-nm8).
//
// lastParlayId used to live only in process memory, but each emit() calls
// process.exit(0) and the tool's own next_step tells the agent to re-run
// `lavish poll`. On re-arm a fresh process started at after='' and re-fetched
// the oldest un-consumed role=user message, so an alert was re-delivered on
// EVERY re-arm forever until a newer user message arrived (agent replies are
// role=agent and never cleared it). Fix: persist the cursor to a temp file
// keyed by file+agent and seed after= from it on startup.
//
// Best-effort throughout — a read/write failure degrades to the old
// in-memory-only behaviour and never blocks polling.

import { createHash } from "node:crypto"
import { readFileSync, writeFileSync, mkdirSync } from "node:fs"
import { join } from "node:path"

export function cursorPath(agentId: string, file: string): string {
  const runtime = (
    process.env.PARLAY_RELAY_RUNTIME || join(process.env.TMPDIR || "/tmp", "parlay")
  ).replace(/\/$/, "")
  const key = createHash("sha256").update(`${agentId}\0${file}`).digest("hex").slice(0, 16)
  try {
    mkdirSync(runtime, { recursive: true })
  } catch {}
  return join(runtime, `lavish-poll-${key}.cursor`)
}

export function readCursor(agentId: string, file: string): string {
  try {
    return readFileSync(cursorPath(agentId, file), "utf8").trim()
  } catch {
    return ""
  }
}

export function writeCursor(agentId: string, file: string, id: string): void {
  try {
    writeFileSync(cursorPath(agentId, file), id)
  } catch {}
}
