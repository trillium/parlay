// Fold §3.6 + §6 Slice 3 — parlay crew-state reader.
//
// Reconciles an agent's last keyed status line against parlay's oracle:
// worktree/session gone, run attribution, herdr tab liveness, subscriber presence.
// Outputs: <state> · source: <src> · <detail>
// States: working | done | blocked | needs-decision | paused | failed | resolved | captain-held | unknown
//
// crew-state is the supervision oracle: a supervisor polling it decides
// whether an agent is alive, so a false "dead" is the expensive failure, and
// a transient answer that masks a real terminal state is nearly as bad.
// robots-me7m: never report "not enrolled" from a lookup that merely FAILED,
// and never report "unknown" when a stale-but-valid status line is on disk.

import { existsSync, readFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { serverUrl, EXIT_USAGE } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"
import type { SubscribersInfo } from "./types"
import { statusSink } from "./commands-status"

// Fetch that reports failure instead of exiting (non-fatal).
async function tryJSON<T>(path: string): Promise<{ ok: true; data: T } | { ok: false; err: string }> {
  try {
    const res = await fetch(`${serverUrl()}${path}`, { signal: AbortSignal.timeout(3_000) })
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

// Read the last status line from the status file, distinguishing the three
// conditions the caller must be able to tell apart (robots-me7m): nothing
// recorded yet, a file that exists but cannot be read/parsed, and a valid
// line.
type StatusRead =
  | { kind: "ok"; status: { verb: string; key?: string; note: string } }
  | { kind: "absent" }
  | { kind: "unreadable" | "unparseable"; detail: string }

function readStatusFor(statusFile: string): StatusRead {
  if (!existsSync(statusFile)) return { kind: "absent" }
  let content: string
  try {
    content = readFileSync(statusFile, "utf-8")
  } catch (err) {
    return { kind: "unreadable", detail: `status file unreadable: ${err}` }
  }
  const lines = content.split("\n").filter((l) => l.trim())
  if (lines.length === 0) return { kind: "absent" }
  const last = lines[lines.length - 1]
  const parsed = parseStatusLine(last)
  if (!parsed) return { kind: "unparseable", detail: `status line unparseable: ${last.trim()}` }
  return { kind: "ok", status: parsed }
}

// The relay's answer about an agent's registry presence. The third case is
// the point of the type: "the relay did not answer" is NOT "the relay says
// no" (robots-me7m).
type Enrollment = "yes" | "no" | "unknown"

// Bound the retry on a failed subscribers read. The observed failure was
// load-dependent (43 concurrent `parlay listen`/`parlay monitor` processes)
// and cleared on an immediate retry, so a couple of cheap retries absorb the
// contention before crew-state has to report a degraded answer at all.
const RELAY_LOOKUP_ATTEMPTS = 3
const RELAY_LOOKUP_BACKOFF_MS = 250

// Ask the relay whether agentId is in the agent registry, distinguishing
// "not registered" from "could not ask".
//
// robots-me7m: this used to be isAgentSubscribed(): Promise<boolean>,
// collapsing a failed lookup (timeout, non-2xx, undecodable body) into "not
// subscribed" — so a transient relay hiccup made a live, registered agent
// with a valid status file report as "unknown · source: none · agent not
// enrolled with relay", which reads as dead to any supervisor polling this
// command.
async function agentEnrollment(agentId: string): Promise<Enrollment> {
  for (let attempt = 0; attempt < RELAY_LOOKUP_ATTEMPTS; attempt++) {
    if (attempt > 0) await new Promise((r) => setTimeout(r, RELAY_LOOKUP_BACKOFF_MS))
    const subs = await tryJSON<SubscribersInfo>(`/api/chat/subscribers`)
    if (!subs.ok) continue
    // A 2xx with no registered block is a real answer ("nobody is
    // registered"), not a failed lookup.
    const agents = subs.data.registered?.agents ?? []
    return agents.some((a) => a.id === agentId) ? "yes" : "no"
  }
  return "unknown"
}

// Exit codes crew-state adds on top of the CLI-wide 0/1/2, so a supervisor
// can tell "no news" from "gone" from "I couldn't ask" WITHOUT string-
// matching the detail text (robots-me7m). Mirrored exactly in
// tools/cli/internal/commands/crew_state.go.
export const EXIT_CREW_NO_STATUS = 3
export const EXIT_CREW_NOT_ENROLLED = 4
export const EXIT_CREW_STATUS_UNREADABLE = 5
export const EXIT_CREW_RELAY_UNREACHABLE = 6

// Determine crew state by reconciling status + oracle signals.
//
// Precedence (fold §4, section 3.6), per robots-me7m's fix direction:
// 1. The relay is asked first, but its answer only decides "gone" when it
//    actually answered. A failed lookup NEVER produces "not enrolled".
// 2. The on-disk status file is the durable record and is always consulted:
//    a stale-but-valid status beats "unknown" in every case, because a
//    supervisor acting on stale-working is safe while one acting on
//    false-dead is not.
// 3. "nothing recorded", "unreadable/unparseable", and "not registered with
//    the relay" are three distinct conditions with distinct details and
//    distinct exit codes — only the last means the agent is unreachable.
export async function crewStateForAgent(
  agentId: string
): Promise<{ state: string; source: string; detail: string; exitCode: number }> {
  // Resolve status file (same logic as parlay status).
  const { file: statusFile } = statusSink()

  const enrolled = await agentEnrollment(agentId)
  const read = readStatusFor(statusFile)

  // Source suffix records HOW much to trust the status line: plain "status"
  // when the relay confirmed enrollment, qualified when the relay disagreed
  // or couldn't be reached.
  let source = "status"
  let exitCode = 0
  let suffix = ""
  if (enrolled === "no") {
    source = "status-unenrolled"
    exitCode = EXIT_CREW_NOT_ENROLLED
    suffix = " (relay does not list this agent)"
  } else if (enrolled === "unknown") {
    source = "status-degraded"
    suffix = " (relay unreachable; status may be stale)"
  }

  if (read.kind === "ok") {
    const { verb, note } = read.status
    if (!ALL_VERBS.has(verb)) {
      return {
        state: "unknown",
        source,
        detail: `unrecognized verb: ${verb}`,
        exitCode: EXIT_CREW_STATUS_UNREADABLE,
      }
    }
    // A valid status line always wins the state, even when the relay says
    // the agent is gone or could not be reached — never return "unknown"
    // over a usable record.
    return { state: verb, source, detail: (note || "(no detail)") + suffix, exitCode }
  }

  if (read.kind === "unreadable" || read.kind === "unparseable") {
    return { state: "unknown", source, detail: read.detail + suffix, exitCode: EXIT_CREW_STATUS_UNREADABLE }
  }

  // No status on disk: the relay's answer is all there is.
  if (enrolled === "no") {
    return {
      state: "unknown",
      source: "none",
      detail: "agent not registered with relay",
      exitCode: EXIT_CREW_NOT_ENROLLED,
    }
  }
  if (enrolled === "unknown") {
    return {
      state: "unknown",
      source: "none",
      detail: "relay unreachable and no status recorded",
      exitCode: EXIT_CREW_RELAY_UNREACHABLE,
    }
  }
  return { state: "unknown", source: "none", detail: "no status recorded", exitCode: EXIT_CREW_NO_STATUS }
}

export async function cmdCrewState(args: string[]) {
  if (helpWanted("crew-state", args)) return
  const { positionals } = parseArgs("crew-state", args)
  const agentId = positionals[0]?.trim()
  if (!agentId) return die("parlay crew-state: agent id required", EXIT_USAGE)

  const { state, source, detail, exitCode } = await crewStateForAgent(agentId)
  console.log(`${state} · source: ${source} · ${detail}`)
  if (exitCode !== 0) process.exit(exitCode)
}
