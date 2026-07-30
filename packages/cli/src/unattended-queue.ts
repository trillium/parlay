// Unattended (away-mode) queue management for supervise (fold §3.6.2).
// Durable queue ensures no escalations are lost across crashes.

import { existsSync, readFileSync, appendFileSync, mkdirSync, rmSync } from "fs"
import { homedir } from "os"
import { join } from "path"

// Queue entry: timestamp + verb + detail
export interface QueueEntry {
  ts: number
  verb: string
  detail: string
}

// Read the unattended queue for an agent. Returns array of { timestamp, verb, detail } tuples.
export function readUnattendedQueue(agentId: string): QueueEntry[] {
  const queueFile = join(homedir(), ".parlay", "agents", agentId, ".unattended-queue")
  if (!existsSync(queueFile)) return []
  try {
    const lines = readFileSync(queueFile, "utf-8")
      .split("\n")
      .filter((l) => l.trim())
    return lines
      .map((line) => {
        try {
          return JSON.parse(line) as QueueEntry
        } catch {
          return null
        }
      })
      .filter((item) => item !== null)
  } catch {
    return []
  }
}

// Enqueue an event to the unattended queue. MUST happen BEFORE advancing any suppression markers.
// This ensures crash safety — if we crash after enqueue but before marker update,
// the next run will re-enqueue and re-deliver.
export function enqueueUnattended(agentId: string, verb: string, detail: string): void {
  const queueFile = join(homedir(), ".parlay", "agents", agentId, ".unattended-queue")
  const dir = join(homedir(), ".parlay", "agents", agentId)
  mkdirSync(dir, { recursive: true })
  const entry: QueueEntry = { ts: Date.now(), verb, detail }
  appendFileSync(queueFile, `${JSON.stringify(entry)}\n`)
}

// Drain the unattended queue. Returns all buffered events without deleting the file.
// Caller must call clearUnattendedQueue() separately after confirming delivery.
export function drainUnattendedQueue(agentId: string): QueueEntry[] {
  return readUnattendedQueue(agentId)
}

// Clear the unattended queue file. Call only after confirming successful delivery.
export function clearUnattendedQueue(agentId: string): void {
  const queueFile = join(homedir(), ".parlay", "agents", agentId, ".unattended-queue")
  if (existsSync(queueFile)) {
    rmSync(queueFile, { force: true })
  }
}
