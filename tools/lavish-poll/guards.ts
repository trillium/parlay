// Hot-spin and orphan guards for the lavish-poll bridge (robots-zahn).
//
// An unreachable Parlay does not long-poll. The fetch REJECTS at the transport
// layer in microseconds, index.ts maps that rejection to {timeout:true}, and
// the loop restarts immediately — so with no --timeout-ms (deadline Infinity)
// the bridge spins a core flat out forever. One was found in the wild running
// 21h at 76-98% CPU against a dead http://127.0.0.1:53715, re-parented to
// launchd (ppid 1) after the Claude session that spawned it had exited.
//
// Two independent guards, because either alone leaves a hole:
//   - the retry budget bounds CPU and lifetime when an upstream stops answering
//     but the process still has a parent that would read its output;
//   - the orphan check bounds a process whose reader is already gone, which is
//     true regardless of whether the upstreams are healthy — a bridge parked in
//     a perfectly normal 30s long-poll is just as unwanted once nobody is left
//     to receive the JSON it will print.

export interface Budget {
  /** Wall-clock a consecutive-failure streak may last before giving up. */
  windowMs: number
  /** Consecutive failures allowed before giving up. */
  maxRetries: number
  /** Floor for one loop iteration, and the first backoff step. */
  backoffMs: number
  /** Cap on the doubling, so a long outage still retries periodically. */
  maxBackoffMs: number
  /** How often the watchdog samples process.ppid. */
  orphanCheckMs: number
}

export const DEFAULT_BUDGET: Budget = {
  windowMs: 60_000,
  maxRetries: 30,
  backoffMs: 250,
  maxBackoffMs: 5_000,
  orphanCheckMs: 1_000,
}

/**
 * Env overrides, for tests that must exercise a guard without waiting out its
 * production budget. Not part of the CLI surface — a caller who wants a bound
 * on a healthy run passes --timeout-ms.
 */
export const BUDGET_ENV: Readonly<Record<keyof Budget, string>> = {
  windowMs: "LAVISH_POLL_UNREACHABLE_WINDOW_MS",
  maxRetries: "LAVISH_POLL_MAX_RETRIES",
  backoffMs: "LAVISH_POLL_BACKOFF_MS",
  maxBackoffMs: "LAVISH_POLL_MAX_BACKOFF_MS",
  orphanCheckMs: "LAVISH_POLL_ORPHAN_CHECK_MS",
}

/**
 * Reads the budget, falling back to the default for any value that is not a
 * positive finite number — the same rule --timeout-ms applies, and for the same
 * reason: `Number("abc")` is NaN and `Number("")` is 0, and a guard that
 * accepted either would disarm itself on a typo. `warn` is called for a value
 * that was present and rejected, so a silently ignored override is impossible.
 */
export function readBudget(
  env: Record<string, string | undefined> = process.env,
  warn: (msg: string) => void = () => {},
): Budget {
  const out = { ...DEFAULT_BUDGET }
  for (const key of Object.keys(BUDGET_ENV) as (keyof Budget)[]) {
    const name = BUDGET_ENV[key]
    const raw = env[name]
    if (raw === undefined || raw === "") continue
    const n = Number(raw)
    if (Number.isFinite(n) && n > 0) {
      out[key] = n
      continue
    }
    warn(`ignoring ${name}=${JSON.stringify(raw)} — not a positive number; using ${out[key]}`)
  }
  return out
}

/**
 * Delay before the next attempt after `consecutiveFailures` in a row (1-based),
 * doubling from `backoffMs` and capped at `maxBackoffMs`. Zero for a streak of
 * zero, so a caller can pass the counter straight through.
 */
export function backoffFor(consecutiveFailures: number, budget: Budget): number {
  if (consecutiveFailures < 1) return 0
  // 2 ** big is Infinity rather than an overflow, which Math.min flattens to
  // the cap — no special case needed for a very long outage.
  return Math.min(budget.maxBackoffMs, budget.backoffMs * 2 ** (consecutiveFailures - 1))
}

/**
 * True once a run of consecutive transport failures has spent either bound.
 * Both are checked: the retry count alone is meaningless without knowing how
 * fast the retries came, and the window alone would let a slow-failing upstream
 * hold the process open for the full window on a handful of attempts.
 */
export function budgetSpent(
  consecutiveFailures: number,
  firstFailureAt: number,
  now: number,
  budget: Budget,
): boolean {
  if (consecutiveFailures < 1) return false
  return consecutiveFailures >= budget.maxRetries || now - firstFailureAt >= budget.windowMs
}

/**
 * A ppid of 1 means the spawning session is gone and this process was
 * re-parented to launchd/init: there is no longer anyone to read the JSON line
 * the bridge exists to print, so continuing to poll can only burn CPU.
 */
export function isOrphaned(ppid: number): boolean {
  return ppid === 1
}
