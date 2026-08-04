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
// The bd/handoff store emits `created_at` (ISO8601); `created` is kept as a legacy
// alias so older stores and hand-built test rows still resolve. Reading only `created`
// was robots-qkr: the real field is `created_at`, so age was ALWAYS unknown and every
// inherited handoff misfired the aggressive create→submit nag.
type HandoffRow = {
  id?: unknown
  status?: unknown
  assignee?: unknown
  created_at?: unknown
  created?: unknown
}

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
// callers can inspect `created` for age-based inherited detection).
//
// When the agent is KNOWN, the agent-scoped query is AUTHORITATIVE: its answer —
// including "no open handoff for this agent" — is final, and we MUST NOT fall through
// to the store-global newest-open handoff. That fall-through was the fleet-wide
// misattribution bug (robots-4x9f, root-cause cluster robots-6wb/0sv/bu8/51s/vi7): a
// fresh/resumed agent has zero open handoffs of its OWN, so the store-global fallback
// grabbed some OTHER agent's newest open handoff (136 open store-wide) and pinned the
// create→submit / say-guard nag on whoever was posting — every fresh agent then
// narrated "stale/inherited handoff unrelated to my role — dismiss it". Handoffs set
// assignee=<agent-id> (owner stays the principal), so the agent-scoped list is
// reliable; its emptiness genuinely means "nothing unsubmitted for this agent".
// Query preference:
//   1. agent known    → list --assignee <agent> --status open,…  (AUTHORITATIVE)
//   2. agent UNKNOWN   → list --status open,…                    → newest open in store
//   3. agent UNKNOWN   → show --current                          → store's "current"
// Steps 2/3 run ONLY when there is no agent identity to scope by (a bare CLI call with
// no PARLAY_AGENT_ID) — so there is no one to misattribute the result to.
function resolveRow(store: string, agent: string): HandoffRow | undefined {
  // 1. Agent-scoped newest open handoff — the precise, AUTHORITATIVE "for this agent"
  //    answer. When agent is known, we stop here whether or not it found a row.
  if (agent) {
    const rows = runStore(store, [
      "list", "--status", OPEN_STATUSES, "--assignee", agent, "--sort", "updated", "-r", "--json",
    ])
    if (rows && rows.length > 0 && rows[0].id) return rows[0]
    return undefined
  }

  // 2. Agent UNKNOWN: newest open handoff in the store (no identity to scope by).
  const anyRows = runStore(store, [
    "list", "--status", OPEN_STATUSES, "--sort", "updated", "-r", "--json",
  ])
  if (anyRows && anyRows.length > 0 && anyRows[0].id) return anyRows[0]

  // 3. Agent UNKNOWN last resort: the store's notion of "current"
  //    (in-progress/hooked/last-touched). Kept for stores/versions without a working
  //    `list --status`; a closed row here is filtered out.
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
  // Prefer the store's real `created_at`; fall back to the legacy `created` alias.
  const createdMs = parseCreatedMs(row.created_at ?? row.created)
  if (sessionStartedAt !== undefined && createdMs !== undefined) {
    return createdMs < sessionStartedAt
  }
  if (createdMs !== undefined) {
    return Date.now() - createdMs > INHERITED_AGE_MS
  }
  // Unknown age → treat as inherited (robots-qkr). The two warnings are asymmetric:
  // the "current-session" branch urges `identity --submit`, which RESETS context and
  // (on a handoff this agent did not create) corrupts its identity pointer; the
  // "inherited" branch only points to the non-destructive `--dismiss-handoff`. When we
  // cannot prove the handoff belongs to THIS session, the safe default is the gentle,
  // reversible one — never push a destructive reset on an unprovable handoff.
  return true
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
