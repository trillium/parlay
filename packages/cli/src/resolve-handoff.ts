// Auto-resolve the agent's current open handoff bead id.
//
// The create→submit death window: a persistent agent's clean shutdown is two acts —
// (1) `handoff create` mints a handoff bead, (2) `identity --submit <id>` pins the
// pointer AND reincarnates. If anything is interposed between them (a courtesy
// `parlay say`, a context-limit kill), step 2 never runs and the shutdown is stranded
// with a live bead but no pin/restart. Requiring the id to be threaded by hand through
// step 2 is exactly what leaves room for the interposition.
//
// Fix: make the id OPTIONAL. When `--submit`/`--handoff` is given no id, resolve the
// agent's NEWEST OPEN handoff from the store. So the skill can run
// `handoff create … && identity --submit` as ONE atomic act with nothing between, and
// a create that DID get stranded is recovered by a bare `identity --submit`.

import { spawnSync } from "child_process"

// The store CLI name is the bead-id prefix ("handoff-1bk" → `handoff`). Kept a
// parameter (not hardcoded) so a differently-named handoff store still resolves.
export const DEFAULT_HANDOFF_STORE = "handoff"

// Open statuses a live handoff can be in. A "current" handoff that has already been
// closed must never be treated as the pending shutdown target.
const OPEN_STATUSES = "open,in_progress,blocked"

// One handoff row as returned by the store's --json output. Only the fields we read.
type HandoffRow = { id?: unknown; status?: unknown; assignee?: unknown; created?: unknown }

function runStore(store: string, args: string[]): HandoffRow[] | undefined {
  // Pass env explicitly so command resolution honors the live process.env.PATH
  // (bun's spawnSync otherwise resolves against a cached PATH).
  const r = spawnSync(store, args, { encoding: "utf8", env: process.env })
  if (r.error || r.status !== 0 || !r.stdout) return undefined
  try {
    const parsed = JSON.parse(r.stdout)
    return Array.isArray(parsed) ? (parsed as HandoffRow[]) : [parsed as HandoffRow]
  } catch {
    return undefined
  }
}

function firstId(rows: HandoffRow[] | undefined): string | undefined {
  if (!rows || rows.length === 0) return undefined
  const id = (rows[0]?.id ?? "").toString().trim()
  return id || undefined
}

// Internal: resolve the full HandoffRow for the newest open bead (returns the row so
// callers can inspect `created` for age-based inherited detection). Query preference:
//   1. list --assignee <agent> --status open,…  → newest open FOR this agent
//   2. list --status open,…                     → newest open in the store
//   3. show --current                           → store's "current" (may be closed)
function resolveRow(store: string, agent: string): HandoffRow | undefined {
  // 1. Agent-scoped newest open handoff — the precise "for this agent" answer.
  if (agent) {
    const rows = runStore(store, [
      "list", "--status", OPEN_STATUSES, "--assignee", agent, "--sort", "updated", "-r", "--json",
    ])
    if (rows && rows.length > 0 && rows[0].id) return rows[0]
  }

  // 2. Newest open handoff in the store (assignee unknown / not set on the bead).
  const anyRows = runStore(store, [
    "list", "--status", OPEN_STATUSES, "--sort", "updated", "-r", "--json",
  ])
  if (anyRows && anyRows.length > 0 && anyRows[0].id) return anyRows[0]

  // 3. Last resort: the store's notion of "current" (in-progress/hooked/last-touched).
  //    Kept for stores/versions without a working `list --status` and as a bare-metal
  //    fallback; a closed row here is filtered out.
  const current = runStore(store, ["show", "--current", "--json"])
  if (current && current.length > 0) {
    const status = (current[0]?.status ?? "").toString().trim().toLowerCase()
    if (status !== "closed") return current[0]
  }
  return undefined
}

// Resolve the newest OPEN handoff bead id for `agent` (or the store's current, if no
// agent is known). Returns undefined when the store is unavailable or reports nothing
// open — callers then demand an explicit id rather than guessing. Never throws.
export function resolveCurrentHandoff(
  store: string = DEFAULT_HANDOFF_STORE,
  agent?: string,
): string | undefined {
  const ag = (agent ?? process.env.PARLAY_AGENT_ID ?? "").trim()
  return firstId([resolveRow(store, ag) ?? {}])
}

// Age threshold for the "inherited handoff" fallback when no session-start file is
// available. A handoff older than this was almost certainly created in a prior session.
const INHERITED_AGE_MS = 24 * 60 * 60 * 1000  // 24 hours

function parseCreatedMs(created: unknown): number | undefined {
  if (!created) return undefined
  const s = created.toString().trim()
  if (!s) return undefined
  const ms = Date.parse(s)
  return isNaN(ms) ? undefined : ms
}

// True when the handoff row predates the current agent session.
// Primary signal: `sessionStartedAt` (epoch ms, from ~/.parlay/agents/<id>/session-start
// written by parlay-spawn on every new spawn). Fallback: row.created older than 24h.
function isInherited(row: HandoffRow, sessionStartedAt?: number): boolean {
  const createdMs = parseCreatedMs(row.created)
  if (sessionStartedAt !== undefined && createdMs !== undefined) {
    return createdMs < sessionStartedAt
  }
  if (createdMs !== undefined) {
    return Date.now() - createdMs > INHERITED_AGE_MS
  }
  return false  // unknown age → assume current-session (conservative)
}

// Detect a handoff that was created but NOT yet submitted — the exact hazard state the
// atomic create→submit contract guards against. "Not yet submitted" is read off the
// agent's identity.md: `identity --submit` pins a `> 📎 Handoff: <id>` pointer, so an
// OPEN handoff for this agent whose id is NOT the pinned pointer is an in-flight,
// unsubmitted shutdown. Posting chat in that window is what stranded the prior Mayor.
//
// Returns { id, inherited } or undefined when nothing open / already pinned. Never throws.
//   `pinnedPointer`    — the id currently pinned in identity.md (undefined if none).
//   `sessionStartedAt` — epoch ms when this agent session started; from parlay-spawn's
//                        session-start sentinel file. When absent, a 24h age threshold
//                        is used to distinguish inherited (prior-session) handoffs.
//   `inherited: true`  — the handoff predates this session; the agent did NOT create it
//                        and should NOT run `identity --submit` (that would reset a fresh
//                        context). Use `identity --dismiss-handoff` to silence the nag.
export function detectUnsubmittedHandoff(
  pinnedPointer: string | undefined,
  store: string = DEFAULT_HANDOFF_STORE,
  agent?: string,
  sessionStartedAt?: number,
): { id: string; inherited: boolean } | undefined {
  const ag = (agent ?? process.env.PARLAY_AGENT_ID ?? "").trim()
  const row = resolveRow(store, ag)
  if (!row) return undefined
  const id = firstId([row])
  if (!id) return undefined
  if (pinnedPointer && id === pinnedPointer.trim()) return undefined
  return { id, inherited: isInherited(row, sessionStartedAt) }
}
