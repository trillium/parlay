// parlay robots-watch — the MVP event poll-daemon (decision-4zr interim bridge).
//
// The durable design (docs/CLI_VERBS_AND_EVENTS.md §2.4) is: beads owns EMIT
// (an app-blind on-status-change hook), parlay owns SUBSCRIBE+ROUTE+DELIVER.
// Until the beads EMIT hook exists (tracked as task-n1ao), parlay STANDS IN for
// the missing emit with this poll loop: it polls each watched store's
// `<store> list --all --json`, diffs a persisted per-bead status cursor, and
// routes each detected (store, status-change) through a handler table. When the
// real EMIT lands, only the source swaps — the router + handlers are unchanged.
//
// Two shipped handlers:
//   (a) robots bead CREATED  → `mechanic-dispatch <id>` (idempotent launch/route)
//   (b) request/question/task CLOSED → notify the requester channel named by the
//       bead's `notify:<channel>` label, via `parlay send --<channel>`.
//
// Zero server change, zero unverified bd internals. Run under launchd for the
// interim bridge; retired once beads EMIT + the ingest endpoint exist.
//
// Usage:
//   parlay robots-watch [--interval <sec>] [--once] [--verbose]
//     --interval <sec>   poll cadence (default 15). Ignored with --once.
//     --once             run a single poll pass and exit (for tests / cron).
//     --verbose          log every poll pass + routed event to stderr.
//
// State: $PARLAY_STATE_HOME/robots-watch/cursor.json (default ~/.parlay/…),
// a { "<store>": { "<bead-id>": "<status>" } } map. FIRST sighting of a store
// SEEDS its cursor and fires nothing (no replaying history on startup); only
// transitions observed AFTER seeding fire handlers.

import { EXIT_USAGE } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

const DEFAULT_INTERVAL_SEC = 15

export async function cmdRobotsWatch(args: string[]): Promise<void> {
  if (helpWanted("robots-watch", args)) return
  const { opts } = parseArgs("robots-watch", args, ["--once", "--verbose"], ["--interval"])

  const once = opts["--once"] === true
  const verbose = opts["--verbose"] === true
  const intervalRaw = (opts["--interval"] as string | undefined)?.trim()
  let intervalSec = DEFAULT_INTERVAL_SEC
  if (intervalRaw !== undefined) {
    const n = Number(intervalRaw)
    if (!Number.isFinite(n) || n <= 0) {
      return die(`parlay robots-watch: --interval must be a positive number of seconds (got '${intervalRaw}')`, EXIT_USAGE)
    }
    intervalSec = n
  }

  process.stderr.write(
    `parlay robots-watch — ${once ? "single pass" : `polling every ${intervalSec}s`}` +
      ` (handlers: robots-created→mechanic-dispatch, request-closed→notify)\n`,
  )

  // Feature 2 fills in the poll+cursor loop; features 3–4 add the handlers.
  await pollOnce(verbose)
  if (once) return

  // Continuous loop for the launchd daemon. Bun.sleep keeps the process alive
  // between passes without spinning the CPU.
  // eslint-disable-next-line no-constant-condition
  while (true) {
    await Bun.sleep(intervalSec * 1000)
    try {
      await pollOnce(verbose)
    } catch (err) {
      // A single bad pass (store CLI hiccup, transient error) must never kill the
      // daemon — log and try again next tick.
      process.stderr.write(`parlay robots-watch: poll pass failed (continuing): ${String(err)}\n`)
    }
  }
}

// One poll pass. Feature 2 implements the real store-poll + cursor diff + routing;
// this stub keeps the verb runnable as its own commit.
async function pollOnce(_verbose: boolean): Promise<void> {
  // no-op until Feature 2
}
