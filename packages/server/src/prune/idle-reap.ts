// ── Idle reaper for Parlay-launched agents (task-4dz9) ───────────────────────
//
// The gap this closes: firstmate-spawned agents get a durable lifecycle record
// and a 2h idle auto-close. Parlay-spawned agents (`parlay spawn`, ticket
// auto-claim) got neither — registered, then left resident forever once their
// listener stopped polling, because nothing had authority to close them. See
// discussion #232 and docs/agent-notes/idle-reap-parlay-launched-task-4dz9.md.
//
// Scope is deliberately narrow, mirroring the channel-prune module next door:
//
//   • shouldIdleReap(input)  — the pure policy predicate, unit-tested directly
//   • idleReapThresholdMs()  — env-configurable idle window (default 2h)
//   • idleReapSweep()        — one pass; reaps every agent shouldIdleReap flags
//
// Teardown primitive: unregisterAgent (./sweep.ts) — the exact function
// POST /api/chat/unregister calls, i.e. what `parlay shutdown <id>` invokes
// server-side (task-35ww/#234). This module does not kill the agent's local
// listener PROCESS the way the CLI verb's first step does (no `ps` access from
// here) — but unregisterAgent's resolvePollWaiters immediately resolves the
// listener's parked long-poll with {gone:true}, which both `parlay monitor`'s
// poll loop and the relay's poll loop already treat as a terminal signal to
// stop (see tools/cli/internal/monitor/monitor.go and tools/relay/relay_poll.go)
// — so the listener process ends on its own within one poll cycle, not a
// second teardown path.
//
// Liveness signal: lastPollByChannel, the SAME in-memory freshness map the
// channel-prune sweep already reads (./policy.ts) — no new tracking was added.
// A channel only stays "seen" while its listener is alive and actually
// long-polling, so an agent parked on a genuine human-facing wait (paused,
// needs-decision, or simply mid-task) keeps its listener running and its
// lastSeen fresh, and is therefore never idle by this definition — see the
// open question in discussion #232 this answers, and the PR body for the
// alternative considered (a dedicated "waiting on human" status signal, which
// does not exist anywhere the server can see and was deliberately not
// invented here).

import { agents, lastPollByChannel } from "../sse"
import { unregisterAgent, PERIODIC_SWEEP_MS } from "./sweep"

/** Default idle window: 2h, mirroring firstmate's own staleness auto-close. */
export const DEFAULT_IDLE_REAP_MS = 2 * 60 * 60 * 1000

/**
 * Idle threshold, overridable via PARLAY_AGENT_IDLE_TIMEOUT_MS (milliseconds,
 * matching this file's own *_MS constants and IDLE_PRUNE_MS/LISTEN_WINDOW_MS
 * in ./policy.ts and ../sse.ts). Missing, non-numeric, or non-positive values
 * fall back to the default — never a shorter-than-intended window from a typo.
 */
export function idleReapThresholdMs(): number {
  const raw = process.env.PARLAY_AGENT_IDLE_TIMEOUT_MS
  if (raw === undefined || raw.trim() === "") return DEFAULT_IDLE_REAP_MS
  const n = Number(raw)
  return Number.isFinite(n) && n > 0 ? n : DEFAULT_IDLE_REAP_MS
}

export interface IdleReapDecisionInput {
  id: string
  /** AgentInfo.launchedBy — only "parlay"-prefixed launchers are ours to reap. */
  launchedBy?: string
  /** epoch ms this channel was last seen polling, or undefined if never/unknown. */
  lastSeenMs?: number
  /** epoch ms "now" — injected so the predicate is deterministic in tests. */
  nowMs: number
  thresholdMs: number
}

export interface IdleReapDecision {
  reap: boolean
  reason: string
}

/**
 * Decide whether ONE agent has been idle long enough to reclaim its pane.
 * Pure and total, mirroring shouldPrune (../prune/policy.ts) — every guard
 * fails toward "leave it alone", because a wrongly-reaped agent destroys a
 * live session while a wrongly-kept one just waits one more sweep.
 */
