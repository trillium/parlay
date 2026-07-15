// ── Autonomous Parlay channel cleanup ────────────────────────────────────────
//
// Parlay's agent registry (`agents` in sse.ts, persisted to parlay-agents.json)
// grows every time a channel first polls or registers — including throwaway
// test/bench/probe channels that never deregister. Left alone it accretes stale
// tabs the captain has to prune by hand. This module makes cleanup autonomous:
//
//   • unregisterAgent(id)     — remove one channel, fail-LOUD on unknown id
//   • pruneChannels(mode)     — one sweep; removes channels shouldPrune() flags
//   • startPruneSweeps()      — startup sweep + hourly interval, wired in startChat()
//
// The proper long-term fix is DEREGISTRATION ON EXIT: whatever spawns a channel
// (firstmate's crewmate spawn/teardown, a test harness, a probe script) should
// POST /api/chat/unregister { id } when it finishes, exactly as a task tears
// down its worktree. This sweep is the belt-and-suspenders for when that is
// missed — not a replacement for it. See the brain doc referenced in the report.
//
// Design note on freshness data (load-bearing): `lastPollByChannel` is IN-MEMORY
// only and is empty right after a Pulse restart. A channel that is real and
// active but simply hasn't re-polled yet in this process lifetime has NO
// lastSeen. Therefore the STARTUP sweep MUST NOT prune by staleness — it prunes
// only by test-name pattern and known phantom ids, which are safe to remove
// regardless of freshness. The PERIODIC sweep, running in a long-lived process
// where lastPollByChannel has accumulated real activity, may additionally prune
// by idle-beyond-threshold. Both sweeps always honor the protected whitelist.

import { agents, lastPollByChannel, LISTEN_WINDOW_MS, broadcastToClients, persistAgents } from "./sse"

// ── Configuration (clearly tunable) ──────────────────────────────────────────

/**
 * Channels that must NEVER be pruned, whatever their age or name. These are the
 * real, persistent Parlay channels. A channel active within the idle window is
 * ALSO protected dynamically (see shouldPrune) — this list is the static core.
 */
export const PROTECTED_CHANNELS: ReadonlySet<string> = new Set([
  "main-agent",   // the primary firstmate channel
  "lavish",       // Lavish review-artifact channel
  "muse",         // Muse channel
  "parlay-panel", // the proxied chat panel channel
  "resume",       // resume-system channel
  "deckhand",     // deckhand channel
  "parlay-dev",   // Parlay development channel
])

/**
 * Idle threshold: a channel last seen longer ago than this is prune-eligible in
 * the PERIODIC sweep (never the startup sweep, which has no reliable freshness
 * data). 7 days — long enough that a channel used even weekly survives.
 */
export const IDLE_PRUNE_MS = 7 * 24 * 60 * 60 * 1000 // 7 days

/**
 * Test/throwaway channel-name patterns pruned AGGRESSIVELY — in every sweep,
 * including startup, regardless of freshness. These names only ever come from
 * benchmarks, probes, and test harnesses; a real channel is never named this.
 */
export const TEST_NAME_PATTERNS: readonly RegExp[] = [
  /^bench-/i,        // bench-probe-nonexistent-channel
  /^forge-/i,        // forge-probe-88764, forge-iso-a-90498, forge-agent-a, forge-b-a, forge-deploy-test …
  /^meas-/i,         // meas-a, meas-b, meas-c, meas-old
  /^test-/i,         // test-agent
  /-probe\b/i,       // bench-probe-…, forge-probe-…
  /-test$/i,         // forge-deploy-test
  /^nonexistent-/i,  // nonexistent-bench-channel-xyz
]

/**
 * Known phantom / duplicate ids to remove by EXACT match. `firstmate` is a
 * stray duplicate of the real `main-agent` channel; it matches no test pattern,
 * so it is named here explicitly. Extend this set if other one-off phantoms
 * appear that no pattern would catch.
 */
