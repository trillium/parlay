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
type HandoffRow = { id?: unknown; status?: unknown; assignee?: unknown }

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

// Resolve the newest OPEN handoff bead for `agent` (or the store's current, if no agent
// is known). Query preference order — each step is the more precise answer, and we fall
// back only when the store can't satisfy it:
//   1. list --status open,in_progress,blocked --assignee <agent> --sort updated -r  → newest open FOR this agent
//   2. list --status open,in_progress,blocked --sort updated -r                     → newest open in the store
//   3. show --current                                                                → store's "current" (may be closed)
// Returns undefined when the store is unavailable or reports nothing open — callers then
// demand an explicit id rather than guessing. Never throws.
export function resolveCurrentHandoff(
  store: string = DEFAULT_HANDOFF_STORE,
  agent?: string,
): string | undefined {
  const ag = (agent ?? process.env.PARLAY_AGENT_ID ?? "").trim()

  // 1. Agent-scoped newest open handoff — the precise "for this agent" answer.
  if (ag) {
    const scoped = firstId(runStore(store, [
      "list", "--status", OPEN_STATUSES, "--assignee", ag, "--sort", "updated", "-r", "--json",
    ]))
    if (scoped) return scoped
  }

  // 2. Newest open handoff in the store (assignee unknown / not set on the bead).
  const anyOpen = firstId(runStore(store, [
    "list", "--status", OPEN_STATUSES, "--sort", "updated", "-r", "--json",
  ]))
  if (anyOpen) return anyOpen

  // 3. Last resort: the store's notion of "current" (in-progress/hooked/last-touched).
  //    Kept for stores/versions without a working `list --status` and as a bare-metal
  //    fallback; a closed row here is filtered out.
  const current = runStore(store, ["show", "--current", "--json"])
  if (current && current.length > 0) {
    const status = (current[0]?.status ?? "").toString().trim().toLowerCase()
    if (status !== "closed") return firstId(current)
  }
  return undefined
}

// Detect a handoff that was created but NOT yet submitted — the exact hazard state the
// atomic create→submit contract guards against. "Not yet submitted" is read off the
// agent's identity.md: `identity --submit` pins a `> 📎 Handoff: <id>` pointer, so an
// OPEN handoff for this agent whose id is NOT the pinned pointer is an in-flight,
// unsubmitted shutdown. Posting chat in that window is what stranded the prior Mayor.
//
// Returns the unsubmitted handoff id, or undefined when there is none (nothing open, or
// the newest open handoff is already the pinned pointer). Never throws. `pinnedPointer`
// is the id currently pinned in identity.md (undefined if none pinned).
export function detectUnsubmittedHandoff(
  pinnedPointer: string | undefined,
  store: string = DEFAULT_HANDOFF_STORE,
  agent?: string,
): string | undefined {
  const open = resolveCurrentHandoff(store, agent)
  if (!open) return undefined
  if (pinnedPointer && open === pinnedPointer.trim()) return undefined
  return open
}