export function shouldIdleReap(input: IdleReapDecisionInput): IdleReapDecision {
  const { launchedBy, lastSeenMs, nowMs, thresholdMs } = input

  // 1. Not Parlay-launched — untouched. This is what keeps firstmate-spawned
  //    (and any hand-registered) agents out of scope entirely: they carry no
  //    launchedBy, by construction, so this reaper never sees them as
  //    candidates regardless of how idle they look.
  if (!launchedBy || !launchedBy.startsWith("parlay")) {
    return { reap: false, reason: "not a Parlay-launched agent" }
  }

  // 2. No liveness data at all — conservative by construction, the same rule
  //    the startup channel-prune sweep uses (lastPollByChannel is empty right
  //    after a server restart, so "never seen" must never mean "idle").
  if (lastSeenMs === undefined) {
    return { reap: false, reason: "no liveness data yet — never polled or not seen since restart" }
  }

  const idleMs = nowMs - lastSeenMs
  if (idleMs < thresholdMs) {
    return { reap: false, reason: "active within idle threshold" }
  }

  const idleMin = Math.round(idleMs / 60_000)
  const thresholdMin = Math.round(thresholdMs / 60_000)
  return { reap: true, reason: `idle: last seen ${idleMin}m ago (> ${thresholdMin}m threshold)` }
}

export interface IdleReapResult {
  reaped: { id: string; reason: string }[]
  kept: number
}

/**
 * Run one idle-reap pass over the live registry. Only called from the
 * PERIODIC sweep (see sweep.ts's startPruneSweeps) — never at startup, for
 * the identical reason the periodic channel-prune sweep withholds its idle
 * rule at startup: lastPollByChannel has no data yet, so every agent would
 * read as "no liveness data" and be skipped anyway, but running it at startup
 * would also mean the FIRST periodic tick after a restart judges agents on
 * freshness accumulated in under an hour — deliberately deferred here too.
 */
export function idleReapSweep(nowMs: number = Date.now()): IdleReapResult {
  const reaped: IdleReapResult["reaped"] = []
  let kept = 0
  const thresholdMs = idleReapThresholdMs()
  // Snapshot ids first — unregisterAgent mutates the map we'd otherwise iterate.
  for (const [id, info] of [...agents.entries()]) {
    const decision = shouldIdleReap({
      id,
      launchedBy: info.launchedBy,
      lastSeenMs: lastPollByChannel.get(id),
      nowMs,
      thresholdMs,
    })
    if (!decision.reap) { kept++; continue }
    const res = unregisterAgent(id)
    if (res.ok) {
      reaped.push({ id, reason: decision.reason })
      console.log(`[parlay-idle-reap] shut down "${id}" — ${decision.reason}`)
    } else {
      // Should not happen (id came from the live map), but never swallow it —
      // matches pruneChannels's own defensive log line.
      console.warn(`[parlay-idle-reap] failed to shut down "${id}": ${res.error}`)
    }
  }
  if (reaped.length) {
    console.log(`[parlay-idle-reap] reaped ${reaped.length}, kept ${kept}`)
  }
  return { reaped, kept }
}

let idleReapTimer: ReturnType<typeof setInterval> | null = null

/**
 * Wire the periodic idle-reap sweep. Deliberately no startup pass (see the
 * doc comment on idleReapSweep). Reuses sweep.ts's own PERIODIC_SWEEP_MS
 * cadence (hourly) rather than a second interval constant — one clock is
 * fine since the idle threshold is itself hours, not minutes. Idempotent:
 * a second call does not stack intervals, mirroring startPruneSweeps.
 */
export function startIdleReapSweeps(): void {
  if (idleReapTimer) return
  idleReapTimer = setInterval(() => {
    try { idleReapSweep() } catch (err) { console.warn("[parlay-idle-reap] periodic sweep error:", err) }
  }, PERIODIC_SWEEP_MS)
  if (typeof idleReapTimer.unref === "function") idleReapTimer.unref()
}
