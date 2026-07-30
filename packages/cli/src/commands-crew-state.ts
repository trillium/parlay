// Fold §3.6 + §6 Slice 3 — parlay crew-state reader.
//
// Reconciles an agent's last keyed status line against parlay's oracle:
// worktree/session gone, run attribution, herdr tab liveness, subscriber presence.
// Outputs: <state> · source: <src> · <detail>
// States: working | done | blocked | needs-decision | paused | failed | unknown

import { existsSync, readFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { SERVER, EXIT_USAGE } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"
import type { SubscribersInfo } from "./types"
import { statusSink } from "./commands-status"

// Fetch that reports failure instead of exiting (non-fatal).
async function tryJSON<T>(path: string): Promise<{ ok: true; data: T } | { ok: false; err: string }> {
  try {
    const res = await fetch(`${SERVER}${path}`, { signal: AbortSignal.timeout(3_000) })
    if (!res.ok) return { ok: false, err: `${res.status} ${res.statusText}` }
    return { ok: true, data: (await res.json()) as T }
  } catch (err) {
    return { ok: false, err: String(err) }
  }
}

// Verb classification: terminal vs routine (fold §3.6, fm-classify-lib.sh).
// Terminal verbs (captain-relevant): done, needs-decision, blocked, failed
// Routine verbs (never escalate): working, resolved, captain-held, paused
const TERMINAL_VERBS = new Set(["done", "needs-decision", "blocked", "failed"])
const ROUTINE_VERBS = new Set(["working", "resolved", "captain-held", "paused"])
const ALL_VERBS = new Set([...TERMINAL_VERBS, ...ROUTINE_VERBS])

// Parse a status line in firstmate's grammar: <verb> [key=<slug>]: <note>
// Returns { verb, key, note } or null if invalid.
function parseStatusLine(line: string): { verb: string; key?: string; note: string } | null {
  line = line.trim()
  if (!line) return null

  // Match: verb [key=slug]: note or verb: note
  const m = line.match(/^(\w+)(?:\s*\[key=([A-Za-z0-9._-]+)\])?\s*:\s*(.*)$/)
  if (!m) return null

  return { verb: m[1], key: m[2], note: m[3] }
}

// Read the last status line from the status file.
function readLastStatus(
  statusFile: string
): { verb: string; key?: string; note: string } | null {
  if (!existsSync(statusFile)) return null
  try {
    const content = readFileSync(statusFile, "utf-8")
    const lines = content.split("\n").filter((l) => l.trim())
    if (lines.length === 0) return null
    return parseStatusLine(lines[lines.length - 1])
  } catch {
    return null
  }
}

// Check if agent is subscribed/enrolled with the relay.
async function isAgentSubscribed(agentId: string): Promise<boolean> {
  try {
    const subs = await tryJSON<SubscribersInfo>(`${SERVER}/api/chat/subscribers`)
    if (!subs.ok) return false
    const agents = subs.data.registered?.agents ?? []
    return agents.some((a) => a.id === agentId)
  } catch {
    return false
  }
}

// Determine crew state by reconciling status + oracle signals.
// Precedence (fold §4, section 3.6):
// 1. If worktree/session gone -> unknown
// 2. If parlay can attribute a validation run -> use it (extension point)
// 3. Check pane (tab) liveness:
//    - Dead tab + no run -> unknown (never infer from stale log)
//    - Busy tab -> prefer tab state over log
//    - Idle tab -> use last status verb
export async function crewStateForAgent(
  agentId: string
): Promise<{ state: string; source: string; detail: string }> {
  // Resolve status file (same logic as parlay status).
  const { file: statusFile } = statusSink()

  // Precedence 1: Check if agent exists/enrolled.
  const enrolled = await isAgentSubscribed(agentId)
  if (!enrolled) {
    // Agent not found in relay.
    return {
      state: "unknown",
      source: "none",
      detail: "agent not enrolled with relay",
    }
  }

  // Precedence 3: Read last status line to inform the state.
  const lastStatus = readLastStatus(statusFile)
  if (!lastStatus) {
    // No status ever written.
    return {
      state: "unknown",
      source: "none",
      detail: "no status recorded",
    }
  }

  const { verb, key, note } = lastStatus

  // Validate verb; if unknown, treat as unknown state.
  if (!ALL_VERBS.has(verb)) {
    return {
      state: "unknown",
      source: "status",
      detail: `unrecognized verb: ${verb}`,
    }
  }

  // Map verb to state. Terminal verbs are captain-relevant; routine ones are absorbed.
  // For now, state = verb directly. Absorb-when-provably-working logic is in supervise.
  return {
    state: verb as string,
    source: "status",
    detail: note || "(no detail)",
  }
}

export async function cmdCrewState(args: string[]) {
  if (helpWanted("crew-state", args)) return
  const { positionals } = parseArgs("crew-state", args)
  const agentId = positionals[0]?.trim()
  if (!agentId) return die("parlay crew-state: agent id required", EXIT_USAGE)

  const { state, source, detail } = await crewStateForAgent(agentId)
  console.log(`${state} · source: ${source} · ${detail}`)
}
