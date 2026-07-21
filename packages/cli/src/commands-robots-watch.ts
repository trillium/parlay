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

import { spawnSync } from "child_process"
import { existsSync, mkdirSync, readFileSync, writeFileSync, renameSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { EXIT_USAGE } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

const DEFAULT_INTERVAL_SEC = 15

// ── Event model ──────────────────────────────────────────────────────────────
export type BeadStatus = string
export type StoreState = Record<string, BeadStatus> // bead id → status
export type EventKind = "created" | "closed"
export interface RouteEvent { store: string; kind: EventKind; id: string; status: BeadStatus }

// One bead as the store's `list --json` returns it (only the fields we read).
interface Bead { id: string; status?: string; title?: string; labels?: string[] }

// bd's terminal status. Everything else (open/in_progress/blocked) is "live".
function isClosed(status: BeadStatus | undefined): boolean {
  return status === "closed"
}

// ── The watch table (SUBSCRIBE, data-driven) ─────────────────────────────────
// Which store, which transitions we care about. A route key is `<store>:<kind>`;
// the handler table (features 3–4) maps each key to an action. Adding a consumer
// is a new row here, not new machinery — decision-4zr's closed handler registry.
const WATCHES: { store: string; kinds: EventKind[] }[] = [
  { store: "robots", kinds: ["created"] },     // → mechanic-dispatch (handler a)
  { store: "questions", kinds: ["closed"] },   // → notify-requester (handler b)
  { store: "task", kinds: ["closed"] },        // → notify-requester (handler b)
]

// ── Cursor persistence ───────────────────────────────────────────────────────
function stateDir(): string {
  const base = process.env.PARLAY_STATE_HOME || join(homedir(), ".parlay")
  return join(base, "robots-watch")
}
function cursorPath(): string {
  return join(stateDir(), "cursor.json")
}
function readCursor(): Record<string, StoreState> {
  const p = cursorPath()
  if (!existsSync(p)) return {}
  try {
    const parsed = JSON.parse(readFileSync(p, "utf8"))
    return parsed && typeof parsed === "object" ? parsed : {}
  } catch {
    // A corrupt cursor is treated as empty → every store re-seeds (fires nothing),
    // which is the safe failure: we never replay history, we just lose one diff.
    return {}
  }
}
function writeCursor(cursor: Record<string, StoreState>): void {
  const dir = stateDir()
  mkdirSync(dir, { recursive: true })
  const tmp = join(dir, `.cursor.${process.pid}.tmp`)
  writeFileSync(tmp, JSON.stringify(cursor, null, 2) + "\n")
  renameSync(tmp, cursorPath()) // atomic swap
}

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

// ── Pure diff core (unit-tested) ─────────────────────────────────────────────
// Given the PREVIOUS status map for a store (undefined = never seen → SEED) and
// the CURRENT one, return the events to fire for the requested kinds.
//   - SEED (prev undefined): fire nothing; caller adopts curr. No history replay.
//   - created: a bead present now, absent before, and NOT already closed.
//   - closed: a bead we previously saw LIVE that is now closed (open→closed).
// A bead that first appears already-closed fires neither (it's history, not a
// transition we witnessed).
export function detectEvents(
  prev: StoreState | undefined,
  curr: StoreState,
  store: string,
  kinds: EventKind[],
): { events: RouteEvent[]; seeded: boolean } {
  if (prev === undefined) return { events: [], seeded: true }
  const events: RouteEvent[] = []
  const want = new Set(kinds)
  for (const [id, status] of Object.entries(curr)) {
    const before = prev[id]
    if (want.has("created") && before === undefined && !isClosed(status)) {
      events.push({ store, kind: "created", id, status })
    }
    if (want.has("closed") && before !== undefined && !isClosed(before) && isClosed(status)) {
      events.push({ store, kind: "closed", id, status })
    }
  }
  return { events, seeded: false }
}

// ── Store polling (impure) ───────────────────────────────────────────────────
// Run `<store> list --all --json --limit 0`. Returns null (skip this store this
// pass) if the store CLI is missing or errors — never throws, so one bad store
// can't stall the others or kill the daemon.
function listStore(store: string, verbose: boolean): Bead[] | null {
  const r = spawnSync(store, ["list", "--all", "--json", "--limit", "0"], {
    encoding: "utf8",
    env: process.env,
  })
  if (r.error || r.status !== 0 || !r.stdout) {
    if (verbose) process.stderr.write(`robots-watch: skip store '${store}' (${r.error?.message ?? `exit ${r.status}`})\n`)
    return null
  }
  try {
    const parsed = JSON.parse(r.stdout)
    return Array.isArray(parsed) ? (parsed as Bead[]) : []
  } catch {
    if (verbose) process.stderr.write(`robots-watch: skip store '${store}' (unparseable --json)\n`)
    return null
  }
}

// ── One poll pass: poll → diff → route → persist ─────────────────────────────
async function pollOnce(verbose: boolean): Promise<void> {
  const cursor = readCursor()
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
      const bead = beadsById.get(ev.id)
      try {
        await routeEvent(ev, bead, verbose)
      } catch (err) {
        // A failing handler must not abort the pass or lose the rest of the diff.
        process.stderr.write(`robots-watch: handler for ${ev.store}:${ev.kind} ${ev.id} failed: ${String(err)}\n`)
      }
    }
    cursor[store] = curr // adopt current state (seed or advance)
  }
  writeCursor(cursor)
}

// ── Router (ROUTE) ───────────────────────────────────────────────────────────
// Maps `<store>:<kind>` → handler. Features 3–4 replace these stubs with the
// real mechanic-dispatch / notify-requester actions.
async function routeEvent(ev: RouteEvent, bead: Bead | undefined, verbose: boolean): Promise<void> {
  const key = `${ev.store}:${ev.kind}`
  switch (key) {
    case "robots:created":
      if (verbose) process.stderr.write(`robots-watch: [stub] robots:created ${ev.id} → mechanic-dispatch\n`)
      return // handler (a) — feature 3
    case "questions:closed":
    case "task:closed":
      if (verbose) process.stderr.write(`robots-watch: [stub] ${key} ${ev.id} → notify-requester\n`)
      return // handler (b) — feature 4
    default:
      if (verbose) process.stderr.write(`robots-watch: no route for ${key} (${ev.id})\n`)
      // Reference bead so the param is used until handlers land; no behavior.
      void bead
  }
}
