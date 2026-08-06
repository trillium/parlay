// ── Autonomous Parlay channel cleanup ────────────────────────────────────────
//
// Parlay's agent registry (`agents` in sse.ts, persisted to parlay-agents.json)
// grows every time a channel first polls or registers — including throwaway
// test/bench/probe channels that never deregister. Left alone it accretes stale
// tabs the captain has to prune by hand. This module makes cleanup autonomous:
//
//   • shouldPrune(input)      — the pure policy predicate            (policy.ts)
//   • tombstone/isTombstoned  — make a prune STICK against the pruned channel's
//                               own leaked poller             (tombstones.ts)
//   • unregisterAgent(id)     — remove one channel, fail-LOUD on unknown id
//   • pruneChannels(mode)     — one sweep; removes channels shouldPrune() flags
//   • startPruneSweeps()      — startup sweep + hourly interval, wired in
//                               startChat()                        (sweep.ts)
//
// The proper long-term fix is DEREGISTRATION ON EXIT: whatever spawns a channel
// (firstmate's crewmate spawn/teardown, a test harness, a probe script) should
// POST /api/chat/unregister { id } when it finishes, exactly as a task tears
// down its worktree. This sweep is the belt-and-suspenders for when that is
// missed — not a replacement for it. See the brain doc referenced in the report.
//
// Design note on LEAKED POLLERS (robots-ycfa, load-bearing): a leaked channel is
// not passive. A test fixture that enrolled against the live relay and was never
// torn down leaves a listener PROCESS behind, and that process long-polls
// /api/chat/poll forever. Two consequences fall out of that, and both used to
// defeat this module entirely:
//
//   1. Freshness proves nothing — see the rule-ordering note in policy.ts.
//   2. Removal alone does not stick. handlePollRequest auto-registers any
//      channel that polls, so a pruned leak resurrected its own registry row on
//      its next poll (< LISTEN_WINDOW_MS later) — which is how 82 orphan
//      listeners accumulated while an hourly sweep was running. A prune now also
//      records a TOMBSTONE; the poll route refuses to re-create a tombstoned
//      channel and answers 410 Gone so the poller can stop for good.
//
// A tombstone is a statement about a leak, never about a person: an explicit
// re-enrollment (POST /api/chat/register-agent) clears it, so re-arming a real
// agent that shared a pruned id works on the first try.

export {
  PROTECTED_CHANNELS,
  IDLE_PRUNE_MS,
  TEST_NAME_PATTERNS,
  PHANTOM_IDS,
  shouldPrune,
  type PruneMode,
  type PruneDecisionInput,
  type PruneDecision,
} from "./policy"

export {
  TOMBSTONE_TTL_MS,
  tombstones,
  tombstone,
  isTombstoned,
  clearTombstone,
} from "./tombstones"

export {
  unregisterAgent,
  pruneChannels,
  startPruneSweeps,
  PERIODIC_SWEEP_MS,
  type UnregisterResult,
  type PruneSweepResult,
} from "./sweep"
