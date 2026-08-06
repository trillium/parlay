// ── Prune policy: the pure predicate and its tunables ────────────────────────
//
// Everything here is data and one deterministic function, so the entire policy
// is unit-testable without a server, a clock, or a registry (see policy.test.ts,
// which drives it with the real 23-channel registry fixture).
//
// Design note on freshness data (load-bearing): `lastPollByChannel` is IN-MEMORY
// only and is empty right after a Pulse restart. A channel that is real and
// active but simply hasn't re-polled yet in this process lifetime has NO
// lastSeen. Therefore the STARTUP sweep MUST NOT prune by staleness — it prunes
// only by test-name pattern and known phantom ids, which are safe to remove
// regardless of freshness. The PERIODIC sweep, running in a long-lived process
// where lastPollByChannel has accumulated real activity, may additionally prune
// by idle-beyond-threshold. Both sweeps always honor the protected whitelist.

import { LISTEN_WINDOW_MS } from "../sse"

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
 *
 * "Regardless of freshness" is the whole point and it is deliberate: a leaked
 * fixture polls forever, so requiring it to look idle would mean never removing
 * it (robots-ycfa). Add a pattern here only when a REAL channel could not
 * plausibly carry it — a leaked one-off whose name is an ordinary word
 * ("design", "anchor", "shape") is left to explicit unregistration instead of
 * being guessed at, because a false positive here evicts a live agent.
 */
export const TEST_NAME_PATTERNS: readonly RegExp[] = [
  /^bench-/i,        // bench-probe-nonexistent-channel
  /^forge-/i,        // forge-probe-88764, forge-iso-a-90498, forge-agent-a, forge-b-a, forge-deploy-test …
  /^meas-/i,         // meas-a, meas-b, meas-c, meas-old
  /^test-/i,         // test-agent
  /-probe\b/i,       // bench-probe-…, forge-probe-…
  /-test$/i,         // forge-deploy-test
  /^nonexistent-/i,  // nonexistent-bench-channel-xyz
  // firstmate test-suite fixture families, observed leaked 2026-08-05 (robots-ycfa).
  /^profile-/i,      // profile-off-z1, profile-claude-z2, profile-batch-a-z9 … (fm-spawn-dispatch-profile)
  /^busy-/i,         // busy-pi-1, busy-cl-2, busy-pi-order (fm-busy-adapter-wiring)
  /^spawn-beads-/i,  // spawn-beads-absent-z2, spawn-beads-hooksect-z5 (fm-spawn-beads)
  /z\d+[a-z]?$/i,    // the -z<n> case-numbering suffix every fm spawn fixture uses:
                     // settle-single-stale-z1, account-off-z2, nobackendz3, orcaspawnz1, profile-grok-xhigh-z6b
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
 * Order matters. The static whitelist is checked FIRST, so a real channel is
 * never pruned no matter what else is true. The name-based rules come next,
 * AHEAD of the activity check, because activity cannot distinguish a live agent
 * from a leaked fixture: the fixture's own orphaned listener polls forever, so
 * every one of the 82 leaked channels in robots-ycfa looked permanently
 * "active" and was skipped by an hourly sweep that was working as designed.
 * For a name only a test harness produces, polling means leaking. The activity
 * check still guards every OTHER channel — the ones the idle rule would judge.
 */
export function shouldPrune(input: PruneDecisionInput): PruneDecision {
  const { id, lastSeenMs, nowMs, mode } = input

  // 1. Static whitelist — never prune, whatever else is true.
  if (PROTECTED_CHANNELS.has(id)) return { prune: false, reason: "protected: whitelisted" }

  // 2. Known phantom/duplicate id — prune in any mode, freshness irrelevant.
  if (PHANTOM_IDS.has(id)) return { prune: true, reason: "phantom: known duplicate id" }

  // 3. Test-name pattern — prune aggressively in any mode (startup included),
  //    freshness irrelevant. See the pattern list for why a live-looking poll
  //    is not a reprieve here.
  for (const re of TEST_NAME_PATTERNS) {
    if (re.test(id)) return { prune: true, reason: `test-pattern: matched ${re}` }
  }

  // 4. Dynamic protection — a channel seen within the listening window is live
  //    right now; keep it. When unsure, keep.
  if (lastSeenMs !== undefined && nowMs - lastSeenMs < LISTEN_WINDOW_MS) {
    return { prune: false, reason: "protected: active within listen window" }
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
