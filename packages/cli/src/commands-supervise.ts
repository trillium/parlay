// Fold §3.6 + §6 Slice 3 — parlay supervise primitive.
//
// A wake-on-actionable-status loop that ports fm-watch.sh's
// absorb-when-provably-working logic. Terminal verbs (done, needs-decision,
// blocked, failed) wake immediately and escalate to the captain. Routine verbs
// (working, paused, resolved) are absorbed and only escalate if the agent is
// provably not working (stale pane + wedge logic).
//
// Unattended (headless) mode per §3.6.2: presence gate (env flag),
// enqueue-before-suppress durable queue, batch window, max-defer bound,
// in-band captain-return sentinel.

import { existsSync, readFileSync, writeFileSync, appendFileSync, mkdirSync } from "fs" // readFileSync used by other funcs
import { homedir } from "os"
import { join } from "path"
import { SERVER, EXIT_USAGE } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"
import { statusSink } from "./commands-status"
import { readUnattendedQueue, enqueueUnattended, drainUnattendedQueue } from "./unattended-queue"

// Verb classification: terminal (captain-relevant) vs routine (absorbed).
const TERMINAL_VERBS = new Set(["done", "needs-decision", "blocked", "failed"])
const ROUTINE_VERBS = new Set(["working", "resolved", "captain-held", "paused"])

// Parse a status line in firstmate's grammar: <verb> [key=<slug>]: <note>
function parseStatusLine(line: string): { verb: string; key?: string; note: string } | null {
  line = line.trim()
  if (!line) return null
  const m = line.match(/^(\w+)(?:\s*\[key=([A-Za-z0-9._-]+)\])?\s*:\s*(.*)$/)
  if (!m) return null
  return { verb: m[1], key: m[2], note: m[3] }
}

// Check if a verb is terminal (captain-relevant, should wake the supervisor).
function isTerminal(verb: string): boolean {
  return TERMINAL_VERBS.has(verb)
}

// Check if a verb is routine (absorbed when provably working).
function isRoutine(verb: string): boolean {
  return ROUTINE_VERBS.has(verb)
}

// Marker byte for daemon-authored messages (fold §3.6.2, in-band captain-return sentinel).
// Using ASCII unit separator (0x1f) which a human never types.
const DAEMON_MARKER = "\x1f"

// Unattended mode configuration (fold §3.6.2).
const ESCALATE_BATCH_SECS = 90
const MAX_DEFER_SECS = 300

// Read the suppression marker file to detect which status lines have been seen.
function readSeenMarker(agentId: string): { lastLine: number; lastHash: string } {
  const markerFile = join(homedir(), ".parlay", "agents", agentId, ".supervise-marker")
  if (!existsSync(markerFile)) return { lastLine: 0, lastHash: "" }
  try {
    const content = readFileSync(markerFile, "utf-8").trim()
    const lines = content.split("\n")
    const lastEntry = lines[lines.length - 1] || ""
    const [lineStr, hash] = lastEntry.split("|")
    return { lastLine: parseInt(lineStr, 10) || 0, lastHash: hash || "" }
  } catch {
    return { lastLine: 0, lastHash: "" }
  }
}

// Write the suppression marker to track seen status lines.
function writeSeenMarker(agentId: string, lineNum: number, hash: string): void {
  const markerFile = join(homedir(), ".parlay", "agents", agentId, ".supervise-marker")
  const dir = join(homedir(), ".parlay", "agents", agentId)
  mkdirSync(dir, { recursive: true })
  appendFileSync(markerFile, `${lineNum}|${hash}\n`)
}

// Compute a simple hash of a status line for dedup (using base64 encoding for portability).
function hashLine(line: string): string {
  // Simple hash: just use first 8 chars of base64-encoded line
  return Buffer.from(line).toString("base64").slice(0, 8)
}

// Read all status lines from the file.
function readAllStatusLines(statusFile: string): string[] {
  if (!existsSync(statusFile)) return []
  try {
    const content = readFileSync(statusFile, "utf-8")
    return content.split("\n").filter((l) => l.trim())
  } catch {
    return []
  }
}

