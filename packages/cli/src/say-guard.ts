// The create→submit death-window guard for chat sends.
//
// Forensics on the prior Mayor's abrupt death: it ran `handoff create` but posted a
// courtesy `parlay say` before `identity --submit`, and died in that gap. Recovery only
// worked because the identity pointer is pinned at CREATE time. To make the atomic
// create→submit path the SUPPORTED one, `parlay say`/`reply` warns (loudly, on stderr —
// never blocking) whenever it is called while an OPEN handoff for the agent has not yet
// been submitted.

import { existsSync, readFileSync } from "fs"
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

// When a chat message is posted while an OPEN handoff exists for this agent that has NOT
// yet been submitted (its id is not the pinned pointer in identity.md), print a loud
// stderr warning steering to `identity --submit`. This is the exact interposition that
// stranded the prior Mayor. WARN ONLY — never block the send. Never throws.
export function warnIfUnsubmittedHandoff(agent: string): void {
  try {
    const open = detectUnsubmittedHandoff(pinnedHandoffId(agent), undefined, agent)
    if (!open) return
    process.stderr.write(
      `⚠️  parlay: open handoff ${open} for ${agent} is NOT yet submitted.\n` +
      `    You are posting chat inside the create→submit window — the exact gap that\n` +
      `    stranded a prior shutdown. Make it atomic: run \`identity --submit\` NOW\n` +
      `    (it auto-resolves ${open}), or \`identity --submit ${open}\` to be explicit.\n`,
    )
  } catch {
    /* a guard failure must never block a chat send */
  }
}