export const PHANTOM_IDS: ReadonlySet<string> = new Set([
  "firstmate", // phantom duplicate of main-agent
])

// ── Pure prune predicate (unit-tested against the real 23-channel fixture) ────

export type PruneMode = "startup" | "periodic"

export interface PruneDecisionInput {
  id: string
  /** epoch ms this channel was last seen polling, or undefined if never/unknown */
  lastSeenMs?: number
  /** epoch ms "now" — injected so the predicate is deterministic in tests */
  nowMs: number
  mode: PruneMode
}

export interface PruneDecision {
  prune: boolean
  reason: string
}

/**
 * Decide whether a single channel should be pruned. Pure and deterministic:
 * every input is passed in, nothing is read from module state. This is the one
 * place that encodes the whole policy, so it is the one place the tests target.
 *
 * Order matters — protection is checked FIRST so a protected or freshly-active
 * channel is never pruned no matter what its name looks like.
 */
export function shouldPrune(input: PruneDecisionInput): PruneDecision {
  const { id, lastSeenMs, nowMs, mode } = input

  // 1. Static whitelist — never prune, whatever else is true.
  if (PROTECTED_CHANNELS.has(id)) return { prune: false, reason: "protected: whitelisted" }

  // 2. Dynamic protection — a channel seen within the listening window is live
  //    right now; keep it even if its name looks test-y. When unsure, keep.
  if (lastSeenMs !== undefined && nowMs - lastSeenMs < LISTEN_WINDOW_MS) {
    return { prune: false, reason: "protected: active within listen window" }
  }

  // 3. Known phantom/duplicate id — prune in any mode.
  if (PHANTOM_IDS.has(id)) return { prune: true, reason: "phantom: known duplicate id" }

  // 4. Test-name pattern — prune aggressively in any mode (startup included).
  for (const re of TEST_NAME_PATTERNS) {
    if (re.test(id)) return { prune: true, reason: `test-pattern: matched ${re}` }
  }

  // 5. Idle-beyond-threshold — PERIODIC sweep only. The startup sweep has no
  //    reliable freshness data (lastPollByChannel is empty after restart), so a
  //    never-seen channel at startup is KEPT — conservative by design.
  if (mode === "periodic") {
    if (lastSeenMs !== undefined && nowMs - lastSeenMs > IDLE_PRUNE_MS) {
      const days = Math.floor((nowMs - lastSeenMs) / (24 * 60 * 60 * 1000))
      return { prune: true, reason: `idle: last seen ${days}d ago (> ${IDLE_PRUNE_MS / (24 * 60 * 60 * 1000)}d)` }
    }
  }

  return { prune: false, reason: "kept: no prune rule matched" }
}

// ── Registry mutation ─────────────────────────────────────────────────────────

export interface UnregisterResult {
  ok: boolean
  id: string
  error?: string
}

/**
 * Remove one channel from the registry. Fail LOUD on an unknown id — the caller
 * gets ok:false with an explicit error and (via the endpoint) a non-2xx status.
 * A silent success on a bad id hides bugs (see robots-5l8); never do that.
 *
 * On success: drop the in-memory record, persist the registry to disk, and
 * broadcast an `agent_unregister` SSE event so open tabs remove the channel
 * without a manual reload.
 */
export function unregisterAgent(id: string): UnregisterResult {
  const clean = String(id ?? "").trim()
  if (!clean) return { ok: false, id: clean, error: "id required" }
  if (!agents.has(clean)) return { ok: false, id: clean, error: `unknown channel: ${clean}` }
  agents.delete(clean)
  // Drop freshness tracking too so a re-registered id starts clean and a
  // just-pruned id can't linger in the presence map.
  lastPollByChannel.delete(clean)
  persistAgents()
  broadcastToClients("agent_unregister", { id: clean })
  return { ok: true, id: clean }
}

// ── Sweep ─────────────────────────────────────────────────────────────────────

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
 * see the freshness note at the top of this file) plus an hourly periodic sweep
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
