// ── Registry mutation and the sweeps that drive it ───────────────────────────
//
// `shouldPrune` (./policy) decides; this file acts on the decision and owns the
// only two callers: the startup sweep and the hourly periodic sweep.

import { agents, lastPollByChannel, broadcastToClients, persistAgents, resolvePollWaiters } from "../sse"
import { shouldPrune, type PruneMode } from "./policy"
import { tombstone } from "./tombstones"
import { history } from "../storage"

export interface UnregisterResult {
  ok: boolean
  id: string
  error?: string
  /** User messages addressed to this channel that were never polled/received.
   *  Reported, not flushed or redelivered — there is no other listener to hand
   *  them to, and silently discarding chat history is a separate, unrequested
   *  destructive action. Present only on a successful removal. */
  undelivered?: number
}

/**
 * Remove one channel from the registry. Fail LOUD on an unknown id — the caller
 * gets ok:false with an explicit error and (via the endpoint) a non-2xx status.
 * A silent success on a bad id hides bugs (see robots-5l8); never do that.
 *
 * On success: drop the in-memory record, TOMBSTONE the id, persist the registry
 * to disk, and broadcast an `agent_unregister` SSE event so open tabs remove the
 * channel without a manual reload.
 *
 * The tombstone is what makes removal mean anything: it stops the id from being
 * silently re-registered (POST /api/chat/register-agent, or an auto-register on
 * first /reply) before the TTL, so both the sweep AND a hand-run
 * `POST /api/chat/unregister` stick. Poll itself no longer registers anything
 * (task-1t0m), but the historical incident (robots-ycfa) it fixed — a leaked
 * listener's own poll resurrecting a just-pruned row within seconds — is why
 * this exists at all.
 */
export function unregisterAgent(id: string): UnregisterResult {
  const clean = String(id ?? "").trim()
  if (!clean) return { ok: false, id: clean, error: "id required" }
  if (!agents.has(clean)) return { ok: false, id: clean, error: `unknown channel: ${clean}` }
  // Count before deletion — nothing here reads agents/history after removal.
  const undelivered = history.filter(m =>
    m.channel === clean && m.role === "user" && m.received !== true
  ).length
  agents.delete(clean)
  tombstone(clean)
  // Drop freshness tracking too so a re-registered id starts clean and a
  // just-pruned id can't linger in the presence map.
  lastPollByChannel.delete(clean)
  // Wake any in-flight long-poll on this channel now, rather than letting it
  // sit until its own timeout and only discover the channel is gone on its
  // NEXT request (up to 30s of a relay polling a channel that is already
  // retired).
  resolvePollWaiters(clean, { gone: true })
  persistAgents()
  broadcastToClients("agent_unregister", { id: clean })
  return { ok: true, id: clean, undelivered }
}

export interface PruneSweepResult {
  mode: PruneMode
  pruned: { id: string; reason: string }[]
  kept: number
}

/**
 * Run one prune sweep over the live registry. Evaluates every channel through
 * shouldPrune(), removes the flagged ones via unregisterAgent(), and logs each
 * removal (what + why). Returns a structured result for callers/tests.
 */
export function pruneChannels(mode: PruneMode, nowMs: number = Date.now()): PruneSweepResult {
  const pruned: { id: string; reason: string }[] = []
  let kept = 0
  // Snapshot ids first — unregisterAgent mutates the map we'd otherwise iterate.
  for (const id of [...agents.keys()]) {
    const decision = shouldPrune({ id, lastSeenMs: lastPollByChannel.get(id), nowMs, mode })
    if (!decision.prune) { kept++; continue }
    const res = unregisterAgent(id)
    if (res.ok) {
      pruned.push({ id, reason: decision.reason })
      console.log(`[parlay-prune] removed channel "${id}" — ${decision.reason}`)
    } else {
      // Should not happen (id came from the live map), but never swallow it.
      console.warn(`[parlay-prune] failed to remove "${id}": ${res.error}`)
    }
  }
  if (pruned.length) {
    console.log(`[parlay-prune] ${mode} sweep removed ${pruned.length}, kept ${kept}`)
  }
  return { mode, pruned, kept }
}

/**
 * Periodic sweep cadence — hourly. Frequent enough to keep the list clean,
 * infrequent enough to be invisible.
 */
export const PERIODIC_SWEEP_MS = 60 * 60 * 1000 // 1 hour

let periodicTimer: ReturnType<typeof setInterval> | null = null

/**
 * Wire autonomous cleanup: one immediate startup sweep (name/phantom only —
 * see the freshness note in ./policy) plus an hourly periodic sweep
 * (name/phantom + idle). Idempotent: a second call does not stack intervals.
 */
export function startPruneSweeps(): void {
  pruneChannels("startup")
  if (periodicTimer) return
  periodicTimer = setInterval(() => {
    try { pruneChannels("periodic") } catch (err) { console.warn("[parlay-prune] periodic sweep error:", err) }
  }, PERIODIC_SWEEP_MS)
  // Don't let the sweep interval keep the process alive on its own.
  if (typeof periodicTimer.unref === "function") periodicTimer.unref()
}
