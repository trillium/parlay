// Runtime half of the hot-spin/orphan guards (robots-zahn). guards.ts holds the
// pure arithmetic; this file holds the two things that act on it — the watchdog
// that ends an unread run, and the pacer that keeps a failing upstream from
// becoming a busy loop. Split out of index.ts to keep the poll loop readable.

import { backoffFor, budgetSpent, isOrphaned, type Budget } from "./guards"

/**
 * Ends the process once its parent is gone. Checked immediately and then
 * sampled on a timer, because the poll loop cannot be the only thing that
 * notices: a healthy Parlay parks it in a ~30s long-poll and a stalling one
 * parks it until the deadline, so a check at the top of each iteration would
 * leave a parentless process alive for minutes — or, with no --timeout-ms,
 * forever. The timer is unref'd so the watchdog never keeps the process alive
 * by itself.
 *
 * Install this AFTER any --agent-reply POST: that reply goes to the captain's
 * chat rather than to this process's parent, so it is still worth delivering.
 * Only the polling half is pointless once nobody is left to read stdout.
 */
export function installOrphanWatchdog(budget: Budget): void {
  const exitIfOrphaned = () => {
    if (!isOrphaned(process.ppid)) return
    process.stderr.write("lavish-poll: parent is gone (ppid 1) — exiting rather than polling unread\n")
    process.exit(0)
  }
  exitIfOrphaned()
  setInterval(exitIfOrphaned, budget.orphanCheckMs).unref()
}

/** What one loop iteration produced, which is what decides how long to wait. */
export type Outcome =
  /** The upstream never answered — count it against the give-up budget. */
  | "failed"
  /** Nothing to deliver, but the upstream is alive. */
  | "idle"
  /** A message was consumed; the failure streak is over. */
  | "progress"

export interface Pacer {
  /**
   * Closes out one iteration: records its outcome, gives up if the retry budget
   * is spent, and sleeps whatever is left of that iteration's floor.
   */
  settle(iterStarted: number, outcome?: Outcome): Promise<void>
}

/**
 * An iteration that delivered nothing and returned faster than the floor is not
 * polling, it is spinning — against a refused connection every leg settles in
 * microseconds. Consecutive failures back off exponentially; anything else uses
 * the flat floor, so a genuinely busy channel is barely slowed by a guard aimed
 * at a dead one. Waits are capped by whatever is left of --timeout-ms, for the
 * same reason the native grace window is: a caller passing --timeout-ms 100 must
 * not get 300ms back.
 */
export function createPacer(budget: Budget, deadline: number, upstream: string): Pacer {
  let failStreak = 0
  let failSince = 0

  const giveUp = (): never => {
    const secs = Math.round((Date.now() - failSince) / 1000)
    process.stderr.write(
      `lavish-poll: ${upstream} unreachable — ${failStreak} consecutive failed polls over ${secs}s; ` +
        `giving up rather than spinning against a dead port\n`,
    )
    // Exit 1 with nothing on stdout, matching how every other unrecoverable
    // condition here reports: a caller parsing the last JSON line must not find
    // a record that reads like a completed poll when nothing ever answered.
    process.exit(1)
  }

  return {
    async settle(iterStarted, outcome = "idle") {
      if (outcome === "failed") {
        if (failStreak === 0) failSince = Date.now()
        failStreak++
        if (budgetSpent(failStreak, failSince, Date.now(), budget)) giveUp()
      } else if (outcome === "progress") {
        failStreak = 0
      }
      const floor = failStreak > 0 ? backoffFor(failStreak, budget) : budget.backoffMs
      const wait = Math.min(floor - (Date.now() - iterStarted), deadline - Date.now())
      if (wait > 0) await Bun.sleep(wait)
    },
  }
}