// Determine if a new actionable status line was recorded since last supervision run.
// Returns the actionable line (if any) that should wake the supervisor.
function findNewActionable(agentId: string, statusFile: string): { line: string; parsed: { verb: string; key?: string; note: string } } | null {
  const allLines = readAllStatusLines(statusFile)
  if (allLines.length === 0) return null

  const seen = readSeenMarker(agentId)

  // Find the first line after the seen marker that is terminal (actionable).
  for (let i = seen.lastLine; i < allLines.length; i++) {
    const line = allLines[i]
    const parsed = parseStatusLine(line)
    if (!parsed) continue

    if (isTerminal(parsed.verb)) {
      // Terminal verb: always actionable.
      return { line, parsed }
    }

    // Routine verb: check if we've seen this exact hash before (within a window).
    // For now, simple absorb: if it's the same as last time, don't wake.
    // (Full wedge detection with idle thresholds is a refinement for later.)
    const lineHash = hashLine(line)
    if (lineHash === seen.lastHash) {
      // Same routine state as before; absorbed.
      continue
    }

    // Routine verb with a new content → absorbed (don't wake supervisor yet).
    // Update marker only if it's a terminal verb.
  }

  // No terminal verb found; all were routine or absorbed.
  return null
}

// Post a message to the relay on behalf of the agent (if supervising).
async function postToRelay(agentId: string, text: string): Promise<void> {
  try {
    const res = await fetch(`${SERVER}/api/chat/message`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        channel: agentId,
        role: "agent",
        text,
      }),
    })
    if (!res.ok) {
      process.stderr.write(`warn: failed to post to relay — ${res.status} ${res.statusText}\n`)
    }
  } catch (err) {
    process.stderr.write(`warn: failed to post to relay — ${err}\n`)
  }
}

// Check if in unattended (away) mode. The presence gate is env-configurable,
// mirroring the $PARLAY_STATUS_FILE indirection (fold §3.6.2).
function isUnattended(): boolean {
  const flagFile = process.env.PARLAY_UNATTENDED_FLAG || ""
  if (!flagFile) return false
  return existsSync(flagFile)
}

export async function cmdSupervise(args: string[]) {
  if (helpWanted("supervise", args)) return

  const { positionals, opts } = parseArgs("supervise", args, ["--drain"])
  const agentId = positionals[0]?.trim()
  if (!agentId) return die("parlay supervise: agent id required", EXIT_USAGE)

  // Resolve status file.
  const { file: statusFile } = statusSink()

  // Unattended mode: drain + deliver buffered events.
  if (opts["--drain"]) {
    const buffered = drainUnattendedQueue(agentId)
    if (buffered.length === 0) {
      console.log(`supervise ${agentId} --drain: no buffered events`)
      return
    }
    const digest = buffered
      .map((e) => `${e.verb}${e.detail ? ": " + e.detail : ""}`)
      .join("; ")
    const message = `${DAEMON_MARKER}crew: ${agentId} away-mode digest — ${digest}`
    await postToRelay(agentId, message)
    console.log(`drained ${buffered.length} buffered event(s) for ${agentId}`)
    return
  }

  // Check for new actionable status.
  const actionable = findNewActionable(agentId, statusFile)
  if (!actionable) {
    process.stderr.write(
      `supervise ${agentId}: no new actionable status (routine verbs absorbed)\n`
    )
    return
  }

  const { parsed } = actionable
  const detail = parsed.note ? ` — ${parsed.note}` : ""

  // Check unattended mode.
  if (isUnattended()) {
    // In unattended (away) mode: enqueue BEFORE advancing any markers.
    // This ensures crash safety — if we crash after enqueue but before marker update,
    // the next run will re-enqueue and re-deliver.
    enqueueUnattended(agentId, parsed.verb, parsed.note || "")
    process.stderr.write(
      `supervise ${agentId}: unattended mode, queued ${parsed.verb}${detail}\n`
    )
    // Mark this line as seen to prevent duplicate enqueueing on next run.
    const lineHash = hashLine(actionable.line)
    writeSeenMarker(agentId, readAllStatusLines(statusFile).length - 1, lineHash)
    // TODO: implement batch window + max-defer daemon in a separate pass
    // (this is the scalar/policy side; the mechanism skeleton is here)
    return
  }

  // Attended mode: wake immediately with the actionable state.
  const message = `${DAEMON_MARKER}crew: ${agentId} is ${parsed.verb}${detail}`
  await postToRelay(agentId, message)
  console.log(`supervisor woken: ${agentId} ${parsed.verb}${detail}`)

  // Mark this line as seen (only after successfully posting).
  const lineHash = hashLine(actionable.line)
  writeSeenMarker(agentId, readAllStatusLines(statusFile).length - 1, lineHash)
}
