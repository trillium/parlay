// The watch table (SUBSCRIBE), store polling, the router (ROUTE), and the two
// shipped handlers (DELIVER). decision-4zr: parlay owns subscribe+route+deliver.

import { spawnSync } from "child_process"
import { notifyChannels, type RouteEvent, type EventKind, type Bead } from "./detect"

// Which store, which transitions we care about. A route key is `<store>:<kind>`.
// Adding a consumer is a new row here, not new machinery — the closed handler
// registry of decision-4zr.
export const WATCHES: { store: string; kinds: EventKind[] }[] = [
  // robots: created → mechanic-dispatch (handler a); closed → notify the
  // originating agent stamped on the bead as notify:<channel> (robots-3q7n).
  { store: "robots", kinds: ["created", "closed"] },
  { store: "questions", kinds: ["closed"] },   // → notify-requester (handler b)
  { store: "task", kinds: ["closed"] },        // → notify-requester (handler b)
]

// Run `<store> list --all --json --limit 0`. Returns null (skip this store this
// pass) if the store CLI is missing or errors — never throws, so one bad store
// can't stall the others or kill the daemon.
export function listStore(store: string, verbose: boolean): Bead[] | null {
  const r = spawnSync(store, ["list", "--all", "--json", "--limit", "0"], { encoding: "utf8", env: process.env })
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

// Maps `<store>:<kind>` → handler.
export function routeEvent(ev: RouteEvent, bead: Bead | undefined, verbose: boolean): void {
  const key = `${ev.store}:${ev.kind}`
  switch (key) {
    case "robots:created":
      return handleRobotsCreated(ev, verbose) // handler (a)
    case "robots:closed":
    case "questions:closed":
    case "task:closed":
      return handleRequestClosed(ev, bead, verbose) // handler (b)
    default:
      if (verbose) process.stderr.write(`robots-watch: no route for ${key} (${ev.id})\n`)
      void bead
  }
}

// ── Handler (a): robots bead CREATED → mechanic-dispatch <id> ─────────────────
// mechanic-dispatch is idempotent (checks if the zone's mechanic is live and
// launches via parlay-spawn only if not) and resolves the zone from the ticket's
// `zone:<x>` label itself — so we pass only the id.
function handleRobotsCreated(ev: RouteEvent, verbose: boolean): void {
  dispatchMechanic(ev.id, verbose)
}

// The reusable dispatch: spawn `mechanic-dispatch <id>` (idempotent — checks the
// zone's mechanic liveness and launches via parlay-spawn only if down). Shared by
// the POLL path (handler a) and the TAILER fast path (robots-tail, task-jif2), so
// both triggers converge on one dispatch. Failure-isolated: never throws.
export function dispatchMechanic(id: string, verbose: boolean): void {
  const r = spawnSync("mechanic-dispatch", [id], { encoding: "utf8", env: process.env })
  if (r.error) {
    process.stderr.write(`robots-watch: mechanic-dispatch not runnable for ${id}: ${r.error.message}\n`)
    return
  }
  const out = [r.stdout, r.stderr].filter(Boolean).join("").trim()
  if (r.status === 0) {
    process.stderr.write(`robots-watch: dispatched mechanic for ${id}${verbose && out ? ` — ${out}` : ""}\n`)
  } else {
    process.stderr.write(`robots-watch: mechanic-dispatch ${id} exited ${r.status}: ${out}\n`)
  }
}

// ── Handler (b): request/question/task CLOSED → notify requester ──────────────
// DELIVER: `parlay send --<channel> "<text>"` for each subscribed channel. A
// monitor on that channel (e.g. firstmate on `mayor`) wakes and reads it. We
// shell out to the `parlay` wrapper rather than call postJSON in-process on
// purpose: (1) the wrapper resolves PARLAY_SERVER (the Pulse :31337 target)
// exactly as every other caller does, and (2) it is a SEPARATE process, so a
// server-unreachable failure is a captured non-zero exit — it can NEVER exit the
// daemon (postJSON die()s on a network error, which would kill the loop).
function handleRequestClosed(ev: RouteEvent, bead: Bead | undefined, verbose: boolean): void {
  const channels = notifyChannels(bead?.labels)
  if (channels.length === 0) {
    if (verbose) process.stderr.write(`robots-watch: ${ev.id} closed but no notify:<channel> label — no subscriber\n`)
    return
  }
  const title = bead?.title?.trim()
  const text = `✅ ${ev.id} closed${title ? ` — ${title}` : ""}`
  for (const channel of channels) {
    const r = spawnSync("parlay", ["send", `--${channel}`, text], { encoding: "utf8", env: process.env })
    if (r.error) {
      process.stderr.write(`robots-watch: notify '${channel}' of ${ev.id} — parlay not runnable: ${r.error.message}\n`)
    } else if (r.status !== 0) {
      process.stderr.write(`robots-watch: notify '${channel}' of ${ev.id} exited ${r.status}: ${[r.stdout, r.stderr].filter(Boolean).join("").trim()}\n`)
    } else {
      process.stderr.write(`robots-watch: notified '${channel}' — ${ev.id} closed\n`)
    }
  }
}
