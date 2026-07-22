// parlay robots-watch — the MVP event poll-daemon (decision-4zr interim bridge).
//
// The durable design (docs/CLI_VERBS_AND_EVENTS.md §2.4) is: beads owns EMIT (an
// app-blind on-status-change hook), parlay owns SUBSCRIBE+ROUTE+DELIVER. Until
// the beads EMIT hook exists (task-n1ao), parlay STANDS IN for the missing emit
// with this poll loop: it polls each watched store's `<store> list --all --json`,
// diffs a persisted per-bead status cursor, and routes each detected
// (store, status-change) through a handler table. When the real EMIT lands, only
// the source swaps — the router + handlers are unchanged.
//
// Shipped handlers:
//   (a) robots bead CREATED  → mechanic-dispatch <id> (idempotent)
//   (b) request/question/task CLOSED → notify the requester channel(s) named by
//       the bead's notify:<channel> label, via `parlay send --<channel>`.
//
// Usage: parlay robots-watch [--interval <sec>] [--once] [--verbose]
// State: $PARLAY_STATE_HOME/robots-watch/cursor.json (default ~/.parlay/…). First
// sighting of a store SEEDS its cursor and fires nothing (no history replay).

import { EXIT_USAGE } from "../config"
import { die } from "../http"
import { parseArgs } from "../args"
import { helpWanted } from "../help"
import { detectEvents, type StoreState } from "./detect"
import { readCursor, writeCursor, type Cursor } from "./cursor"
import { WATCHES, listStore, routeEvent } from "./handlers"

// Re-export the pure core for unit tests (../commands-robots-watch.test.ts).
export { detectEvents, notifyChannels } from "./detect"
export type { StoreState } from "./detect"

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

  pollOnce(verbose)
  if (once) return

  // Continuous loop for the launchd daemon.
  // eslint-disable-next-line no-constant-condition
  while (true) {
    await Bun.sleep(intervalSec * 1000)
    try {
      pollOnce(verbose)
    } catch (err) {
      // A single bad pass must never kill the daemon — log and retry next tick.
      process.stderr.write(`robots-watch: poll pass failed (continuing): ${String(err)}\n`)
    }
  }
}

// One poll pass: poll → diff → route → persist.
function pollOnce(verbose: boolean): void {
  const cursor: Cursor = readCursor()
  for (const { store, kinds } of WATCHES) {
    const beads = listStore(store, verbose)
    if (beads === null) continue // store unavailable this pass; keep prior cursor
    const beadsById = new Map(beads.map(b => [b.id, b]))
    const curr: StoreState = {}
    for (const b of beads) curr[b.id] = b.status ?? "open"

    const { events, seeded } = detectEvents(cursor[store], curr, store, kinds)
    if (verbose) {
      process.stderr.write(
        `robots-watch: ${store} — ${beads.length} beads, ${seeded ? "SEEDED (no fire)" : `${events.length} event(s)`}\n`,
      )
    }
    for (const ev of events) {
      try {
        routeEvent(ev, beadsById.get(ev.id), verbose)
      } catch (err) {
        // A failing handler must not abort the pass or lose the rest of the diff.
        process.stderr.write(`robots-watch: handler for ${ev.store}:${ev.kind} ${ev.id} failed: ${String(err)}\n`)
      }
    }
    cursor[store] = curr // adopt current state (seed or advance)
  }
  writeCursor(cursor)
}
