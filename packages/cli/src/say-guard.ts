// The create→submit death-window guard for chat sends.
//
// Forensics on the prior Mayor's abrupt death: it ran `handoff create` but posted a
// courtesy `parlay say` before `identity --submit`, and died in that gap. Recovery only
// worked because the identity pointer is pinned at CREATE time. To make the atomic
// create→submit path the SUPPORTED one, `parlay say`/`reply` warns (loudly, on stderr —
// never blocking) whenever it is called while an OPEN handoff for the agent has not yet
// been submitted.
//
// robots-3yy: A fresh context reusing the same agent-id also sees any prior, unsubmitted
// handoff as "open" — the same nag fires even though the fresh context did NOT create
// that handoff and MUST NOT run `identity --submit` (that would reset a healthy context).
// Fix: classify handoffs as "inherited" (created before this session started) and emit a
// distinct, non-alarming warning that points to `--dismiss-handoff` instead of `--submit`.

import { existsSync, readFileSync, writeFileSync, mkdirSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { detectUnsubmittedHandoff } from "./resolve-handoff"

// Read the handoff pointer currently pinned in an agent's identity.md, if any.
// `identity --submit` pins `> 📎 Handoff: <id> — …`; a pinned pointer means the
// agent's shutdown handoff was already submitted. Returns undefined when the file
// or pointer is absent. Never throws (a bad read must not block a chat send).
export function pinnedHandoffId(agent: string): string | undefined {
  try {
    const base = process.env.PARLAY_AGENT_HOME || join(homedir(), ".parlay", "agents")
    const file = join(base, agent, "identity.md")
    if (!existsSync(file)) return undefined
    const m = readFileSync(file, "utf8").match(/📎 Handoff:\s*(\S+)/)
    return m?.[1]?.trim() || undefined
  } catch {
    return undefined
  }
}

// Read the epoch-ms timestamp written by parlay-spawn to ~/.parlay/agents/<id>/session-start.
// Returns undefined when the file is absent or unparseable. Never throws.
export function readSessionStartMs(agent: string): number | undefined {
  try {
    const base = process.env.PARLAY_AGENT_HOME || join(homedir(), ".parlay", "agents")
    const file = join(base, agent, "session-start")
    if (!existsSync(file)) return undefined
    const sec = parseInt(readFileSync(file, "utf8").trim(), 10)
    return isNaN(sec) ? undefined : sec * 1000
  } catch {
    return undefined
  }
}

// Write a session-start sentinel if one does not yet exist for this agent. Called on
// first `parlay say` as a fallback for agents not spawned via parlay-spawn. The sentinel
// marks "this session began now" so any older open handoff is classified as inherited.
// Never throws — a guard failure must never block a chat send.
export function writeSessionStartOnce(agent: string): void {
  try {
    const base = process.env.PARLAY_AGENT_HOME || join(homedir(), ".parlay", "agents")
    const file = join(base, agent, "session-start")
    if (existsSync(file)) return  // already set (by parlay-spawn or a prior first-say)
    mkdirSync(join(base, agent), { recursive: true })
    writeFileSync(file, Math.floor(Date.now() / 1000).toString() + "\n")
  } catch { /* best-effort */ }
}

// When a chat message is posted while an OPEN handoff exists for this agent that has NOT
// yet been submitted, print a warning on stderr. Two distinct cases:
//
//   Inherited handoff (predates this session): the agent did NOT create it. A fresh
//   context reusing the same agent-id must NOT run `identity --submit` — that resets a
//   healthy context. Show a calm one-liner pointing to `--dismiss-handoff`.
//
//   Current-session handoff (created THIS session, not yet submitted): the agent is
//   posting chat inside the create→submit death window. Show the aggressive shutdown nag.
//
// WARN ONLY — never block the send. Never throws.
export function warnIfUnsubmittedHandoff(agent: string): void {
  try {
    // Write a session-start sentinel on first-say (fallback for non-parlay-spawn agents).
    writeSessionStartOnce(agent)
    const sessionStartedAt = readSessionStartMs(agent)
    const result = detectUnsubmittedHandoff(pinnedHandoffId(agent), undefined, agent, sessionStartedAt)
    if (!result) return

    if (result.inherited) {
      // Gentle: this handoff is from a prior session. Running --submit would wrongly
      // reset a fresh context. Steer toward --dismiss-handoff (non-destructive pin).
      process.stderr.write(
        `💡 parlay: inherited stale handoff ${result.id} for agent '${agent}' (from a prior session).\n` +
        `    To silence this warning without resetting context: identity --dismiss-handoff ${result.id}\n` +
        `    To inspect it: handoff show ${result.id}\n`,
      )
    } else {
      // Aggressive: this is a current-session handoff. The agent is in the create→submit
      // window — posting chat here is what stranded a prior Mayor.
      process.stderr.write(
        `⚠️  parlay: open handoff ${result.id} for ${agent} is NOT yet submitted.\n` +
        `    You are posting chat inside the create→submit window — the exact gap that\n` +
        `    stranded a prior shutdown. Make it atomic: run \`identity --submit\` NOW\n` +
        `    (it auto-resolves ${result.id}), or \`identity --submit ${result.id}\` to be explicit.\n`,
      )
    }
  } catch {
    /* a guard failure must never block a chat send */
  }
}
